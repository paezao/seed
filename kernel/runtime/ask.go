package runtime

import (
	"bytes"
	"errors"
	"image"
	_ "image/jpeg" // decoders for checking screenshots
	_ "image/png"
	"io"
	"net/http"
	"strings"
	"sync"
	"time"
)

// Point and ask (see badge.js): my owner points at something on my
// organism's page and says what should change. The badge may attach a
// screenshot of the page. It can't send a message, so it leaves the picture
// here as a draft: a draft does nothing by itself, and only becomes part of
// a message when my owner presses Send in my control plane (which names the
// draft). Organism scripts share the page with the badge and could leave
// drafts too, so drafts are few, small, short-lived, and must be real images.

const (
	maxDraftBytes = 3 << 20
	maxDraftSide  = 4096
	maxDrafts     = 8
	draftTTL      = 30 * time.Minute
	maxMsgImages  = 3
)

type askDraft struct {
	mediaType string
	data      []byte
	expires   time.Time
}

type askDrafts struct {
	mu    sync.Mutex
	m     map[string]askDraft
	order []string
}

func (d *askDrafts) put(mediaType string, data []byte) string {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.m == nil {
		d.m = map[string]askDraft{}
	}
	d.prune()
	for len(d.order) >= maxDrafts {
		delete(d.m, d.order[0])
		d.order = d.order[1:]
	}
	id := randomHex(16)
	d.m[id] = askDraft{mediaType: mediaType, data: data, expires: time.Now().Add(draftTTL)}
	d.order = append(d.order, id)
	return id
}

func (d *askDrafts) get(id string) (askDraft, bool) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.prune()
	v, ok := d.m[id]
	return v, ok
}

func (d *askDrafts) take(id string) (askDraft, bool) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.prune()
	v, ok := d.m[id]
	if ok {
		delete(d.m, id)
		for i, o := range d.order {
			if o == id {
				d.order = append(d.order[:i], d.order[i+1:]...)
				break
			}
		}
	}
	return v, ok
}

func (d *askDrafts) prune() {
	now := time.Now()
	keep := d.order[:0]
	for _, id := range d.order {
		if now.After(d.m[id].expires) {
			delete(d.m, id)
			continue
		}
		keep = append(keep, id)
	}
	d.order = keep
}

// checkImage accepts a PNG or JPEG of sensible size, of the type it claims.
func checkImage(mediaType string, data []byte) error {
	cfg, format, err := image.DecodeConfig(bytes.NewReader(data))
	if err != nil {
		return errors.New("that isn't a PNG or JPEG image")
	}
	if "image/"+format != mediaType {
		return errors.New("the image isn't the type it claims")
	}
	if cfg.Width <= 0 || cfg.Height <= 0 || cfg.Width > maxDraftSide || cfg.Height > maxDraftSide {
		return errors.New("the image is too large")
	}
	return nil
}

// handleAskDraft keeps a screenshot from the badge until my owner sends
// (or doesn't send) the ask it belongs to.
func (k *Kernel) handleAskDraft(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	// From a page of mine, in my owner's browser, while the badge is on.
	if r.Header.Get("Sec-Fetch-Site") != "same-origin" {
		http.Error(w, "same-origin requests only", http.StatusForbidden)
		return
	}
	if _, ok := k.pageToken(r); !ok || k.badgeOff.Load() {
		http.Error(w, "not signed in", http.StatusForbidden)
		return
	}
	mt := strings.ToLower(strings.TrimSpace(strings.SplitN(r.Header.Get("Content-Type"), ";", 2)[0]))
	if mt != "image/jpeg" && mt != "image/png" {
		http.Error(w, "a PNG or JPEG image, please", http.StatusUnsupportedMediaType)
		return
	}
	data, err := io.ReadAll(http.MaxBytesReader(w, r.Body, maxDraftBytes))
	if err != nil {
		http.Error(w, "the image is too large", http.StatusRequestEntityTooLarge)
		return
	}
	if err := checkImage(mt, data); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	writeJSON(w, http.StatusCreated, map[string]string{"id": k.asks.put(mt, data)})
}

// handleAskDraftImage shows a draft's screenshot to my control plane.
func (k *Kernel) handleAskDraftImage(w http.ResponseWriter, r *http.Request) {
	d, ok := k.asks.get(r.PathValue("id"))
	if !ok {
		writeErr(w, http.StatusNotFound, errors.New("that screenshot expired"))
		return
	}
	serveImage(w, d.mediaType, d.data, false)
}

// handleImage serves an image my owner attached to a message.
func (k *Kernel) handleImage(w http.ResponseWriter, r *http.Request) {
	mt, data, err := k.Store.Image(r.Context(), r.PathValue("id"))
	if err != nil {
		writeErr(w, http.StatusNotFound, errors.New("no such image"))
		return
	}
	serveImage(w, mt, data, true)
}

func serveImage(w http.ResponseWriter, mediaType string, data []byte, immutable bool) {
	w.Header().Set("Content-Type", mediaType)
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Content-Security-Policy", "default-src 'none'; sandbox")
	if immutable {
		w.Header().Set("Cache-Control", "private, max-age=86400, immutable")
	} else {
		w.Header().Set("Cache-Control", "no-store")
	}
	_, _ = w.Write(data)
}

// attachDrafts turns the drafts my owner chose to send into stored images.
func (k *Kernel) attachDrafts(r *http.Request, draftIDs []string) ([]string, error) {
	if len(draftIDs) > maxMsgImages {
		return nil, errors.New("too many images")
	}
	var out []string
	for _, id := range draftIDs {
		d, ok := k.asks.take(id)
		if !ok {
			return nil, errors.New("the screenshot expired; point at it again")
		}
		sid, err := k.Store.AddImage(r.Context(), d.mediaType, d.data)
		if err != nil {
			return nil, err
		}
		out = append(out, sid)
	}
	return out, nil
}
