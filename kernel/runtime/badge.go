package runtime

import (
	"bytes"
	"crypto/sha256"
	_ "embed"
	"encoding/hex"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"regexp"
	"strconv"
	"strings"

	"seed/kernel/fsx"
	"seed/kernel/knowledge"
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

// badgeScriptTag carries the script's hash, so a new kernel's badge is never
// hidden behind a cached old one.
var badgeScriptTag = func() string {
	sum := sha256.Sum256([]byte(badgeJS))
	return `<script src="/_seed/badge.js?v=` + hex.EncodeToString(sum[:6]) + `" defer></script>`
}()

var bodyCloseRe = regexp.MustCompile(`(?i)</body\s*>`)

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
func injectBadge(resp *http.Response) error { return injectScript(resp, badgeScriptTag) }

// injectScript adds one script tag to an HTML page response (a top-level
// page load on my main origin; uncompressed; not too large).
func injectScript(resp *http.Response, tag string) error {
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
	// Found in the original bytes (lowercasing a copy can change its length
	// for some non-ASCII text, and the index would point elsewhere).
	if m := bodyCloseRe.FindAllIndex(body, -1); len(m) > 0 {
		i := m[len(m)-1][0]
		body = append(body[:i:i], append([]byte(tag), body[i:]...)...)
	} else {
		body = append(body, []byte(tag)...)
	}
	resp.Body = io.NopCloser(bytes.NewReader(body))
	resp.ContentLength = int64(len(body))
	resp.Header.Set("Content-Length", strconv.Itoa(len(body)))
	resp.Header.Del("ETag") // the page I serve isn't the organism's byte for byte
	return nil
}

const settingBadge = "owner_badge"

// handleBadge answers whether this browser is my owner's (yes: 200, no: 204).
// Turned off, it answers no to everyone.
func (k *Kernel) handleBadge(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	if _, ok := k.pageToken(r); !ok || k.badgeOff.Load() {
		w.WriteHeader(http.StatusNoContent)
		return
	}
	// The badge shows who I am now (my logo, versioned so a new one isn't
	// hidden behind a cached old one); with no logo yet, the seed.
	out := map[string]any{"owner": true}
	if b, err := fsx.ReadFile(k.Cfg.Root, knowledge.LogoPath); err == nil {
		sum := sha256.Sum256(b)
		out["logo"] = "/_seed/api/identity/logo?v=" + hex.EncodeToString(sum[:6])
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(out)
}

func (k *Kernel) handleBadgeScript(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/javascript; charset=utf-8")
	w.Header().Set("Cache-Control", "public, max-age=300")
	_, _ = io.WriteString(w, badgeJS)
}

// badgeJS is the badge itself (badge.js): the way back, and point and ask.
//
//go:embed badge.js
var badgeJS string

// handleBadgeSetting shows or sets whether the badge is on (Settings).
func (k *Kernel) handleBadgeSetting(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodPost {
		var body struct {
			Enabled bool `json:"enabled"`
		}
		if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1024)).Decode(&body); err != nil {
			writeErr(w, http.StatusBadRequest, err)
			return
		}
		v := "on"
		if !body.Enabled {
			v = "off"
		}
		if err := k.Store.SetSetting(r.Context(), settingBadge, v); err != nil {
			writeErr(w, http.StatusInternalServerError, err)
			return
		}
		k.badgeOff.Store(!body.Enabled)
	}
	writeJSON(w, http.StatusOK, map[string]bool{"enabled": !k.badgeOff.Load()})
}

// screenshotJS draws a page into an image for point and ask (modern-screenshot
// 4.7.0, MIT: see vendor/modern-screenshot.LICENSE). The badge loads it only
// when my owner points at something.
//
//go:embed vendor/modern-screenshot.js
var screenshotJS string

func (k *Kernel) handleScreenshotScript(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/javascript; charset=utf-8")
	w.Header().Set("Cache-Control", "public, max-age=86400")
	_, _ = io.WriteString(w, screenshotJS)
}
