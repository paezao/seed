package runtime

import (
	"bytes"
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"

	"seed/kernel/memory"
)

// Egress is my live organism's only way out. The organism runs in its own
// network namespace with no network; inside it, HTTPS_PROXY points at a
// socket to this proxy. It allows HTTPS (CONNECT to port 443) to the hosts my
// owner approved, and nothing else: no plain HTTP, no IP addresses, and never
// an address that resolves somewhere private (my own container, the host,
// cloud metadata). It dials the exact address it checked, so DNS can't
// change the answer in between.
type Egress struct {
	store *memory.Store

	mu      sync.RWMutex
	hosts   []memory.EgressGrant
	secrets []memory.EgressGrant
	denied  []Denial

	// OnSecretsChanged restarts the organism so it sees its new environment.
	OnSecretsChanged func()

	resolve func(ctx context.Context, host string) ([]net.IPAddr, error)
	dial    func(ctx context.Context, addr string) (net.Conn, error)
	slots   chan struct{}
}

// Denial is a connection the organism tried and was refused (shown to the
// owner and to me, so a missing grant is easy to spot).
type Denial struct {
	Host   string    `json:"host"`
	Reason string    `json:"reason"`
	At     time.Time `json:"at"`
}

const (
	maxEgressConns = 64
	maxDenials     = 50
	egressIdle     = 5 * time.Minute
)

func NewEgress(ctx context.Context, store *memory.Store) (*Egress, error) {
	d := &net.Dialer{Timeout: 10 * time.Second}
	e := &Egress{store: store, slots: make(chan struct{}, maxEgressConns),
		resolve: net.DefaultResolver.LookupIPAddr,
		dial:    func(ctx context.Context, addr string) (net.Conn, error) { return d.DialContext(ctx, "tcp", addr) }}
	return e, e.reload(ctx)
}

func (e *Egress) reload(ctx context.Context) error {
	grants, err := e.store.EgressGrants(ctx)
	if err != nil {
		return err
	}
	var hosts, secrets []memory.EgressGrant
	for _, g := range grants {
		if g.Kind == "host" {
			hosts = append(hosts, g)
		} else {
			secrets = append(secrets, g)
		}
	}
	e.mu.Lock()
	e.hosts, e.secrets = hosts, secrets
	e.mu.Unlock()
	return nil
}

// Grants returns what the organism is allowed.
func (e *Egress) Grants() (hosts, secrets []memory.EgressGrant) {
	e.mu.RLock()
	defer e.mu.RUnlock()
	return append([]memory.EgressGrant(nil), e.hosts...), append([]memory.EgressGrant(nil), e.secrets...)
}

// SecretNames lists the secrets the organism may read.
func (e *Egress) SecretNames() []string {
	_, secrets := e.Grants()
	out := make([]string, 0, len(secrets))
	for _, s := range secrets {
		out = append(out, s.Value)
	}
	return out
}

// Denials returns recent refused connections, newest first.
func (e *Egress) Denials() []Denial {
	e.mu.RLock()
	defer e.mu.RUnlock()
	out := make([]Denial, len(e.denied))
	for i, d := range e.denied {
		out[len(e.denied)-1-i] = d
	}
	return out
}

var (
	hostPatternRe = regexp.MustCompile(`^(\*\.)?([a-z0-9]([a-z0-9-]{0,61}[a-z0-9])?\.)+[a-z][a-z0-9-]{0,61}[a-z0-9]$`)
	labelRe       = regexp.MustCompile(`^[a-z0-9]([a-z0-9-]{0,61}[a-z0-9])?$`)
	secretNameRe  = regexp.MustCompile(`^[A-Z][A-Z0-9_]{1,63}$`)
	// Names under my own control, never an organism secret.
	reservedSecrets = map[string]bool{"DATABASE_URL": true, "PORT": true, "PATH": true, "HOME": true,
		"HTTP_PROXY": true, "HTTPS_PROXY": true, "NO_PROXY": true, "ALL_PROXY": true, "NODE_USE_ENV_PROXY": true}
	internalSuffixes = []string{"localhost", "local", "internal", "arpa", "lan", "home", "corp", "intranet", "test", "invalid"}
)

// ValidateHostPattern checks a host the owner is asked to allow:
// "api.example.com" or "*.example.com" (subdomains, not the apex).
func ValidateHostPattern(p string) (string, error) {
	p = strings.TrimSuffix(strings.ToLower(strings.TrimSpace(p)), ".")
	if !hostPatternRe.MatchString(p) {
		return "", fmt.Errorf("%q is not a host name (like api.example.com or *.example.com; no IP addresses, ports or URLs)", p)
	}
	labels := strings.Split(strings.TrimPrefix(p, "*."), ".")
	if strings.HasPrefix(p, "*.") && len(labels) < 2 {
		return "", fmt.Errorf("%q is too broad", p)
	}
	for _, s := range internalSuffixes {
		if labels[len(labels)-1] == s {
			return "", fmt.Errorf("%q is an internal name; the organism may only reach the public internet", p)
		}
	}
	return p, nil
}

// ValidateSecretName checks a secret the organism is asked to read.
func ValidateSecretName(n string) (string, error) {
	n = strings.TrimSpace(n)
	if !secretNameRe.MatchString(n) {
		return "", fmt.Errorf("%q is not a secret name (UPPER_SNAKE_CASE)", n)
	}
	if reservedSecrets[n] || strings.HasPrefix(n, "SEED_") {
		return "", fmt.Errorf("%s is mine; it can't be given to the organism", n)
	}
	return n, nil
}

// Grant records what the owner approved.
func (e *Egress) Grant(ctx context.Context, hosts, secrets []string, reason string) error {
	for _, h := range hosts {
		if err := e.store.AddEgressGrant(ctx, "host", h, reason); err != nil {
			return err
		}
	}
	for _, s := range secrets {
		if err := e.store.AddEgressGrant(ctx, "secret", s, reason); err != nil {
			return err
		}
	}
	if err := e.reload(ctx); err != nil {
		return err
	}
	if len(secrets) > 0 && e.OnSecretsChanged != nil {
		e.OnSecretsChanged()
	}
	return nil
}

// Revoke withdraws a grant.
func (e *Egress) Revoke(ctx context.Context, kind, value string) error {
	if err := e.store.DeleteEgressGrant(ctx, kind, value); err != nil {
		return err
	}
	if err := e.reload(ctx); err != nil {
		return err
	}
	if kind == "secret" && e.OnSecretsChanged != nil {
		e.OnSecretsChanged()
	}
	return nil
}

func (e *Egress) allowed(host string) bool {
	e.mu.RLock()
	defer e.mu.RUnlock()
	for _, g := range e.hosts {
		if g.Value == host || (strings.HasPrefix(g.Value, "*.") && strings.HasSuffix(host, g.Value[1:])) {
			return true
		}
	}
	return false
}

// safeHost is how a requested host is shown (to my owner and in my own
// context): only well-formed names and addresses, never arbitrary text an
// organism put in a request.
func safeHost(raw string) string {
	if len(raw) > 253 {
		return "(invalid name)"
	}
	if ip := net.ParseIP(strings.Trim(raw, "[]")); ip != nil {
		return ip.String()
	}
	if v, err := ValidateHostPattern(raw); err == nil && !strings.HasPrefix(v, "*") {
		return v
	}
	if hostPatternRe.MatchString(strings.ToLower(raw)) || labelRe.MatchString(strings.ToLower(raw)) {
		return strings.ToLower(raw) // well-formed, e.g. an internal name
	}
	return "(invalid name)"
}

func (e *Egress) deny(w http.ResponseWriter, host, reason string) {
	if safe := safeHost(host); safe != host {
		if host != "" {
			reason = strings.ReplaceAll(reason, host, safe)
		}
		host = safe
	}
	e.record(host, reason)
	http.Error(w, "seed: "+reason, http.StatusForbidden)
}

func (e *Egress) record(host, reason string) {
	e.mu.Lock()
	e.denied = append(e.denied, Denial{Host: host, Reason: reason, At: time.Now()})
	if len(e.denied) > maxDenials {
		e.denied = e.denied[len(e.denied)-maxDenials:]
	}
	e.mu.Unlock()
	slog.Warn("outbound connection refused", "host", host, "reason", reason)
}

// readClientHello reads the first TLS record (the ClientHello) and returns
// its server name and the raw bytes to forward.
func readClientHello(r io.Reader) (string, []byte, error) {
	hdr := make([]byte, 5)
	if _, err := io.ReadFull(r, hdr); err != nil {
		return "", nil, err
	}
	if hdr[0] != 0x16 { // handshake
		return "", nil, errors.New("not TLS")
	}
	n := int(hdr[3])<<8 | int(hdr[4])
	if n == 0 || n > 16384 {
		return "", nil, errors.New("bad TLS record")
	}
	raw := make([]byte, 5+n)
	copy(raw, hdr)
	if _, err := io.ReadFull(r, raw[5:]); err != nil {
		return "", nil, err
	}
	var sni string
	errStop := errors.New("stop")
	_ = tls.Server(&helloConn{r: bytes.NewReader(raw)}, &tls.Config{
		GetConfigForClient: func(h *tls.ClientHelloInfo) (*tls.Config, error) {
			sni = h.ServerName
			return nil, errStop
		},
	}).Handshake()
	if sni == "" {
		return "", nil, errors.New("no server name")
	}
	return sni, raw, nil
}

// helloConn feeds recorded bytes to crypto/tls's ClientHello parser.
type helloConn struct{ r io.Reader }

func (c *helloConn) Read(b []byte) (int, error)       { return c.r.Read(b) }
func (c *helloConn) Write(b []byte) (int, error)      { return len(b), nil }
func (c *helloConn) Close() error                     { return nil }
func (c *helloConn) LocalAddr() net.Addr              { return &net.TCPAddr{} }
func (c *helloConn) RemoteAddr() net.Addr             { return &net.TCPAddr{} }
func (c *helloConn) SetDeadline(time.Time) error      { return nil }
func (c *helloConn) SetReadDeadline(time.Time) error  { return nil }
func (c *helloConn) SetWriteDeadline(time.Time) error { return nil }

// publicIP reports whether ip is on the public internet.
func publicIP(ip net.IP) bool {
	if v4 := ip.To4(); v4 != nil {
		ip = v4
	}
	if !ip.IsGlobalUnicast() || ip.IsPrivate() || ip.IsLoopback() || ip.IsLinkLocalUnicast() || ip.IsUnspecified() {
		return false
	}
	for _, n := range blockedNets {
		if n.Contains(ip) {
			return false
		}
	}
	return true
}

var blockedNets = func() []*net.IPNet {
	var out []*net.IPNet
	for _, c := range []string{
		"0.0.0.0/8", "100.64.0.0/10", "192.0.0.0/24", "192.0.2.0/24", "198.18.0.0/15", "198.51.100.0/24",
		"203.0.113.0/24", "240.0.0.0/4", "64:ff9b::/96", "64:ff9b:1::/48", "2002::/16", "2001::/32", "2001:db8::/32",
	} {
		_, n, _ := net.ParseCIDR(c)
		out = append(out, n)
	}
	return out
}()

// ServeHTTP handles the organism's CONNECT requests.
func (e *Egress) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodConnect {
		host := r.URL.Hostname()
		if host == "" {
			host, _, _ = strings.Cut(r.Host, ":")
		}
		e.deny(w, host, "only HTTPS is allowed out (plain http:// requests are refused)")
		return
	}
	host, port, err := net.SplitHostPort(r.Host)
	host = strings.TrimSuffix(strings.ToLower(host), ".")
	if err != nil || port != "443" {
		e.deny(w, r.Host, "only HTTPS on port 443 is allowed out")
		return
	}
	if net.ParseIP(host) != nil {
		e.deny(w, host, "connections to IP addresses are refused; use a host name my owner allowed")
		return
	}
	if !e.allowed(host) {
		e.deny(w, host, host+" is not allowed; my owner has to approve it (request_outbound_access)")
		return
	}
	select {
	case e.slots <- struct{}{}:
		defer func() { <-e.slots }()
	default:
		http.Error(w, "seed: too many outbound connections", http.StatusServiceUnavailable)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
	defer cancel()
	addrs, err := e.resolve(ctx, host)
	if err != nil {
		http.Error(w, "seed: cannot resolve "+host, http.StatusBadGateway)
		return
	}
	var ip net.IP
	for _, a := range addrs {
		if !publicIP(a.IP) {
			e.deny(w, host, host+" resolves to a private address ("+a.IP.String()+")")
			return
		}
		if ip == nil {
			ip = a.IP
		}
	}
	if ip == nil {
		http.Error(w, "seed: no address for "+host, http.StatusBadGateway)
		return
	}
	upstream, err := e.dial(ctx, net.JoinHostPort(ip.String(), "443"))
	if err != nil {
		http.Error(w, "seed: cannot reach "+host, http.StatusBadGateway)
		return
	}
	defer upstream.Close()
	hj, ok := w.(http.Hijacker)
	if !ok {
		http.Error(w, "seed: proxy cannot tunnel", http.StatusInternalServerError)
		return
	}
	client, buf, err := hj.Hijack()
	if err != nil {
		return
	}
	defer client.Close()
	if _, err := client.Write([]byte("HTTP/1.1 200 Connection established\r\n\r\n")); err != nil {
		return
	}
	// The TLS server name must be the host my owner allowed: otherwise a
	// tunnel to an allowed host on a shared CDN could reach any other site
	// there (domain fronting by SNI).
	_ = client.SetReadDeadline(time.Now().Add(10 * time.Second))
	sni, hello, err := readClientHello(buf)
	if err != nil || strings.TrimSuffix(strings.ToLower(sni), ".") != host {
		got := sni
		if err != nil {
			got = "no TLS server name"
		}
		e.record(host, "TLS server name ("+safeHost(got)+") does not match the allowed host")
		return
	}
	if _, err := upstream.Write(hello); err != nil {
		return
	}
	done := make(chan struct{}, 2)
	pipe := func(dst net.Conn, src io.Reader, from net.Conn) {
		b := make([]byte, 32<<10)
		for {
			_ = from.SetReadDeadline(time.Now().Add(egressIdle))
			n, err := src.Read(b)
			if n > 0 {
				if _, werr := dst.Write(b[:n]); werr != nil {
					break
				}
			}
			if err != nil {
				break
			}
		}
		done <- struct{}{}
	}
	go pipe(upstream, buf, client)
	go pipe(client, upstream, upstream)
	<-done
}

// Serve runs the proxy on a Unix socket the organism's sandbox can reach.
func (e *Egress) Serve(l net.Listener) error {
	srv := &http.Server{Handler: e, ReadHeaderTimeout: 10 * time.Second}
	err := srv.Serve(l)
	if errors.Is(err, http.ErrServerClosed) {
		return nil
	}
	return err
}

// Describe tells me (the agent) what my organism may reach.
func (e *Egress) Describe(passed []string) string {
	hosts, secrets := e.Grants()
	var sb strings.Builder
	sb.WriteString("\n## My organism's outbound access\n")
	sb.WriteString("The live organism has no network except HTTPS (port 443) to hosts my owner approved, through HTTPS_PROXY (set automatically; Go and Node honor it). ")
	sb.WriteString("To reach a new host or read a secret, call request_outbound_access (my owner approves it). Tests never get secrets: code must start without them.\n")
	if len(hosts) == 0 {
		sb.WriteString("- allowed hosts: none\n")
	} else {
		var hs []string
		for _, h := range hosts {
			hs = append(hs, h.Value)
		}
		sb.WriteString("- allowed hosts: " + strings.Join(hs, ", ") + "\n")
	}
	set := map[string]bool{}
	for _, p := range passed {
		set[p] = true
	}
	if len(secrets) == 0 {
		sb.WriteString("- secrets the organism may read: none\n")
	} else {
		var ss []string
		for _, s := range secrets {
			if set[s.Value] {
				ss = append(ss, s.Value)
			} else {
				ss = append(ss, s.Value+" (approved, but not passed at this start)")
			}
		}
		sb.WriteString("- secrets the organism may read (environment variables): " + strings.Join(ss, ", ") + "\n")
	}
	var avail []string
	for _, p := range passed {
		if _, err := ValidateSecretName(p); err == nil {
			avail = append(avail, p)
		}
	}
	sort.Strings(avail)
	if len(avail) > 0 {
		sb.WriteString("- secrets my owner passed at this start (names only): " + strings.Join(avail, ", ") + "\n")
	}
	if d := e.Denials(); len(d) > 0 {
		sb.WriteString(fmt.Sprintf("- recently refused: %s (%s)\n", d[0].Host, d[0].Reason))
	}
	return sb.String()
}
