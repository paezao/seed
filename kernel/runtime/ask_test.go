package runtime

import (
	"bytes"
	"image"
	"image/png"
	"net/http"
	"net/http/httptest"
	"strings"

	"seed/kernel/memory"
	"seed/kernel/models"
	"testing"
	"time"
)

func pngBytes(t *testing.T, w, h int) []byte {
	t.Helper()
	var b bytes.Buffer
	if err := png.Encode(&b, image.NewRGBA(image.Rect(0, 0, w, h))); err != nil {
		t.Fatal(err)
	}
	return b.Bytes()
}

// Only my owner's browser, from a page of mine, can leave a screenshot, and
// only a real image of sensible size.
func TestAskDraftAccepts(t *testing.T) {
	k, _ := ownerKernel(t)
	secret, err := k.Owner.Redeem(t.Context(), k.Owner.NewLoginCode(), "")
	if err != nil {
		t.Fatal(err)
	}
	post := func(body []byte, ct, site string, signedIn bool) int {
		r := httptest.NewRequest("POST", "/_seed/ask-draft", bytes.NewReader(body))
		r.Header.Set("Content-Type", ct)
		if site != "" {
			r.Header.Set("Sec-Fetch-Site", site)
		}
		if signedIn {
			r.AddCookie(&http.Cookie{Name: k.cookieName, Value: secret})
		}
		w := httptest.NewRecorder()
		k.handleAskDraft(w, r)
		return w.Code
	}
	img := pngBytes(t, 40, 30)
	for name, code := range map[string]int{
		"visitor":             post(img, "image/png", "same-origin", false),
		"another site":        post(img, "image/png", "cross-site", true),
		"no Fetch Metadata":   post(img, "image/png", "", true),
		"not an image":        post([]byte("<svg onload=alert(1)>"), "image/png", "same-origin", true),
		"claims another type": post(img, "image/jpeg", "same-origin", true),
		"svg":                 post([]byte("<svg/>"), "image/svg+xml", "same-origin", true),
		"too large":           post(pngBytes(t, 5000, 10), "image/png", "same-origin", true),
	} {
		if code < 400 {
			t.Errorf("%s: accepted (%d)", name, code)
		}
	}
	if code := post(img, "image/png", "same-origin", true); code != http.StatusCreated {
		t.Fatalf("my owner's screenshot: %d", code)
	}
	k.badgeOff.Store(true)
	if code := post(img, "image/png", "same-origin", true); code < 400 {
		t.Error("badge off: no drafts")
	}
}

func TestAskDraftsAreFewAndShortLived(t *testing.T) {
	var d askDrafts
	first := d.put("image/png", []byte{1})
	for i := 0; i < maxDrafts; i++ {
		d.put("image/png", []byte{2})
	}
	if _, ok := d.get(first); ok {
		t.Error("the oldest draft makes room")
	}
	id := d.put("image/png", []byte{3})
	if _, ok := d.take(id); !ok {
		t.Fatal("a fresh draft can be taken")
	}
	if _, ok := d.take(id); ok {
		t.Error("a draft is taken once")
	}
	old := d.put("image/png", []byte{4})
	d.mu.Lock()
	v := d.m[old]
	v.expires = time.Now().Add(-time.Second)
	d.m[old] = v
	d.mu.Unlock()
	if _, ok := d.get(old); ok {
		t.Error("drafts expire")
	}
}

// The chat sees only the latest few images, on my owner's turns, and an
// evolution started after "go ahead" still gets the screenshot.
func TestChatImages(t *testing.T) {
	history := []memory.Message{
		{Role: "user", Content: "a", Images: []string{"i1", "i2"}},
		{Role: "seed", Content: "b", Images: []string{"x"}},
		{Role: "user", Content: "c", Images: []string{"i3", "i4"}},
		{Role: "seed", Content: "d"},
		{Role: "user", Content: "go ahead"},
	}
	var loaded []string
	msgs := toModelMessagesWith(history, func(id string) (models.Image, bool) {
		loaded = append(loaded, id)
		return models.Image{MediaType: "image/png", Data: []byte(id)}, true
	})
	if strings.Join(loaded, ",") != "i2,i3,i4" {
		t.Fatalf("loaded %v", loaded)
	}
	if len(msgs[0].Images) != 1 || len(msgs[2].Images) != 2 || len(msgs[1].Images) != 0 {
		t.Fatalf("images on the wrong turns: %+v", msgs)
	}
	if got := lastOwnerImages(history); strings.Join(got, ",") != "i3,i4" {
		t.Fatalf("evolution images: %v", got)
	}
	if got := lastOwnerImages(append(history, memory.Message{Role: "user", Content: "e"}, memory.Message{Role: "user", Content: "f"})); got != nil {
		t.Fatalf("old screenshots stay behind: %v", got)
	}
}
