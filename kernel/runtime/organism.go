package runtime

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"net/http/httputil"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"seed/kernel/config"
	"seed/kernel/events"
	"seed/kernel/git"
	"seed/kernel/infra"
	"seed/kernel/knowledge"
	"seed/kernel/migrate"
	"seed/kernel/sandbox"
	"seed/kernel/tools"
)

// Organism supervises the live generation: the organism built from the main
// checkout, running in its own sandbox against the live database.
type Organism struct {
	Cfg    *config.Config
	Driver sandbox.Driver
	Admin  infra.Admin
	Role   string
	Pass   string
	Repo   *git.Repo
	Bus    *events.Bus
	// ReaderRole can only read the live database (used to answer questions).
	ReaderRole, ReaderPass string
	// Egress is the organism's only way out (see egress.go); EgressSocket
	// is where it listens. Secrets supplies the values of granted secrets.
	Egress       *Egress
	EgressSocket string
	Secrets      *Secrets
	// JobToken is given to the live process (SEED_JOB_TOKEN) and sent with
	// every scheduled job call (X-Seed-Job), so the organism can tell my
	// calls from anyone else's. AfterDeploy runs once a generation is live.
	JobToken    string
	AfterDeploy func()
	// Badge reports whether my owner wants the badge on my pages (badge.go).
	Badge func() bool
	// Observe sees every response's status (my doctor counts server errors).
	Observe func(method, path string, status int)
	// PreviewRoute serves a request from a candidate generation when this
	// browser is previewing one (preview.go), reporting whether it did.
	PreviewRoute func(w http.ResponseWriter, r *http.Request) bool

	mu     sync.Mutex
	sb     sandbox.Sandbox
	state  string
	errMsg string
	proxy  *httputil.ReverseProxy
	target string
	deploy sync.Mutex
}

const liveProcess = "organism"

func (o *Organism) dbName() string { return o.Cfg.DBName("app") }

// DatabaseURL is the host-side URL of the live organism database.
func (o *Organism) DatabaseURL() string {
	return o.Admin.DatabaseURL(o.dbName(), o.Role, o.Pass, "")
}

// ReaderURL connects to the live database as the read-only role.
func (o *Organism) ReaderURL() string {
	return o.Admin.DatabaseURL(o.dbName(), o.ReaderRole, o.ReaderPass, "")
}

func (o *Organism) sandboxDBURL() string {
	host := ""
	if o.Cfg.Sandbox.Driver != "local" {
		host = o.Cfg.Sandbox.DBHost
	}
	return o.Admin.DatabaseURL(o.dbName(), o.Role, o.Pass, host)
}

// State returns the organism's lifecycle state and last error.
func (o *Organism) State() (string, string) {
	o.mu.Lock()
	defer o.mu.Unlock()
	if o.state == "" {
		return "stopped", ""
	}
	return o.state, o.errMsg
}

func (o *Organism) setState(state, msg string) {
	o.mu.Lock()
	o.state, o.errMsg = state, msg
	o.mu.Unlock()
	o.Bus.Publish("organism", map[string]string{"state": state, "error": msg})
}

// sandbox returns the live sandbox, creating it if needed. Everything except
// organism/ is read-only to the live organism; .seed is hidden.
func (o *Organism) sandbox(ctx context.Context) (sandbox.Sandbox, error) {
	o.mu.Lock()
	sb := o.sb
	o.mu.Unlock()
	if sb != nil && sb.Alive(ctx) {
		return sb, nil
	}
	sb, err := o.Driver.Create(ctx, sandbox.Spec{
		Name: o.Cfg.ContainerName("live"), Root: o.Cfg.Root,
		// The live organism may write only its own directory (build outputs).
		Writable: []string{"organism"}, Hidden: []string{".seed"}, Port: o.Cfg.Organism.Port,
		// The live organism is unreachable from anything else in my body
		// (in particular from evolution sandboxes running experimental code).
		PrivateNetwork: o.Cfg.Sandbox.Driver == "bwrap",
		// Only the database here: Exec commands (builds, installs, migrations)
		// run outside the private network, so they must never hold secrets.
		Env:    map[string]string{"DATABASE_URL": o.sandboxDBURL(), "SEED_ENV": "live"},
		Egress: o.egressSocket(),
		Labels: map[string]string{"seed.name": o.Cfg.Name, "seed.role": "live"},
	})
	if err != nil {
		return nil, err
	}
	o.mu.Lock()
	o.sb = sb
	o.proxy, o.target = nil, ""
	o.mu.Unlock()
	return sb, nil
}

func (o *Organism) builtMarker() string { return filepath.Join(o.Cfg.Root, ".seed", "organism-built") }

// EnsureRunning starts the current generation, rebuilding only if the
// checkout changed since the last build.
func (o *Organism) EnsureRunning(ctx context.Context) error {
	head, _ := o.Repo.Head(ctx)
	built, _ := os.ReadFile(o.builtMarker())
	if head != "" && strings.TrimSpace(string(built)) == head {
		if err := o.restart(ctx); err == nil {
			return nil
		}
	}
	return o.Deploy(ctx)
}

// Deploy builds the organism from the main checkout, applies migrations to
// the live database and restarts it.
func (o *Organism) Deploy(ctx context.Context) error {
	o.deploy.Lock()
	defer o.deploy.Unlock()
	o.setState("building", "")
	sb, err := o.sandbox(ctx)
	if err != nil {
		o.setState("failed", err.Error())
		return err
	}
	timeout := time.Duration(o.Cfg.Evolution.CommandTimeoutSeconds) * time.Second
	res, err := sb.Exec(ctx, o.Cfg.Organism.Build, timeout, nil)
	if err != nil || !res.OK() {
		msg := "build failed"
		if err != nil {
			msg = err.Error()
		} else {
			msg = "build failed:\n" + tools.Truncate(res.Output, 4000)
		}
		o.setState("failed", msg)
		return errors.New(msg)
	}
	mres, err := migrate.ApplyRepo(ctx, o.DatabaseURL(), tools.OrganismMigrationsTable, o.Cfg.Root, o.Cfg.Organism.Migrations)
	if err != nil {
		o.setState("failed", "migrations: "+err.Error())
		return fmt.Errorf("migrating live database: %w", err)
	}
	slog.Info("live database migrated", "result", mres.String())
	if err := o.restart(ctx); err != nil {
		return err
	}
	if head, err := o.Repo.Head(ctx); err == nil {
		_ = os.MkdirAll(filepath.Dir(o.builtMarker()), 0o755)
		_ = os.WriteFile(o.builtMarker(), []byte(head), 0o644)
	}
	if o.AfterDeploy != nil {
		o.AfterDeploy()
	}
	return nil
}

func (o *Organism) restart(ctx context.Context) error {
	sb, err := o.sandbox(ctx)
	if err != nil {
		o.setState("failed", err.Error())
		return err
	}
	o.setState("starting", "")
	// The live process alone gets its way out and its granted secrets: it
	// runs in the private network, where the proxy is the only exit.
	env := &tools.Env{Sandbox: sb, RunCommand: o.Cfg.Organism.Run, HealthPath: o.Cfg.Organism.Health, SandboxEnv: o.processEnv()}
	if _, err := tools.StartApp(ctx, env, 60*time.Second); err != nil {
		o.setState("failed", err.Error())
		return err
	}
	o.setState("running", "")
	return nil
}

// Stop stops the live organism and its sandbox.
func (o *Organism) Stop(ctx context.Context) {
	o.mu.Lock()
	sb := o.sb
	o.sb = nil
	o.mu.Unlock()
	if sb != nil {
		_ = sb.Close(ctx)
	}
	o.setState("stopped", "")
}

// StopForRestore stops my app's process while its data is replaced; it
// stays down ("restoring") until the next deploy starts it.
func (o *Organism) StopForRestore(ctx context.Context) {
	o.deploy.Lock()
	defer o.deploy.Unlock()
	o.setState("restoring", "")
	o.mu.Lock()
	sb := o.sb
	o.mu.Unlock()
	if sb != nil {
		_ = sb.Stop(ctx, liveProcess)
	}
}

// Logs returns the live organism's recent output.
func (o *Organism) Logs(ctx context.Context, tail int) string {
	o.mu.Lock()
	sb := o.sb
	o.mu.Unlock()
	if sb == nil {
		return ""
	}
	return sb.Logs(ctx, liveProcess, tail)
}

// ServeHTTP proxies requests to the live organism.
func (o *Organism) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	// A service worker registered by the organism at "/" would also control
	// /_seed (same origin) and could intercept the control plane.
	if r.Header.Get("Service-Worker") == "script" {
		http.Error(w, "service workers are not allowed (they would control /_seed)", http.StatusForbidden)
		return
	}
	// Pages I add the owner's badge to come back uncompressed (see badge.go).
	if wantsBadge(r) {
		r.Header.Del("Accept-Encoding")
	}
	// Only my scheduler sends X-Seed-Job (see CallJob), never a visitor.
	r.Header.Del("X-Seed-Job")
	r.Header.Del("X-Seed-Routine")
	if o.PreviewRoute != nil && o.PreviewRoute(w, r) {
		return
	}
	state, msg := o.State()
	o.mu.Lock()
	sb, proxy := o.sb, o.proxy
	o.mu.Unlock()
	if state != "running" || sb == nil {
		o.unavailable(w, state, msg)
		return
	}
	if proxy == nil {
		target, err := sb.URL(r.Context())
		if err != nil {
			o.unavailable(w, "failed", err.Error())
			return
		}
		u, _ := url.Parse(target)
		proxy = httputil.NewSingleHostReverseProxy(u)
		proxy.Transport = sb.Transport()
		proxy.ModifyResponse = func(resp *http.Response) error {
			// Keep organism pages out of the control plane's browsing context
			// group (see controlUI) whatever the organism asks for.
			resp.Header.Set("Cross-Origin-Opener-Policy", "unsafe-none")
			resp.Header.Del("Service-Worker-Allowed")
			restrictFraming(resp.Header, resp.Request.Host)
			if o.Observe != nil {
				o.Observe(resp.Request.Method, resp.Request.URL.Path, resp.StatusCode)
			}
			if o.Badge != nil && !o.Badge() {
				return nil
			}
			return injectBadge(resp)
		}
		proxy.ErrorHandler = func(w http.ResponseWriter, r *http.Request, err error) {
			if o.Observe != nil {
				// What the client gets: my "unreachable" page.
				o.Observe(r.Method, r.URL.Path, http.StatusServiceUnavailable)
			}
			o.unavailable(w, "unreachable", err.Error())
		}
		o.mu.Lock()
		o.proxy, o.target = proxy, target
		o.mu.Unlock()
	}
	proxy.ServeHTTP(w, r)
}

func (o *Organism) unavailable(w http.ResponseWriter, state, msg string) {
	w.Header().Set("content-type", "text/html; charset=utf-8")
	w.Header().Set("retry-after", "2")
	w.WriteHeader(http.StatusServiceUnavailable)
	name := knowledge.Name(o.Cfg.Root)
	refresh := ""
	if state == "building" || state == "starting" || state == "restoring" {
		refresh = `<meta http-equiv="refresh" content="2">`
	}
	fmt.Fprintf(w, `<!doctype html><html><head><meta charset="utf-8">%s<title>%s</title>
<style>body{font:15px/1.5 system-ui,sans-serif;display:grid;place-items:center;min-height:90vh;color:#444;background:#fafafa}
@media(prefers-color-scheme:dark){body{background:#111;color:#bbb}}main{max-width:32rem;padding:1rem}code{font-size:13px}a{color:inherit}</style></head>
<body><main><p>I am %s.</p><p>My body is <strong>%s</strong> right now.</p>%s<p><a href="/_seed/">Talk to me at /_seed</a></p></main></body></html>`,
		refresh, htmlEscape(name), htmlEscape(name), htmlEscape(state), errorBlock(msg))
}

func errorBlock(msg string) string {
	if msg == "" {
		return ""
	}
	return "<pre><code>" + htmlEscape(tools.Truncate(msg, 2000)) + "</code></pre>"
}

func htmlEscape(s string) string {
	r := strings.NewReplacer("&", "&amp;", "<", "&lt;", ">", "&gt;", `"`, "&quot;")
	return r.Replace(s)
}

// restrictFraming decides who may frame an organism response. On the main
// origin nobody may: a framed admin screen (organism.localhost) could
// otherwise navigate itself to a main-origin page, become same-origin with
// the control plane and read its token. On the organism origin, only the
// control plane (same port on localhost/127.0.0.1/[::1]) may frame it. The
// policy is added as an extra CSP header, so the organism's own CSP still applies.
func restrictFraming(h http.Header, host string) {
	hostname, port := host, ""
	if hn, p, err := net.SplitHostPort(host); err == nil {
		hostname, port = hn, p
	}
	if strings.EqualFold(hostname, OrganismFrameHost) && port != "" {
		h.Add("Content-Security-Policy", fmt.Sprintf("frame-ancestors http://localhost:%[1]s http://127.0.0.1:%[1]s http://[::1]:%[1]s", port))
		h.Del("X-Frame-Options")
		return
	}
	h.Add("Content-Security-Policy", "frame-ancestors 'none'")
	h.Set("X-Frame-Options", "DENY")
}

func (o *Organism) egressSocket() string {
	if o.Egress == nil || o.Cfg.Sandbox.Driver != "bwrap" {
		return ""
	}
	return o.EgressSocket
}

// processEnv is the live organism process's extra environment: its way out
// and the secrets my owner granted it (only those, and only if passed at
// start). Without the bwrap driver there is no private network to keep a
// secret in, so none are given.
func (o *Organism) processEnv() map[string]string {
	env := map[string]string{}
	if o.JobToken != "" {
		env["SEED_JOB_TOKEN"] = o.JobToken
	}
	if o.egressSocket() == "" {
		return env
	}
	for k, v := range ProxyEnv() {
		env[k] = v
	}
	for _, name := range o.Egress.SecretNames() {
		if v := o.Secrets.Get(name); v != "" {
			env[name] = v
		}
	}
	return env
}

// Recreate restarts the live organism in a fresh sandbox (e.g. after its
// granted secrets changed).
func (o *Organism) Recreate(ctx context.Context) error {
	o.deploy.Lock()
	defer o.deploy.Unlock()
	o.Stop(ctx)
	return o.restart(ctx)
}

// Alive reports whether my organism's process is up. ok is false when it
// isn't supposed to be (stopped, building, starting, failed to deploy).
func (o *Organism) Alive(ctx context.Context) (alive, ok bool) {
	o.mu.Lock()
	sb, state := o.sb, o.state
	o.mu.Unlock()
	if state != "running" || sb == nil {
		return false, false
	}
	return sb.Running(ctx, liveProcess), true
}

// RestartProcess starts my organism's process again (after a crash).
func (o *Organism) RestartProcess(ctx context.Context) error {
	if !o.deploy.TryLock() {
		return nil // a deploy is restarting it anyway
	}
	defer o.deploy.Unlock()
	return o.restart(ctx)
}

// ProxyEnv points a process in a private network at its way out (the egress
// proxy, as a local port). Go and Node honor it.
func ProxyEnv() map[string]string {
	proxy := "http://127.0.0.1:" + strconv.Itoa(sandbox.EgressPort)
	env := map[string]string{}
	for _, k := range []string{"HTTPS_PROXY", "https_proxy", "HTTP_PROXY", "http_proxy"} {
		env[k] = proxy
	}
	env["NO_PROXY"], env["no_proxy"] = "localhost,127.0.0.1", "localhost,127.0.0.1"
	env["NODE_USE_ENV_PROXY"] = "1" // Node's fetch ignores HTTPS_PROXY without it
	return env
}
