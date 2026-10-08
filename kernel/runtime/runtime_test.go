package runtime

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"seed/kernel/config"
	"seed/kernel/memory"
	"seed/kernel/models"
)

func TestToModelMessagesAlternates(t *testing.T) {
	msgs := toModelMessages([]memory.Message{
		{Role: "seed", Content: "evolution done"}, // leading assistant turns are dropped
		{Role: "user", Content: "hi"},
		{Role: "user", Content: "are you there?"},
		{Role: "seed", Content: "yes"},
		{Role: "seed", Content: "I am now generation 2"},
		{Role: "user", Content: "great"},
	})
	if len(msgs) != 3 || msgs[0].Role != models.User || msgs[1].Role != models.Assistant || msgs[2].Role != models.User {
		t.Fatalf("not alternating: %+v", msgs)
	}
	if !strings.Contains(msgs[0].Content, "are you there?") || !strings.Contains(msgs[1].Content, "generation 2") {
		t.Fatalf("consecutive turns should merge: %+v", msgs)
	}
}

func TestLogBuffer(t *testing.T) {
	b := NewLogBuffer(3, nil)
	b.Write([]byte("a\nb\n"))
	b.Write([]byte("c\nd"))
	b.Write([]byte("e\n"))
	got := b.Tail(10)
	if strings.Join(got, ",") != "b,c,de" {
		t.Fatalf("got %v", got)
	}
}

func TestControlUIFallsBackToIndex(t *testing.T) {
	k, _ := ownerKernel(t)
	h := k.controlUI()
	secret, err := k.Owner.Redeem(t.Context(), k.Owner.NewLoginCode(), "")
	if err != nil {
		t.Fatal(err)
	}
	for _, p := range []string{"/_seed/", "/_seed/evolutions/evo_123"} {
		rec := httptest.NewRecorder()
		req := httptest.NewRequest("GET", p, nil)
		req.AddCookie(&http.Cookie{Name: k.cookieName, Value: secret})
		req.Header.Set("Sec-Fetch-Dest", "document")
		req.Header.Set("Sec-Fetch-Mode", "navigate")
		h.ServeHTTP(rec, req)
		if rec.Code != 200 || !strings.Contains(rec.Body.String(), "<div id=\"root\">") || !strings.Contains(rec.Body.String(), apiToken(secret)) {
			t.Fatalf("%s: %d", p, rec.Code)
		}
		if strings.Contains(rec.Body.String(), k.Token) {
			t.Fatal("the CLI token must never reach a browser")
		}
	}
	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/_seed/", nil)
	req.Header.Set("Sec-Fetch-Dest", "document")
	req.Header.Set("Sec-Fetch-Mode", "navigate")
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusUnauthorized || strings.Contains(rec.Body.String(), "seed-token") {
		t.Fatal("signed out: the private page, no token")
	}
}

func TestExtensionsAreValidated(t *testing.T) {
	root := t.TempDir()
	os.MkdirAll(filepath.Join(root, "organism", "control"), 0o755)
	os.WriteFile(filepath.Join(root, "organism", "control", "extensions.json"), []byte(`[
		{"id":"signups","title":"Signups","path":"/admin/signups"},
		{"id":"evil","title":"Evil","path":"/_seed/api/settings"},
		{"id":"ext","title":"External","path":"https://example.com"}
	]`), 0o644)
	k := &Kernel{Cfg: &config.Config{Root: root}}
	rec := httptest.NewRecorder()
	k.handleExtensions(rec, httptest.NewRequest("GET", "/_seed/api/extensions", nil))
	var got []map[string]string
	json.Unmarshal(rec.Body.Bytes(), &got)
	if len(got) != 1 || got[0]["id"] != "signups" {
		t.Fatalf("only organism-local paths are allowed: %v", got)
	}
}

func TestUnavailablePage(t *testing.T) {
	o := &Organism{Cfg: &config.Config{Name: "tasks"}}
	rec := httptest.NewRecorder()
	o.ServeHTTP(rec, httptest.NewRequest("GET", "/", nil))
	if rec.Code != http.StatusServiceUnavailable || !strings.Contains(rec.Body.String(), "/_seed/") {
		t.Fatalf("expected 503 pointing to the control plane: %d %s", rec.Code, rec.Body)
	}
}

func TestGuardAPI(t *testing.T) {
	ok := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(204) })
	h := guardAPI(ok)
	cases := []struct {
		method, path, ctype, site string
		want                      int
	}{
		{"POST", "/_seed/api/approvals/x", "application/json", "same-origin", 204},
		{"POST", "/_seed/api/approvals/x", "text/plain", "", 415},
		{"POST", "/_seed/api/approvals/x", "application/json", "cross-site", 403},
		{"GET", "/_seed/api/status", "", "cross-site", 204},
		{"POST", "/api/tasks", "text/plain", "", 204}, // organism routes are not the kernel's business
	}
	for _, c := range cases {
		r := httptest.NewRequest(c.method, c.path, strings.NewReader("{}"))
		if c.ctype != "" {
			r.Header.Set("Content-Type", c.ctype)
		}
		if c.site != "" {
			r.Header.Set("Sec-Fetch-Site", c.site)
		}
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, r)
		if rec.Code != c.want {
			t.Errorf("%s %s (%s, %s) = %d, want %d", c.method, c.path, c.ctype, c.site, rec.Code, c.want)
		}
	}
}

func TestGuardHostAndOrigin(t *testing.T) {
	ok := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(204) })
	h := guardHost([]string{"seed.lan"}, guardAPI(ok))
	cases := []struct {
		host, origin string
		want         int
	}{
		{"127.0.0.1:8080", "", 204},
		{"localhost:8080", "http://localhost:8080", 204},
		{"tasks.localhost:8080", "", 204},
		{"[::1]:8080", "", 204},
		{"seed.lan", "", 204},
		{"evil.example:8080", "", http.StatusMisdirectedRequest}, // DNS rebinding
		{"127.0.0.1:8080", "http://evil.example", 403},
	}
	for _, c := range cases {
		r := httptest.NewRequest("POST", "/_seed/api/messages", strings.NewReader("{}"))
		r.Host = c.host
		r.Header.Set("Content-Type", "application/json")
		if c.origin != "" {
			r.Header.Set("Origin", c.origin)
		}
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, r)
		if rec.Code != c.want {
			t.Errorf("host %s origin %s = %d, want %d", c.host, c.origin, rec.Code, c.want)
		}
	}
}

func TestOrganismCannotRegisterServiceWorkers(t *testing.T) {
	o := &Organism{Cfg: &config.Config{Name: "tasks"}}
	r := httptest.NewRequest("GET", "/sw.js", nil)
	r.Header.Set("Service-Worker", "script")
	rec := httptest.NewRecorder()
	o.ServeHTTP(rec, r)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("got %d", rec.Code)
	}
}

func TestControlToken(t *testing.T) {
	k := &Kernel{Token: "secret"}
	ok := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(204) })
	h := k.requireToken(ok)
	check := func(path, header string, want int) {
		t.Helper()
		r := httptest.NewRequest("GET", path, nil)
		if header != "" {
			r.Header.Set("X-Seed-Token", header)
		}
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, r)
		if rec.Code != want {
			t.Errorf("%s (token %q) = %d, want %d", path, header, rec.Code, want)
		}
	}
	check("/_seed/api/status", "", 401)
	check("/_seed/api/status", "wrong", 401)
	check("/_seed/api/status", "secret", 204)
	check("/_seed/api/events?token=secret", "", 401) // never in a URL
	check("/_seed/api/identity/logo", "", 204)
	check("/api/tasks", "", 204) // organism routes
}

func TestControlPageOnlyForNavigations(t *testing.T) {
	k, _ := ownerKernel(t)
	secret, err := k.Owner.Redeem(t.Context(), k.Owner.NewLoginCode(), "")
	if err != nil {
		t.Fatal(err)
	}
	token := apiToken(secret)
	h := k.controlUI()
	r := httptest.NewRequest("GET", "/_seed/", nil)
	r.AddCookie(&http.Cookie{Name: k.cookieName, Value: secret}) // same-origin fetch() carries the cookie
	r.Header.Set("Sec-Fetch-Dest", "empty")                      // fetch() from an organism script
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, r)
	if rec.Code != 403 || strings.Contains(rec.Body.String(), token) {
		t.Fatalf("token page served to a script: %d", rec.Code)
	}
	r = httptest.NewRequest("GET", "/_seed/evolutions", nil)
	r.AddCookie(&http.Cookie{Name: k.cookieName, Value: secret})
	r.Header.Set("Sec-Fetch-Dest", "document")
	r.Header.Set("Sec-Fetch-Mode", "navigate")
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, r)
	if rec.Code != 200 || !strings.Contains(rec.Body.String(), `name="seed-token" content="`+token+`"`) || rec.Header().Get("Cross-Origin-Opener-Policy") != "same-origin" {
		t.Fatalf("navigation should get the page with token and COOP: %d %v", rec.Code, rec.Header())
	}
}

func TestSecretsLeaveTheEnvironment(t *testing.T) {
	t.Setenv("OPENROUTER_API_KEY", "sk-or-test")
	t.Setenv("STRIPE_KEY", "sk_live_x")
	t.Setenv("SEED_SECRETS", "STRIPE_KEY")
	t.Setenv("PATH_LIKE", "not a secret")
	s := LoadSecrets()
	if s.Get("OPENROUTER_API_KEY") != "sk-or-test" || s.Get("STRIPE_KEY") != "sk_live_x" {
		t.Fatal("secrets should be held in memory")
	}
	for _, n := range []string{"OPENROUTER_API_KEY", "STRIPE_KEY", "SEED_SECRETS"} {
		if _, ok := os.LookupEnv(n); ok {
			t.Fatalf("%s must be removed from the environment so nothing I start inherits it", n)
		}
	}
	if os.Getenv("PATH_LIKE") == "" {
		t.Fatal("non-secrets stay")
	}
}

func TestModelConfigReportsKeySource(t *testing.T) {
	t.Setenv("OPENROUTER_API_KEY", "sk-or-test")
	k := &Kernel{Cfg: &config.Config{}, Mind: models.NewSwitchable(), Secrets: LoadSecrets()}
	mc := k.modelConfig()
	if len(mc.Providers) != 1 || !mc.Providers[0].HasKey || mc.Providers[0].KeySource != "env" || mc.Providers[0].KeyEnv != "OPENROUTER_API_KEY" {
		t.Fatalf("unexpected providers: %+v", mc.Providers)
	}
	b, _ := json.Marshal(mc)
	if strings.Contains(string(b), "sk-or-test") {
		t.Fatal("the key must never be returned")
	}
	// Keys passed at start cannot be forgotten through the API.
	rec := httptest.NewRecorder()
	k.handleForgetKey(rec, httptest.NewRequest("POST", "/_seed/api/model/forget-key", strings.NewReader(`{"provider":"openrouter"}`)))
	if rec.Code != 400 {
		t.Fatalf("forgetting an env key should be refused, got %d", rec.Code)
	}
}

func TestControlPlaneNotServedOnOrganismOrigin(t *testing.T) {
	ok := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(204) })
	h := organismOrigin(ok)
	for path, want := range map[string]int{"/_seed/": 404, "/_seed/api/status": 404, "/_seed": 404, "/admin/stats": 204} {
		r := httptest.NewRequest("GET", path, nil)
		r.Host = "organism.localhost:8081"
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, r)
		if rec.Code != want {
			t.Errorf("%s on organism origin = %d, want %d", path, rec.Code, want)
		}
	}
}

func TestOrganismFramingPolicy(t *testing.T) {
	h := http.Header{}
	restrictFraming(h, "localhost:8081")
	if h.Get("X-Frame-Options") != "DENY" || !strings.Contains(strings.Join(h.Values("Content-Security-Policy"), ";"), "frame-ancestors 'none'") {
		t.Fatalf("main-origin organism pages must not be frameable: %v", h)
	}
	h = http.Header{"Content-Security-Policy": {"default-src 'self'"}}
	restrictFraming(h, "organism.localhost:8081")
	csp := h.Values("Content-Security-Policy")
	if len(csp) != 2 || !strings.Contains(csp[1], "frame-ancestors http://localhost:8081") || h.Get("X-Frame-Options") != "" {
		t.Fatalf("organism origin should be frameable only by the control plane, keeping its own CSP: %v", h)
	}
}

func TestKernelReportsAreRecordsNotAgentWords(t *testing.T) {
	msgs := toModelMessages([]memory.Message{
		{Role: "user", Kind: "chat", Content: "become a todo app"},
		{Role: "seed", Kind: "chat", Content: "On it."},
		{Role: "seed", Kind: "report", Content: "I am now **generation 2**"},
		{Role: "user", Kind: "chat", Content: "add priorities"},
	})
	for _, m := range msgs {
		if m.Role == models.Assistant && strings.Contains(m.Content, "generation 2") {
			t.Fatal("kernel report must not appear as the agent's own words")
		}
	}
	last := msgs[len(msgs)-1]
	if last.Role != models.User || !strings.Contains(last.Content, "[kernel record] I am now") || !strings.Contains(last.Content, "add priorities") {
		t.Fatalf("report should be a user-side record merged before the next request: %+v", msgs)
	}
}

func TestEvolutionKeepsOwnersWords(t *testing.T) {
	owner := "Tasks should have priorities: low, medium and high. Let me filter by priority."
	got := withOwnerWords("Become a todo application with task priorities.", owner)
	if !strings.Contains(got, owner) {
		t.Fatalf("owner's words lost: %q", got)
	}
	if withOwnerWords(owner, owner) != owner {
		t.Fatal("no duplication when the intent already quotes the owner")
	}
}

func TestNextRoadmapStage(t *testing.T) {
	root := t.TempDir()
	if nextRoadmapStage(root) != "" {
		t.Fatal("no roadmap, no next stage")
	}
	os.MkdirAll(filepath.Join(root, "knowledge"), 0o755)
	os.WriteFile(filepath.Join(root, "knowledge", "roadmap.md"), []byte("# Roadmap\n\n- [x] **Stage 1: Catalog** (generation 2): browse\n- [ ] **Stage 2: Cart** (next): add to cart\n- [ ] **Stage 3: Checkout**\n"), 0o644)
	if got := nextRoadmapStage(root); got != "Stage 2: Cart" {
		t.Fatalf("got %q", got)
	}
}

func TestRejectInternal(t *testing.T) {
	ok := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(204) })
	h := rejectInternal(ok)
	for addr, want := range map[string]int{"127.0.0.1:5555": 403, "[::1]:5555": 403, "203.0.113.9:5555": 204} {
		r := httptest.NewRequest("GET", "/", nil)
		r.RemoteAddr = addr
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, r)
		if rec.Code != want {
			t.Errorf("%s: %d, want %d", addr, rec.Code, want)
		}
	}
}
