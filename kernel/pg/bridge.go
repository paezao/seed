package pg

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// Bridge gives sandboxes an external PostgreSQL server as a Unix socket.
//
// Sandboxed code reaches the database only through a socket mounted at
// /run/seed-db; the live organism has no network at all. With an external
// server (DATABASE_URL), the kernel listens on that socket and forwards each
// connection to the server, adding TLS on the way out (libpq never uses TLS
// over Unix sockets, so the sandbox side is plaintext, inside my container).
// Sandboxes still authenticate as their own roles: the bridge carries no
// credentials.
type Bridge struct {
	// SocketDir holds .s.PGSQL.5432 (mounted read-only into sandboxes).
	SocketDir string

	addr    string // host:port of the server
	host    string
	sslmode string
	roots   *x509.CertPool
	l       net.Listener
}

const sslRequestCode = 80877103

// StartBridge listens in dir for connections to the server in adminURL.
func StartBridge(dir, adminURL string) (*Bridge, error) {
	u, err := url.Parse(adminURL)
	if err != nil {
		return nil, err
	}
	q := u.Query()
	host, port := u.Hostname(), u.Port()
	if h := q.Get("host"); h != "" {
		host = h
	}
	if p := q.Get("port"); p != "" {
		port = p
	}
	if host == "" || strings.HasPrefix(host, "/") {
		return nil, errors.New("the database URL must name a TCP host")
	}
	if port == "" {
		port = "5432"
	}
	b := &Bridge{SocketDir: dir, addr: net.JoinHostPort(host, port), host: host, sslmode: q.Get("sslmode")}
	if b.sslmode == "" {
		b.sslmode = "prefer" // libpq's default
	}
	switch b.sslmode {
	case "disable", "allow", "prefer", "require", "verify-ca", "verify-full":
	default:
		return nil, fmt.Errorf("sslmode %q is not supported", b.sslmode)
	}
	if rc := q.Get("sslrootcert"); rc != "" && rc != "system" {
		pem, err := os.ReadFile(rc)
		if err != nil {
			return nil, fmt.Errorf("sslrootcert: %w", err)
		}
		b.roots = x509.NewCertPool()
		if !b.roots.AppendCertsFromPEM(pem) {
			return nil, errors.New("sslrootcert has no certificates")
		}
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, err
	}
	sock := filepath.Join(dir, ".s.PGSQL.5432")
	_ = os.Remove(sock)
	l, err := net.Listen("unix", sock)
	if err != nil {
		return nil, err
	}
	_ = os.Chmod(sock, 0o700)
	b.l = l
	go b.serve()
	return b, nil
}

func (b *Bridge) Close() error { return b.l.Close() }

func (b *Bridge) serve() {
	for {
		c, err := b.l.Accept()
		if err != nil {
			return
		}
		go func() {
			if err := b.handle(c); err != nil && !errors.Is(err, io.EOF) {
				slog.Debug("database bridge", "err", err)
			}
		}()
	}
}

func (b *Bridge) handle(client net.Conn) error {
	defer client.Close()
	// A client that asks for TLS (or GSS encryption) over the socket is told
	// no, as a PostgreSQL server would on a Unix socket; then it starts up.
	first, err := readStartupPacket(client)
	if err != nil {
		return err
	}
	for isEncryptionRequest(first) {
		if _, err := client.Write([]byte{'N'}); err != nil {
			return err
		}
		if first, err = readStartupPacket(client); err != nil {
			return err
		}
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	server, err := b.dial(ctx)
	if err != nil {
		return err
	}
	defer server.Close()
	if _, err := server.Write(first); err != nil {
		return err
	}
	done := make(chan struct{}, 2)
	go func() { _, _ = io.Copy(server, client); done <- struct{}{} }()
	go func() { _, _ = io.Copy(client, server); done <- struct{}{} }()
	<-done
	return nil
}

// dial connects to the server, negotiating TLS as sslmode says.
func (b *Bridge) dial(ctx context.Context) (net.Conn, error) {
	d := &net.Dialer{Timeout: 10 * time.Second, KeepAlive: 30 * time.Second}
	raw, err := d.DialContext(ctx, "tcp", b.addr)
	if err != nil {
		return nil, err
	}
	if b.sslmode == "disable" || b.sslmode == "allow" {
		return raw, nil
	}
	req := make([]byte, 8)
	binary.BigEndian.PutUint32(req[0:4], 8)
	binary.BigEndian.PutUint32(req[4:8], sslRequestCode)
	if _, err := raw.Write(req); err != nil {
		raw.Close()
		return nil, err
	}
	resp := make([]byte, 1)
	if _, err := io.ReadFull(raw, resp); err != nil {
		raw.Close()
		return nil, err
	}
	if resp[0] != 'S' {
		if b.sslmode == "prefer" {
			return raw, nil
		}
		raw.Close()
		return nil, fmt.Errorf("the database server refused TLS (sslmode=%s)", b.sslmode)
	}
	cfg := &tls.Config{ServerName: b.host, RootCAs: b.roots, MinVersion: tls.VersionTLS12}
	switch b.sslmode {
	case "prefer", "require":
		// libpq semantics: encrypt, but verify only with a root certificate.
		if b.roots == nil {
			cfg.InsecureSkipVerify = true
		}
	case "verify-ca":
		cfg.InsecureSkipVerify = true
		cfg.VerifyPeerCertificate = verifyChainOnly(b.roots)
	}
	tc := tls.Client(raw, cfg)
	if err := tc.HandshakeContext(ctx); err != nil {
		raw.Close()
		return nil, err
	}
	return tc, nil
}

// verifyChainOnly checks the certificate chain but not the host name.
func verifyChainOnly(roots *x509.CertPool) func([][]byte, [][]*x509.Certificate) error {
	return func(raw [][]byte, _ [][]*x509.Certificate) error {
		var certs []*x509.Certificate
		for _, r := range raw {
			c, err := x509.ParseCertificate(r)
			if err != nil {
				return err
			}
			certs = append(certs, c)
		}
		if len(certs) == 0 {
			return errors.New("no server certificate")
		}
		inter := x509.NewCertPool()
		for _, c := range certs[1:] {
			inter.AddCert(c)
		}
		_, err := certs[0].Verify(x509.VerifyOptions{Roots: roots, Intermediates: inter})
		return err
	}
}

func readStartupPacket(r io.Reader) ([]byte, error) {
	hdr := make([]byte, 4)
	if _, err := io.ReadFull(r, hdr); err != nil {
		return nil, err
	}
	n := binary.BigEndian.Uint32(hdr)
	if n < 8 || n > 10000 {
		return nil, fmt.Errorf("bad startup packet length %d", n)
	}
	pkt := make([]byte, n)
	copy(pkt, hdr)
	if _, err := io.ReadFull(r, pkt[4:]); err != nil {
		return nil, err
	}
	return pkt, nil
}

func isEncryptionRequest(pkt []byte) bool {
	if len(pkt) != 8 {
		return false
	}
	code := binary.BigEndian.Uint32(pkt[4:8])
	return code == sslRequestCode || code == 80877104 // GSSENCRequest
}
