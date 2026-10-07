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
