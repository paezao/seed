package runtime

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestPreviewTickets(t *testing.T) {
	var p previews
	tk := p.issue("evo_1", "session-a")
	if got, ok := p.lookup(tk); !ok || got.evolution != "evo_1" || got.session != "session-a" {
		t.Fatalf("a ticket opens its evolution for its session: %+v %v", got, ok)
	}
	if _, ok := p.lookup("guess"); ok {
		t.Fatal("unknown tickets open nothing")
	}
}

func TestPreviewCookieNeverReachesTheOrganism(t *testing.T) {
	r := httptest.NewRequest("GET", "/", nil)
	r.AddCookie(&http.Cookie{Name: "seed_preview_x", Value: "ticket"})
	r.AddCookie(&http.Cookie{Name: "app_session", Value: "keep"})
	stripCookie(r, "seed_preview_x")
	if _, err := r.Cookie("seed_preview_x"); err == nil {
		t.Fatal("the preview cookie is stripped")
	}
	if c, err := r.Cookie("app_session"); err != nil || c.Value != "keep" {
		t.Fatal("the app's own cookies stay")
	}
}

func TestPreviewEnterNeedsMyOwnerAndANavigation(t *testing.T) {
	k, _ := ownerKernel(t)
	r := httptest.NewRequest("GET", "/_seed/preview/evo_1", nil)
	r.SetPathValue("id", "evo_1")
	w := httptest.NewRecorder()
	k.handlePreviewEnter(w, r)
	if w.Code != http.StatusForbidden {
		t.Fatalf("fetch() (organism scripts) can't start a preview: %d", w.Code)
	}
	r.Header.Set("Sec-Fetch-Dest", "document")
	r.Header.Set("Sec-Fetch-Mode", "navigate")
	w = httptest.NewRecorder()
	k.handlePreviewEnter(w, r)
	if w.Code != http.StatusUnauthorized || len(w.Result().Cookies()) != 0 {
		t.Fatalf("a stranger gets no preview cookie: %d", w.Code)
	}
	w = httptest.NewRecorder()
	k.handlePreviewInfo(w, httptest.NewRequest("GET", "/_seed/preview/info", nil))
	if w.Code != http.StatusNoContent {
		t.Fatal("no preview, no info")
	}
}
