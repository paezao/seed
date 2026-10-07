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
	k := &Kernel{}
	h := k.controlUI()
	for _, p := range []string{"/_seed/", "/_seed/evolutions/evo_123"} {
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, httptest.NewRequest("GET", p, nil))
		if rec.Code != 200 || !strings.Contains(rec.Body.String(), "<div id=\"root\">") {
			t.Fatalf("%s: %d", p, rec.Code)
		}
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
	check("/_seed/api/events?token=secret", "", 204)
	check("/_seed/api/identity/logo", "", 204)
	check("/api/tasks", "", 204) // organism routes
}

func TestControlPageOnlyForNavigations(t *testing.T) {
	k := &Kernel{Token: "secret"}
	h := k.controlUI()
	r := httptest.NewRequest("GET", "/_seed/", nil)
	r.Header.Set("Sec-Fetch-Dest", "empty") // fetch() from an organism script
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, r)
	if rec.Code != 403 || strings.Contains(rec.Body.String(), "secret") {
		t.Fatalf("token page served to a script: %d", rec.Code)
	}
	r = httptest.NewRequest("GET", "/_seed/evolutions", nil)
	r.Header.Set("Sec-Fetch-Dest", "document")
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, r)
	if rec.Code != 200 || !strings.Contains(rec.Body.String(), `name="seed-token" content="secret"`) || rec.Header().Get("Cross-Origin-Opener-Policy") != "same-origin" {
		t.Fatalf("navigation should get the page with token and COOP: %d %v", rec.Code, rec.Header())
	}
}

func TestStoredKeyNeverSentToCallerURL(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	var hits []string
	evil := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits = append(hits, r.Header.Get("Authorization"))
		w.WriteHeader(500)
	}))
	defer evil.Close()
	if err := updateCredentials(func(c *credentials) { c.Providers["openrouter"] = credential{APIKey: "sk-or-secret"} }); err != nil {
		t.Fatal(err)
	}
	k := &Kernel{Cfg: &config.Config{}, Mind: models.NewSwitchable()}
	r := httptest.NewRequest("POST", "/_seed/api/model", strings.NewReader(`{"provider":"openrouter","name":"x/y","base_url":"`+evil.URL+`"}`))
	rec := httptest.NewRecorder()
	k.handleSetModel(rec, r)
	for _, h := range hits {
		if strings.Contains(h, "sk-or-secret") {
			t.Fatal("stored key was sent to a caller-supplied URL")
		}
	}
	if len(hits) != 0 {
		t.Fatalf("openrouter must ignore base_url; evil server was contacted %d times", len(hits))
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
