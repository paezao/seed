package runtime

import (
	"bytes"
	"io"
	"net"
	"net/http"
	"strconv"
	"strings"
)

// The owner's way back: on my organism's pages, a small floating seed that
// links to my control plane, shown only to my signed-in owner.
//
// I inject one external script into organism HTML pages (no change to the
// organism's code). The script asks /_seed/badge whether this browser is my
// owner's (the session cookie, scoped to /_seed, is sent with that request);
// the answer is only yes or no, never a token, so organism scripts that ask
// the same learn nothing they could use. The badge lives in a closed shadow
// root and is styled through the CSSOM, so the organism's CSS can't restyle
// it and a strict CSP doesn't block it.

const badgeScriptTag = `<script src="/_seed/badge.js" defer></script>`

// maxInjectable bounds the pages I rewrite (larger ones pass untouched).
const maxInjectable = 8 << 20

// wantsBadge reports whether a request is a page load I may add the badge
// to: a top-level navigation on my main origin (not the admin-screen origin,
// which is already inside the control plane).
func wantsBadge(r *http.Request) bool {
	if r.Method != http.MethodGet || r.Header.Get("Sec-Fetch-Dest") != "document" {
		return false
	}
	host := r.Host
	if h, _, err := net.SplitHostPort(host); err == nil {
		host = h
	}
	return !strings.EqualFold(host, OrganismFrameHost)
}

// injectBadge adds the badge script to an HTML page response.
func injectBadge(resp *http.Response) error {
	if resp.Request == nil || !wantsBadge(resp.Request) || resp.StatusCode != http.StatusOK {
		return nil
	}
	if !strings.HasPrefix(strings.ToLower(resp.Header.Get("Content-Type")), "text/html") {
		return nil
	}
	if enc := resp.Header.Get("Content-Encoding"); enc != "" && !strings.EqualFold(enc, "identity") {
		return nil
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxInjectable+1))
	if err != nil {
		return err
	}
	if len(body) > maxInjectable {
		resp.Body = struct {
			io.Reader
			io.Closer
		}{io.MultiReader(bytes.NewReader(body), resp.Body), resp.Body}
		return nil
	}
	resp.Body.Close()
	if i := bytes.LastIndex(bytes.ToLower(body), []byte("</body>")); i >= 0 {
		body = append(body[:i:i], append([]byte(badgeScriptTag), body[i:]...)...)
	} else {
		body = append(body, []byte(badgeScriptTag)...)
	}
	resp.Body = io.NopCloser(bytes.NewReader(body))
	resp.ContentLength = int64(len(body))
	resp.Header.Set("Content-Length", strconv.Itoa(len(body)))
	resp.Header.Del("ETag") // the page I serve isn't the organism's byte for byte
	return nil
}

// handleBadge answers whether this browser is my owner's (yes: 200, no: 204).
func (k *Kernel) handleBadge(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	if _, ok := k.pageToken(r); !ok {
		w.WriteHeader(http.StatusNoContent)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_, _ = io.WriteString(w, `{"owner":true}`)
}

func (k *Kernel) handleBadgeScript(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/javascript; charset=utf-8")
	w.Header().Set("Cache-Control", "public, max-age=300")
	_, _ = io.WriteString(w, badgeJS)
}

const badgeJS = `(() => {
  if (window.top !== window || document.getElementById('seed-badge')) return;
  fetch('/_seed/badge', { credentials: 'same-origin', cache: 'no-store' }).then((r) => {
    if (r.status !== 200 || document.getElementById('seed-badge')) return;
    const host = document.createElement('div');
    host.id = 'seed-badge';
    Object.assign(host.style, { position: 'fixed', right: '16px', bottom: '16px', zIndex: '2147483647' });
    const root = host.attachShadow({ mode: 'closed' });
    const a = document.createElement('a');
    a.href = '/_seed/';
    a.title = 'Back to my control plane';
    a.setAttribute('aria-label', 'Back to my control plane');
    Object.assign(a.style, {
      display: 'flex', alignItems: 'center', justifyContent: 'center', width: '40px', height: '40px',
      borderRadius: '50%', background: '#10140f', color: '#7cc495', border: '1px solid rgba(124,196,149,.35)',
      boxShadow: '0 4px 14px rgba(0,0,0,.25)', opacity: '.82', transition: 'opacity .15s, transform .15s',
      textDecoration: 'none', outlineOffset: '3px',
    });
    const grow = (on) => { a.style.opacity = on ? '1' : '.82'; a.style.transform = on ? 'scale(1.07)' : 'none'; };
    a.addEventListener('mouseenter', () => grow(true));
    a.addEventListener('mouseleave', () => grow(false));
    a.addEventListener('focus', () => grow(true));
    a.addEventListener('blur', () => grow(false));
    const NS = 'http://www.w3.org/2000/svg';
    const svg = document.createElementNS(NS, 'svg');
    svg.setAttribute('width', '20'); svg.setAttribute('height', '20'); svg.setAttribute('viewBox', '0 0 24 24');
    svg.setAttribute('aria-hidden', 'true');
    for (const [d, attrs] of [
      ['M12 21v-9', { stroke: 'currentColor', 'stroke-width': '1.8', 'stroke-linecap': 'round', fill: 'none' }],
      ['M12 13c0-4.4 2.9-7.3 7.8-7.3 0 4.4-2.9 7.3-7.8 7.3z', { fill: 'currentColor' }],
      ['M12 15.5c0-3.3-2.2-5.6-5.8-5.6 0 3.3 2.2 5.6 5.8 5.6z', { fill: 'currentColor', opacity: '.6' }],
    ]) {
      const p = document.createElementNS(NS, 'path');
      p.setAttribute('d', d);
      for (const [k, v] of Object.entries(attrs)) p.setAttribute(k, v);
      svg.appendChild(p);
    }
    a.appendChild(svg);
    root.appendChild(a);
    const media = window.matchMedia('print');
    const print = () => { host.style.display = media.matches ? 'none' : ''; };
    media.addEventListener && media.addEventListener('change', print);
    document.body.appendChild(host);
  }).catch(() => {});
})();
`
