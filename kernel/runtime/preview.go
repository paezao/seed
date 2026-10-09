package runtime

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httputil"
	"net/url"
	"strings"
	"sync"
	"time"

	"seed/kernel/evolution"
	"seed/kernel/sandbox"
)

// Trying an evolution before it goes live (see evolution/preview.go).
//
// My owner opens /_seed/preview/{id} (a navigation that carries their
// session cookie). I give that browser a preview cookie, scoped to my whole
// origin, holding only a random ticket. While it has a valid ticket, my
// organism's paths in that browser are the candidate generation instead of
// the live one, with a bar saying so; everyone else keeps seeing the live
// one. The ticket is bound to the owner's session (signing out ends it) and
// the cookie is never passed on to the organism.

const (
	settingPreview = "preview_before_live"
	previewTTL     = 12 * time.Hour
)

type previewTicket struct {
	evolution string
	session   string
	expires   time.Time
}

type previews struct {
	mu      sync.Mutex
	tickets map[string]previewTicket // sha256(ticket) → what it opens
}

func (p *previews) issue(evo, session string) string {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.tickets == nil {
		p.tickets = map[string]previewTicket{}
	}
	t := randomHex(24)
	p.tickets[hash(t)] = previewTicket{evolution: evo, session: session, expires: time.Now().Add(previewTTL)}
	return t
}

func (p *previews) lookup(ticket string) (previewTicket, bool) {
	p.mu.Lock()
	defer p.mu.Unlock()
	t, ok := p.tickets[hash(ticket)]
	if !ok || time.Now().After(t.expires) {
		delete(p.tickets, hash(ticket))
		return previewTicket{}, false
	}
	return t, true
}

func (k *Kernel) previewCookieName() string {
	return "seed_preview_" + strings.TrimPrefix(k.cookieName, "seed_owner_")
}

// previewFor returns the candidate a request should see, if its browser is
// previewing one my owner may still see.
func (k *Kernel) previewFor(r *http.Request) (string, sandbox.Sandbox, bool) {
	c, err := r.Cookie(k.previewCookieName())
	if err != nil || c.Value == "" {
		return "", nil, false
	}
	t, ok := k.previews.lookup(c.Value)
	if !ok || k.Owner == nil || !k.Owner.Valid(t.session) {
		return "", nil, false
	}
	sb, ok := k.Orch.PreviewSandbox(t.evolution)
	if !ok {
		return "", nil, false
	}
	return t.evolution, sb, true
}

// handlePreviewEnter starts previewing an evolution in this browser.
func (k *Kernel) handlePreviewEnter(w http.ResponseWriter, r *http.Request) {
	if !isNavigation(r) {
		http.Error(w, navigationOnly, http.StatusForbidden)
		return
	}
	secret := k.ownerCookie(r)
	if _, ok := k.Owner.PageToken(r.Context(), secret); !ok {
		k.privatePage(w, r, http.StatusUnauthorized, "")
		return
	}
	id := r.PathValue("id")
	if _, ok := k.Orch.PreviewSandbox(id); !ok {
		http.Redirect(w, r, "/_seed/evolutions/"+url.PathEscape(id), http.StatusSeeOther)
		return
	}
	ticket := k.previews.issue(id, hash(secret))
	http.SetCookie(w, &http.Cookie{Name: k.previewCookieName(), Value: ticket, Path: "/", HttpOnly: true, SameSite: http.SameSiteLaxMode,
		MaxAge: int(previewTTL / time.Second), Secure: r.TLS != nil || strings.EqualFold(r.Header.Get("X-Forwarded-Proto"), "https")})
	w.Header().Set("cache-control", "no-store")
	http.Redirect(w, r, "/", http.StatusSeeOther)
}

// handlePreviewExit stops previewing in this browser.
func (k *Kernel) handlePreviewExit(w http.ResponseWriter, r *http.Request) {
	back := "/_seed/"
	if id, _, ok := k.previewFor(r); ok {
		back = "/_seed/evolutions/" + url.PathEscape(id)
	}
	http.SetCookie(w, &http.Cookie{Name: k.previewCookieName(), Value: "", Path: "/", MaxAge: -1, HttpOnly: true, SameSite: http.SameSiteLaxMode})
	w.Header().Set("cache-control", "no-store")
	http.Redirect(w, r, back, http.StatusSeeOther)
}

// handlePreviewInfo tells the preview bar what is being previewed (only to
// the browser previewing it).
func (k *Kernel) handlePreviewInfo(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("cache-control", "no-store")
	id, _, ok := k.previewFor(r)
	if !ok {
		w.WriteHeader(http.StatusNoContent)
		return
	}
	title := "a new generation"
	data := ""
	if e, err := k.Store.Evolution(r.Context(), id); err == nil {
		if e.Plan != nil && e.Plan.Title != "" {
			title = e.Plan.Title
		}
		if e.Preview != nil {
			data = e.Preview.Data
		}
	}
	writeJSON(w, http.StatusOK, map[string]string{"id": id, "title": title, "data": data})
}

// servePreview proxies a previewing browser's request to the candidate.
func (k *Kernel) servePreview(w http.ResponseWriter, r *http.Request, sb sandbox.Sandbox) {
	target, err := sb.URL(r.Context())
	if err != nil {
		k.Organism.unavailable(w, "unreachable", err.Error())
		return
	}
	u, _ := url.Parse(target)
	proxy := httputil.NewSingleHostReverseProxy(u)
	proxy.Transport = sb.Transport()
	proxy.ModifyResponse = func(resp *http.Response) error {
		resp.Header.Set("Cross-Origin-Opener-Policy", "unsafe-none")
		resp.Header.Del("Service-Worker-Allowed")
		restrictFraming(resp.Header, resp.Request.Host)
		resp.Header.Set("Cache-Control", "no-store") // never mix up live and preview pages
		return injectScript(resp, previewScriptTag)
	}
	proxy.ErrorHandler = func(w http.ResponseWriter, r *http.Request, err error) {
		k.Organism.unavailable(w, "unreachable", err.Error())
	}
	stripCookie(r, k.previewCookieName())
	stripCookie(r, k.cookieName)
	proxy.ServeHTTP(w, r)
}

func stripCookie(r *http.Request, name string) {
	cs := r.Cookies()
	r.Header.Del("Cookie")
	for _, c := range cs {
		if c.Name != name {
			r.AddCookie(c)
		}
	}
}

// handlePreviewDecision applies, changes or discards a previewed evolution.
func (k *Kernel) handlePreviewDecision(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Action   string `json:"action"`
		Feedback string `json:"feedback"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 16<<10)).Decode(&body); err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	if err := k.Orch.Decide(r.PathValue("id"), evolution.Decision{Action: body.Action, Feedback: body.Feedback}); err != nil {
		writeErr(w, http.StatusConflict, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

// handlePreviewSetting shows or sets whether evolutions wait to be tried.
func (k *Kernel) handlePreviewSetting(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodPost {
		var body struct {
			Preview bool `json:"preview"`
		}
		if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1024)).Decode(&body); err != nil {
			writeErr(w, http.StatusBadRequest, err)
			return
		}
		v := "off"
		if body.Preview {
			v = "on"
		}
		if err := k.Store.SetSetting(r.Context(), settingPreview, v); err != nil {
			writeErr(w, http.StatusInternalServerError, err)
			return
		}
	}
	writeJSON(w, http.StatusOK, map[string]bool{"preview": k.previewOn(r)})
}

func (k *Kernel) previewOn(r *http.Request) bool {
	v, err := k.Store.Setting(r.Context(), settingPreview)
	return err != nil || v != "off" // on unless turned off
}

var previewScriptTag = func() string {
	sum := sha256.Sum256([]byte(previewJS))
	return `<script src="/_seed/preview.js?v=` + hex.EncodeToString(sum[:6]) + `" defer></script>`
}()

func (k *Kernel) handlePreviewScript(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/javascript; charset=utf-8")
	w.Header().Set("Cache-Control", "public, max-age=300")
	_, _ = io.WriteString(w, previewJS)
}

// previewJS shows a bar on previewed pages: what this is, and the way back.
const previewJS = `(() => {
  if (window.top !== window || document.getElementById('seed-preview-bar')) return;
  fetch('/_seed/preview/info', { credentials: 'same-origin', cache: 'no-store' }).then((r) => r.status === 200 ? r.json() : null).then((p) => {
    if (!p || document.getElementById('seed-preview-bar')) return;
    const host = document.createElement('div');
    host.id = 'seed-preview-bar';
    Object.assign(host.style, { position: 'fixed', left: '50%', bottom: '14px', transform: 'translateX(-50%)', zIndex: '2147483647', maxWidth: 'calc(100% - 24px)' });
    const root = host.attachShadow({ mode: 'closed' });
    const bar = document.createElement('div');
    Object.assign(bar.style, { display: 'flex', alignItems: 'center', gap: '12px', padding: '9px 10px 9px 14px', borderRadius: '12px',
      background: '#10140f', color: '#e8ece6', border: '1px solid rgba(124,196,149,.4)', boxShadow: '0 8px 28px rgba(0,0,0,.35)',
      font: '13px/1.35 system-ui, -apple-system, Segoe UI, sans-serif', flexWrap: 'wrap' });
    const text = document.createElement('span');
    const strong = document.createElement('strong');
    strong.textContent = 'Previewing: ';
    text.appendChild(strong);
    text.appendChild(document.createTextNode(p.title));
    const note = document.createElement('span');
    note.textContent = p.data === 'copy' ? 'a copy of your data, changes here are thrown away' : 'test data, changes here are thrown away';
    Object.assign(note.style, { color: '#9aa49a', fontSize: '12px' });
    const link = (label, href, primary) => {
      const a = document.createElement('a');
      a.textContent = label;
      a.href = href;
      Object.assign(a.style, { color: primary ? '#0d140f' : '#e8ece6', background: primary ? '#7cc495' : 'transparent',
        border: '1px solid ' + (primary ? '#7cc495' : 'rgba(232,236,230,.3)'), borderRadius: '8px', padding: '5px 10px',
        textDecoration: 'none', fontWeight: '600', whiteSpace: 'nowrap' });
      return a;
    };
    bar.append(text, note, link('Decide', '/_seed/evolutions/' + encodeURIComponent(p.id), true), link('Exit preview', '/_seed/preview-exit', false));
    root.appendChild(bar);
    document.body.appendChild(host);
  }).catch(() => {});
})();
`
