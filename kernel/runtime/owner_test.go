package runtime

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"seed/kernel/config"
	"seed/kernel/memory"
	"seed/kernel/testutil"
)

func ownerKernel(t *testing.T) (*Kernel, http.Handler) {
	t.Helper()
	ctx := context.Background()
	store, err := memory.Open(ctx, testutil.Database(t))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(store.Close)
	owner, err := NewOwner(ctx, store)
	if err != nil {
		t.Fatal(err)
	}
	k := &Kernel{Cfg: &config.Config{Root: t.TempDir()}, Store: store, Owner: owner, Token: "cli-token", cookieName: "seed_owner_test"}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /_seed/login", k.handleLogin)
	mux.HandleFunc("POST /_seed/login", k.handlePasswordLogin)
	mux.HandleFunc("POST /_seed/api/login-links", k.handleLoginLink)
	mux.HandleFunc("POST /_seed/api/owner/logout", k.handleLogout)
	mux.HandleFunc("GET /_seed/api/owner/sessions", k.handleOwnerSessions)
	mux.HandleFunc("GET /_seed/api/whoami", func(w http.ResponseWriter, r *http.Request) { w.Write([]byte("in:" + currentSession(r))) })
	mux.HandleFunc("/_seed/", func(w http.ResponseWriter, r *http.Request) {
		if tok, ok := k.pageToken(r); ok {
			w.Write([]byte("page:" + tok))
			return
		}
		k.privatePage(w, r, http.StatusUnauthorized, "")
	})
	return k, guardAPI(k.requireToken(mux))
}

var nav = map[string]string{"Sec-Fetch-Dest": "document", "Sec-Fetch-Mode": "navigate"}

func do(h http.Handler, method, target string, hdr map[string]string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, target, nil)
	for k, v := range hdr {
		req.Header.Set(k, v)
	}
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	return w
}

func signIn(t *testing.T, k *Kernel, h http.Handler) (cookie, token string) {
	t.Helper()
	code := k.Owner.NewLoginCode()
	w := do(h, "GET", "/_seed/login?code="+code, nav)
	if w.Code != http.StatusSeeOther {
		t.Fatalf("sign-in: %d %s", w.Code, w.Body)
	}
	c := w.Result().Cookies()[0]
	if !c.HttpOnly || c.Path != "/_seed" || c.SameSite != http.SameSiteLaxMode {
		t.Fatalf("cookie must be HttpOnly, scoped to /_seed, SameSite=Lax: %+v", c)
	}
	page := do(h, "GET", "/_seed/", map[string]string{"Cookie": c.Name + "=" + c.Value, "Sec-Fetch-Dest": "document", "Sec-Fetch-Mode": "navigate"})
	if !strings.HasPrefix(page.Body.String(), "page:") {
		t.Fatalf("signed-in browser should get the page: %s", page.Body)
	}
	return c.Name + "=" + c.Value, strings.TrimPrefix(page.Body.String(), "page:")
}

func TestOwnerSignIn(t *testing.T) {
	k, h := ownerKernel(t)
	if w := do(h, "GET", "/_seed/", nav); w.Code != http.StatusUnauthorized || !strings.Contains(w.Body.String(), "seed login") {
		t.Fatalf("strangers see the private page: %d", w.Code)
	}
	code := k.Owner.NewLoginCode()
	if w := do(h, "GET", "/_seed/login?code="+code, map[string]string{"Sec-Fetch-Dest": "empty"}); w.Code != http.StatusForbidden {
		t.Fatal("a sign-in link must not be redeemable by fetch() (organism scripts)")
	}
	cookie, token := signIn(t, k, h)

	// The cookie alone never authorizes the API: organism scripts on the
	// same origin would send it too.
	if w := do(h, "GET", "/_seed/api/whoami", map[string]string{"Cookie": cookie}); w.Code != http.StatusUnauthorized {
		t.Fatalf("cookie without token must be refused: %d", w.Code)
	}
	if w := do(h, "GET", "/_seed/api/whoami", map[string]string{"X-Seed-Token": token}); w.Code != 200 || w.Body.String() == "in:" {
		t.Fatalf("session token should work: %d %s", w.Code, w.Body)
	}
	if w := do(h, "GET", "/_seed/api/whoami", map[string]string{"X-Seed-Token": "cli-token"}); w.Body.String() != "in:" {
		t.Fatal("CLI token should work, as no browser session")
	}
	if w := do(h, "GET", "/_seed/api/whoami", map[string]string{"X-Seed-Token": "nope"}); w.Code != http.StatusUnauthorized {
		t.Fatal("bad token must be refused")
	}

	// Sessions survive a restart (they live in my memory, as hashes).
	k2, err := NewOwner(context.Background(), k.Store)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := k2.SessionForToken(context.Background(), token); !ok {
		t.Fatal("session should survive a kernel restart")
	}

	// Sign out: cookie and token stop working.
	w := do(h, "POST", "/_seed/api/owner/logout", map[string]string{"X-Seed-Token": token, "Content-Type": "application/json"})
	if w.Code != 200 || w.Result().Cookies()[0].MaxAge >= 0 {
		t.Fatalf("logout should clear the cookie: %d", w.Code)
	}
	if w := do(h, "GET", "/_seed/api/whoami", map[string]string{"X-Seed-Token": token}); w.Code != http.StatusUnauthorized {
		t.Fatal("token must stop working after sign-out")
	}
	if w := do(h, "GET", "/_seed/", map[string]string{"Cookie": cookie, "Sec-Fetch-Dest": "document", "Sec-Fetch-Mode": "navigate"}); w.Code != http.StatusUnauthorized {
		t.Fatal("cookie must stop working after sign-out")
	}
}

func TestLoginCodesAreOneTimeAndExpire(t *testing.T) {
	k, h := ownerKernel(t)
	code := k.Owner.NewLoginCode()
	signInWith := func(c string) int {
		return do(h, "GET", "/_seed/login?code="+c, nav).Code
	}
	if signInWith(code) != http.StatusSeeOther {
		t.Fatal("first use should sign in")
	}
	if signInWith(code) != http.StatusForbidden {
		t.Fatal("a code works only once")
	}
	if signInWith("") != http.StatusForbidden || signInWith("guess") != http.StatusForbidden {
		t.Fatal("empty or wrong codes must fail")
	}
	late := k.Owner.NewLoginCode()
	k.Owner.now = func() time.Time { return time.Now().Add(loginCodeTTL + time.Minute) }
	if signInWith(late) != http.StatusForbidden {
		t.Fatal("expired codes must fail")
	}
}

func TestSessionExpiresAndRevokes(t *testing.T) {
	k, h := ownerKernel(t)
	_, token := signIn(t, k, h)
	_, other := signIn(t, k, h)
	list := k.Owner.List("")
	if len(list) != 2 {
		t.Fatalf("two browsers: %v", list)
	}
	if err := k.Owner.Revoke(context.Background(), list[0].ID); err != nil {
		t.Fatal(err)
	}
	if len(k.Owner.List("")) != 1 {
		t.Fatal("revoked by public id")
	}
	k.Owner.now = func() time.Time { return time.Now().Add(sessionTTL + time.Hour) }
	for _, tok := range []string{token, other} {
		if _, ok := k.Owner.SessionForToken(context.Background(), tok); ok {
			t.Fatal("sessions expire")
		}
	}
}

func TestPrivatePageEscapesName(t *testing.T) {
	k, _ := ownerKernel(t)
	w := httptest.NewRecorder()
	k.privatePage(w, httptest.NewRequest("GET", "/_seed/", nil), 401, `<script>x</script>`)
	if strings.Contains(w.Body.String(), "<script>x") {
		t.Fatal("problem text must be escaped")
	}
}

func TestOldBrowsersDontGetTheToken(t *testing.T) {
	k, h := ownerKernel(t)
	code := k.Owner.NewLoginCode()
	// No Fetch Metadata: could be fetch() from organism code in an old browser.
	if w := do(h, "GET", "/_seed/login?code="+code, nil); w.Code != http.StatusForbidden {
		t.Fatal("sign-in without Fetch Metadata must be refused")
	}
	secret, _ := k.Owner.Redeem(t.Context(), k.Owner.NewLoginCode(), "")
	r := httptest.NewRequest("GET", "/_seed/", nil)
	r.AddCookie(&http.Cookie{Name: k.cookieName, Value: secret})
	w := httptest.NewRecorder()
	k.controlUI().ServeHTTP(w, r)
	if w.Code != http.StatusForbidden || strings.Contains(w.Body.String(), apiToken(secret)) {
		t.Fatalf("page without Fetch Metadata must not carry the token: %d", w.Code)
	}
}

func TestStreamsEndWithTheSession(t *testing.T) {
	k, h := ownerKernel(t)
	_, token := signIn(t, k, h)
	id, ok := k.Owner.SessionForToken(t.Context(), token)
	if !ok {
		t.Fatal("signed in")
	}
	ended := k.Owner.Ended(id)
	select {
	case <-ended:
		t.Fatal("live session's stream must stay open")
	default:
	}
	if err := k.Owner.Revoke(t.Context(), PublicID(id)); err != nil {
		t.Fatal(err)
	}
	select {
	case <-ended:
	case <-time.After(time.Second):
		t.Fatal("revoking must end the session's open streams")
	}
	if k.Owner.Valid(id) {
		t.Fatal("revoked session is not valid")
	}
}

func postLogin(h http.Handler, user, pass string, hdr map[string]string) *httptest.ResponseRecorder {
	form := url.Values{"username": {user}, "password": {pass}}
	req := httptest.NewRequest("POST", "/_seed/login", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Sec-Fetch-Dest", "document")
	req.Header.Set("Sec-Fetch-Mode", "navigate")
	req.Header.Set("Sec-Fetch-Site", "same-origin")
	for k, v := range hdr {
		req.Header.Set(k, v)
	}
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	return w
}

func TestPasswordSignIn(t *testing.T) {
	k, h := ownerKernel(t)
	if w := postLogin(h, "owner", "whatever-long-pass", nil); w.Code == http.StatusSeeOther {
		t.Fatal("password sign-in must be off unless a password was passed")
	}
	if err := k.Owner.SetPassword("", "short"); err == nil {
		t.Fatal("short passwords must be refused")
	}
	if on, _ := k.Owner.PasswordEnabled(); on {
		t.Fatal("a refused password leaves sign-in off")
	}
	if err := k.Owner.SetPassword("", "correct horse battery"); err != nil {
		t.Fatal(err)
	}
	if w := do(h, "GET", "/_seed/", nav); !strings.Contains(w.Body.String(), `type="password"`) {
		t.Fatal("the private page should offer the sign-in form")
	}
	if w := postLogin(h, "owner", "wrong password!!", nil); w.Code != http.StatusUnauthorized {
		t.Fatalf("wrong password: %d", w.Code)
	}
	if w := postLogin(h, "admin", "correct horse battery", nil); w.Code != http.StatusUnauthorized {
		t.Fatal("wrong username must fail")
	}
	if w := postLogin(h, "owner", "correct horse battery", map[string]string{"Sec-Fetch-Site": "cross-site"}); w.Code != http.StatusForbidden {
		t.Fatal("another site must not be able to sign me in")
	}
	if w := postLogin(h, "owner", "correct horse battery", map[string]string{"Origin": "https://evil.example"}); w.Code != http.StatusForbidden {
		t.Fatal("cross-origin sign-in must be refused")
	}
	if w := postLogin(h, "owner", "correct horse battery", map[string]string{"Sec-Fetch-Dest": "empty", "Sec-Fetch-Mode": "cors"}); w.Code != http.StatusForbidden {
		t.Fatal("fetch() (organism scripts) must not sign in")
	}
	if w := postLogin(h, "owner", "correct horse battery", map[string]string{"Origin": "null", "Sec-Fetch-Site": "cross-site"}); w.Code != http.StatusForbidden {
		t.Fatal("Origin: null from another site must be refused")
	}
	// What a real browser sends from the sign-in page when the referrer is hidden.
	if w := postLogin(h, "owner", "correct horse battery", map[string]string{"Origin": "null"}); w.Code != http.StatusSeeOther {
		t.Fatalf("Origin: null with Sec-Fetch-Site: same-origin is my own form: %d", w.Code)
	}
	w := postLogin(h, "owner", "correct horse battery", map[string]string{"X-Forwarded-Proto": "https"})
	if w.Code != http.StatusSeeOther {
		t.Fatalf("right password: %d %s", w.Code, w.Body)
	}
	if c := w.Result().Cookies()[0]; !c.Secure || !c.HttpOnly {
		t.Fatal("behind HTTPS the cookie must be Secure")
	}
}

func TestPasswordSignInIsRateLimited(t *testing.T) {
	k, h := ownerKernel(t)
	if err := k.Owner.SetPassword("me", "correct horse battery"); err != nil {
		t.Fatal(err)
	}
	// Behind a proxy (httptest's peer is 192.0.2.1; use a private peer).
	from := func(client, spoof string) map[string]string {
		xff := client
		if spoof != "" {
			xff = spoof + ", " + client // the client can only add to the left
		}
		return map[string]string{"X-Forwarded-For": xff}
	}
	k.trustedProxies = 1 // one ingress in front, which appends the client
	h = withPeer(h, "10.0.0.2:5000")
	for i := 0; i < maxClientFails; i++ {
		postLogin(h, "me", "guess", from("203.0.113.9", "1.2.3."+string(rune('0'+i))))
	}
	if w := postLogin(h, "me", "correct horse battery", from("203.0.113.9", "9.9.9.9")); w.Code != http.StatusTooManyRequests {
		t.Fatalf("a limited client is refused even with the right password (no oracle), and spoofing X-Forwarded-For doesn't help: %d", w.Code)
	}
	if w := postLogin(h, "me", "correct horse battery", from("198.51.100.7", "")); w.Code != http.StatusSeeOther {
		t.Fatalf("other clients can still sign in: %d", w.Code)
	}
	k.Owner.now = func() time.Time { return time.Now().Add(failureWindow + time.Minute) }
	if w := postLogin(h, "me", "correct horse battery", from("203.0.113.9", "")); w.Code != http.StatusSeeOther {
		t.Fatal("the limit lifts after the window")
	}
}

func withPeer(h http.Handler, addr string) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		r.RemoteAddr = addr
		h.ServeHTTP(w, r)
	})
}

func TestForwardedForIsIgnoredUnlessProxiesAreTrusted(t *testing.T) {
	k, h := ownerKernel(t)
	if err := k.Owner.SetPassword("me", "correct horse battery"); err != nil {
		t.Fatal(err)
	}
	// No proxy configured: the peer (Docker's private gateway, say) is the
	// client, whatever X-Forwarded-For claims.
	h = withPeer(h, "172.17.0.1:40000")
	for i := 0; i < maxClientFails; i++ {
		postLogin(h, "me", "guess", map[string]string{"X-Forwarded-For": "203.0.113." + string(rune('1'+i))})
	}
	if w := postLogin(h, "me", "guess", map[string]string{"X-Forwarded-For": "198.51.100.200"}); w.Code != http.StatusTooManyRequests {
		t.Fatalf("rotating a forged X-Forwarded-For must not escape the limit: %d", w.Code)
	}
}

func TestIPv6ClientsAreGroupedBy48(t *testing.T) {
	if ipGroup("2001:db8:1:2::1") != ipGroup("2001:db8:1:ffff::abcd") {
		t.Fatal("addresses in one /48 are one client")
	}
	if ipGroup("2001:db8:1::1") == ipGroup("2001:db8:2::1") {
		t.Fatal("different /48s are different clients")
	}
	if ipGroup("203.0.113.9") != "203.0.113.9" || ipGroup("::ffff:203.0.113.9") != "203.0.113.9" {
		t.Fatal("IPv4 (and IPv4-mapped) addresses are themselves")
	}
}

func TestSignInPageIsolatedFromOrganismWindows(t *testing.T) {
	k, _ := ownerKernel(t)
	if err := k.Owner.SetPassword("", "correct horse battery"); err != nil {
		t.Fatal(err)
	}
	w := httptest.NewRecorder()
	k.privatePage(w, httptest.NewRequest("GET", "/_seed/", nil), 401, "")
	if w.Header().Get("Cross-Origin-Opener-Policy") != "same-origin" || w.Header().Get("X-Frame-Options") != "DENY" {
		t.Fatal("an organism page must not be able to hold a handle on (or frame) the sign-in page")
	}
}
