// Package update brings a Seed new kernels: signed releases of the Seed
// repository. A release is a template (the pristine Seed, as `seed new`
// plants it), a manifest describing it, and an Ed25519 signature of the
// manifest. A kernel installs only releases signed by a key it carries, whose
// template matches the manifest's hash, and that are newer than what it has.
package update

import (
	"context"
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// DefaultSource is where releases are published.
const DefaultSource = "https://github.com/paezao/seed/releases/latest/download/"

// Manifest describes a release. It is what gets signed.
type Manifest struct {
	Version     string    `json:"version"`
	PublishedAt time.Time `json:"published_at"`
	Notes       string    `json:"notes"`
	// Template is the SHA-256 (hex) of template.tar.gz.
	Template     string `json:"template_sha256"`
	TemplateSize int64  `json:"template_size"`
	// Runtime is the SHA-256 (hex) of the release's Dockerfile: when it
	// differs from mine, the release needs a new runtime image.
	Runtime string `json:"runtime_sha256"`
}

const (
	maxManifest = 64 << 10
	maxTemplate = 64 << 20
)

var (
	ErrBadSignature = errors.New("the release's signature does not verify")
	ErrNotNewer     = errors.New("the release is not newer than my kernel")
)

// Verify checks a manifest's signature against the trusted keys.
func Verify(manifest, sig []byte, keys []ed25519.PublicKey) (*Manifest, error) {
	s, err := base64.StdEncoding.DecodeString(strings.TrimSpace(string(sig)))
	if err != nil || len(s) != ed25519.SignatureSize {
		return nil, ErrBadSignature
	}
	ok := false
	for _, k := range keys {
		if ed25519.Verify(k, manifest, s) {
			ok = true
			break
		}
	}
	if !ok {
		return nil, ErrBadSignature
	}
	var m Manifest
	dec := json.NewDecoder(strings.NewReader(string(manifest)))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&m); err != nil {
		return nil, fmt.Errorf("manifest: %w", err)
	}
	if m.Version == "" || m.PublishedAt.IsZero() || len(m.Template) != 64 || m.TemplateSize <= 0 || m.TemplateSize > maxTemplate {
		return nil, errors.New("manifest is incomplete")
	}
	return &m, nil
}

// Sign signs a manifest (for publishing releases).
func Sign(manifest []byte, key ed25519.PrivateKey) []byte {
	return []byte(base64.StdEncoding.EncodeToString(ed25519.Sign(key, manifest)) + "\n")
}

// Newer reports whether m should replace a kernel released at installed
// (zero when unknown, e.g. a development kernel) with version current.
func Newer(m *Manifest, current string, installed time.Time) bool {
	return m.Version != current && m.PublishedAt.After(installed)
}

// Client fetches releases from a source (a URL prefix).
type Client struct {
	Source string
	Keys   []ed25519.PublicKey
	HTTP   *http.Client
}

func (c *Client) get(ctx context.Context, name string, limit int64) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, "GET", strings.TrimSuffix(c.Source, "/")+"/"+name, nil)
	if err != nil {
		return nil, err
	}
	hc := c.HTTP
	if hc == nil {
		hc = &http.Client{Timeout: 2 * time.Minute}
	}
	resp, err := hc.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return nil, fmt.Errorf("%s: HTTP %d", name, resp.StatusCode)
	}
	b, err := io.ReadAll(io.LimitReader(resp.Body, limit+1))
	if err != nil {
		return nil, err
	}
	if int64(len(b)) > limit {
		return nil, fmt.Errorf("%s is too large", name)
	}
	return b, nil
}

// Latest fetches and verifies the latest release's manifest.
func (c *Client) Latest(ctx context.Context) (*Manifest, error) {
	manifest, err := c.get(ctx, "manifest.json", maxManifest)
	if err != nil {
		return nil, err
	}
	sig, err := c.get(ctx, "manifest.json.sig", 1024)
	if err != nil {
		return nil, err
	}
	return Verify(manifest, sig, c.Keys)
}

// Template downloads a release's template and checks it is the one the
// (verified) manifest names.
func (c *Client) Template(ctx context.Context, m *Manifest) ([]byte, error) {
	b, err := c.get(ctx, "template.tar.gz", m.TemplateSize)
	if err != nil {
		return nil, err
	}
	sum := sha256.Sum256(b)
	if hex.EncodeToString(sum[:]) != m.Template {
		return nil, errors.New("the downloaded template does not match the signed manifest")
	}
	return b, nil
}

// RuntimeHash is the hash a manifest uses for a Dockerfile.
func RuntimeHash(dockerfile []byte) string {
	sum := sha256.Sum256(dockerfile)
	return hex.EncodeToString(sum[:])
}
