package pg

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/binary"
	"encoding/pem"
	"io"
	"math/big"
	"net"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// fakeServer speaks just enough PostgreSQL: answers an SSLRequest, then
// echoes what it receives (over TLS if negotiated) and reports whether TLS
// was used.
func fakeServer(t *testing.T, acceptTLS bool) (addr string, certPEM []byte, gotTLS chan bool) {
	t.Helper()
	key, _ := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	tmpl := &x509.Certificate{SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: "localhost"},
		DNSNames: []string{"localhost"}, NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(time.Hour),
		IsCA: true, BasicConstraintsValid: true, KeyUsage: x509.KeyUsageDigitalSignature | x509.KeyUsageCertSign}
	der, _ := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	certPEM = pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})
	cert := tls.Certificate{Certificate: [][]byte{der}, PrivateKey: key}
	l, _ := net.Listen("tcp", "127.0.0.1:0")
	t.Cleanup(func() { l.Close() })
	gotTLS = make(chan bool, 10)
	go func() {
		for {
			c, err := l.Accept()
			if err != nil {
				return
			}
			go func(c net.Conn) {
				defer c.Close()
				pkt, err := readStartupPacket(c)
				if err != nil {
					return
				}
				var conn net.Conn = c
				if isEncryptionRequest(pkt) {
					if !acceptTLS {
						c.Write([]byte{'N'})
						gotTLS <- false
					} else {
						c.Write([]byte{'S'})
						tc := tls.Server(c, &tls.Config{Certificates: []tls.Certificate{cert}})
						if tc.Handshake() != nil {
							return
						}
						conn = tc
						gotTLS <- true
					}
				} else {
					gotTLS <- false
					conn.Write(pkt) // echo the startup packet back
				}
				io.Copy(conn, conn)
			}(c)
		}
	}()
	return l.Addr().String(), certPEM, gotTLS
}

func startup() []byte {
	body := []byte("user\x00organism\x00database\x00app\x00\x00")
	pkt := make([]byte, 8+len(body))
	binary.BigEndian.PutUint32(pkt[0:4], uint32(len(pkt)))
	binary.BigEndian.PutUint32(pkt[4:8], 196608) // protocol 3.0
	copy(pkt[8:], body)
	return pkt
}

func through(t *testing.T, b *Bridge, sslFirst bool) []byte {
	t.Helper()
	c, err := net.Dial("unix", filepath.Join(b.SocketDir, ".s.PGSQL.5432"))
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	c.SetDeadline(time.Now().Add(5 * time.Second))
	if sslFirst { // a client asking for TLS over the socket is told no
		req := make([]byte, 8)
		binary.BigEndian.PutUint32(req[0:4], 8)
		binary.BigEndian.PutUint32(req[4:8], sslRequestCode)
		c.Write(req)
		resp := make([]byte, 1)
		if _, err := io.ReadFull(c, resp); err != nil || resp[0] != 'N' {
			t.Fatalf("expected N, got %q %v", resp, err)
		}
	}
	msg := append(startup(), []byte("SELECT 1")...)
	c.Write(msg)
	out := make([]byte, len(msg))
	if _, err := io.ReadFull(c, out); err != nil {
		return nil
	}
	return out
}

func TestBridgeAddsTLS(t *testing.T) {
	addr, certPEM, gotTLS := fakeServer(t, true)
	host, port, _ := net.SplitHostPort(addr)
	_ = host
	roots := filepath.Join(t.TempDir(), "root.pem")
	os.WriteFile(roots, certPEM, 0o600)
	for _, mode := range []string{"require", "verify-full"} {
		u := "postgres://admin:pw@localhost:" + port + "/postgres?sslmode=" + mode
		if mode == "verify-full" {
			u += "&sslrootcert=" + roots
		}
		b, err := StartBridge(filepath.Join(t.TempDir(), "s"), u)
		if err != nil {
			t.Fatal(err)
		}
		if out := through(t, b, true); string(out) != string(append(startup(), []byte("SELECT 1")...)) {
			t.Fatalf("%s: bytes should pass through unchanged: %q", mode, out)
		}
		if !<-gotTLS {
			t.Fatalf("%s: the bridge must use TLS to the server", mode)
		}
		b.Close()
	}
	// verify-full without the right root fails closed.
	b, _ := StartBridge(filepath.Join(t.TempDir(), "s"), "postgres://a@localhost:"+port+"/x?sslmode=verify-full")
	if out := through(t, b, false); out != nil {
		t.Fatal("an unverifiable server must not get the connection")
	}
	b.Close()
}

func TestBridgeRequireRefusesPlaintext(t *testing.T) {
	addr, _, _ := fakeServer(t, false)
	b, err := StartBridge(filepath.Join(t.TempDir(), "s"), "postgres://a@"+addr+"/x?sslmode=require")
	if err != nil {
		t.Fatal(err)
	}
	defer b.Close()
	if out := through(t, b, false); out != nil {
		t.Fatal("sslmode=require must not fall back to plaintext")
	}
	p, err := StartBridge(filepath.Join(t.TempDir(), "p"), "postgres://a@"+addr+"/x?sslmode=prefer")
	if err != nil {
		t.Fatal(err)
	}
	defer p.Close()
	if out := through(t, p, false); out == nil {
		t.Fatal("sslmode=prefer may fall back")
	}
}

func TestBridgeRefusesBadURLs(t *testing.T) {
	for _, u := range []string{"postgres:///x?host=/run/pg", "postgres://a@h/x?sslmode=bogus"} {
		if _, err := StartBridge(t.TempDir(), u); err == nil {
			t.Errorf("%s should be refused", u)
		}
	}
}
