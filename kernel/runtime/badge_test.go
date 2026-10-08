package runtime

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func pageResp(t *testing.T, host, dest, ctype, enc, body string) *http.Response {
	t.Helper()
	req := httptest.NewRequest("GET", "http://"+host+"/", nil)
	req.Host = host
	if dest != "" {
		req.Header.Set("Sec-Fetch-Dest", dest)
	}
	h := http.Header{"Content-Type": {ctype}}
	if enc != "" {
		h.Set("Content-Encoding", enc)
	}
	return &http.Response{StatusCode: 200, Header: h, Body: io.NopCloser(strings.NewReader(body)), Request: req, ContentLength: int64(len(body))}
}

func bodyOf(t *testing.T, r *http.Response) string {
	b, _ := io.ReadAll(r.Body)
	return string(b)
}

func TestInjectBadge(t *testing.T) {
	page := "<html><body><h1>Recipes</h1></body></html>"
	r := pageResp(t, "localhost:8081", "document", "text/html; charset=utf-8", "", page)
	if err := injectBadge(r); err != nil {
		t.Fatal(err)
	}
	got := bodyOf(t, r)
	if !strings.Contains(got, badgeScriptTag+"</body>") || r.ContentLength != int64(len(got)) || r.Header.Get("Content-Length") == "" {
		t.Fatalf("pages get the badge before </body>: %q", got)
	}
	for name, r := range map[string]*http.Response{
		"fetch() of a page":       pageResp(t, "localhost:8081", "empty", "text/html", "", page),
		"admin screen origin":     pageResp(t, "organism.localhost:8081", "document", "text/html", "", page),
		"compressed":              pageResp(t, "localhost:8081", "document", "text/html", "gzip", page),
		"JSON":                    pageResp(t, "localhost:8081", "document", "application/json", "", `{"a":1}`),
		"no Fetch Metadata (API)": pageResp(t, "localhost:8081", "", "text/html", "", page),
	} {
		if err := injectBadge(r); err != nil {
			t.Fatal(err)
		}
		if strings.Contains(bodyOf(t, r), badgeScriptTag) {
			t.Errorf("%s: must not be rewritten", name)
		}
	}
}

func TestBadgeAnswersOnlyYesOrNo(t *testing.T) {
	k, _ := ownerKernel(t)
	secret, err := k.Owner.Redeem(t.Context(), k.Owner.NewLoginCode(), "")
	if err != nil {
		t.Fatal(err)
	}
	w := httptest.NewRecorder()
	k.handleBadge(w, httptest.NewRequest("GET", "/_seed/badge", nil))
	if w.Code != http.StatusNoContent || w.Body.Len() != 0 {
		t.Fatalf("visitors: no badge: %d", w.Code)
	}
	req := httptest.NewRequest("GET", "/_seed/badge", nil)
	req.AddCookie(&http.Cookie{Name: k.cookieName, Value: secret})
	w = httptest.NewRecorder()
	k.handleBadge(w, req)
	if w.Code != 200 || strings.Contains(w.Body.String(), apiToken(secret)) || strings.Contains(w.Body.String(), secret) {
		t.Fatalf("owner: yes, and nothing usable: %d %s", w.Code, w.Body)
	}
}
