package runtime

import (
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"sort"
	"strings"
	"sync"
	"time"

	"seed/kernel/memory"
)

// Owner is who may use my control plane.
//
// The owner signs a browser in with a one-time link (from `seed login`, or
// opened by `seed run`). The browser then holds an HttpOnly cookie, scoped
// to /_seed, that is never accepted as authorization by itself: my organism
// is served on the same origin, so its scripts' requests to /_seed would
// carry the cookie too. The cookie only unlocks the control plane page,
// which carries an API token derived from it; API calls must present that
// token in a header, which organism scripts cannot read.
type Owner struct {
	store *memory.Store
	now   func() time.Time

	mu    sync.Mutex
	byID  map[string]*memory.OwnerSession // sha256(cookie secret) → session
	byAPI map[string]string               // sha256(API token) → session id
	codes map[string]time.Time            // sha256(one-time code) → expiry
}

const (
	loginCodeTTL   = 15 * time.Minute
	sessionTTL     = 30 * 24 * time.Hour // renewed whenever the session is used
	touchEvery     = 10 * time.Minute
	maxLoginCodes  = 20
	maxSessions    = 20
	apiTokenDomain = "seed control-plane API token"
)

var ErrBadLoginCode = errors.New("this sign-in link has expired or was already used")

// NewOwner loads my owner's signed-in browsers.
func NewOwner(ctx context.Context, store *memory.Store) (*Owner, error) {
	o := &Owner{store: store, now: time.Now, byID: map[string]*memory.OwnerSession{}, byAPI: map[string]string{}, codes: map[string]time.Time{}}
	sessions, err := store.OwnerSessions(ctx)
	if err != nil {
		return nil, err
	}
	for _, s := range sessions {
		o.byID[s.ID], o.byAPI[s.APIHash] = s, s.ID
	}
	return o, nil
}

func randomHex(n int) string {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		panic(err)
	}
	return hex.EncodeToString(b)
}

func hash(s string) string {
	sum := sha256.Sum256([]byte(s))
	return hex.EncodeToString(sum[:])
}

// apiToken derives a session's API token from its cookie secret, so the
// page can carry it without my storing it.
func apiToken(secret string) string {
	m := hmac.New(sha256.New, []byte(secret))
	m.Write([]byte(apiTokenDomain))
	return hex.EncodeToString(m.Sum(nil))
}

// NewLoginCode returns a one-time sign-in code.
func (o *Owner) NewLoginCode() string {
	o.mu.Lock()
	defer o.mu.Unlock()
	now := o.now()
	for h, exp := range o.codes {
		if now.After(exp) {
			delete(o.codes, h)
		}
	}
	for len(o.codes) >= maxLoginCodes { // drop the oldest
		var oldest string
		for h, exp := range o.codes {
			if oldest == "" || exp.Before(o.codes[oldest]) {
				oldest = h
			}
		}
		delete(o.codes, oldest)
	}
	code := randomHex(16)
	o.codes[hash(code)] = now.Add(loginCodeTTL)
	return code
}

// Redeem exchanges a one-time code for a new session's cookie secret.
func (o *Owner) Redeem(ctx context.Context, code, label string) (string, error) {
	o.mu.Lock()
	h := hash(code)
	exp, ok := o.codes[h]
	delete(o.codes, h) // one use, even if expired
	o.mu.Unlock()
	if code == "" || !ok || o.now().After(exp) {
		return "", ErrBadLoginCode
	}
	secret := randomHex(32)
	now := o.now()
	s := &memory.OwnerSession{ID: hash(secret), APIHash: hash(apiToken(secret)), Label: truncateLabel(label),
		CreatedAt: now, LastSeenAt: now, ExpiresAt: now.Add(sessionTTL)}
	if err := o.store.CreateOwnerSession(ctx, s); err != nil {
		return "", err
	}
	o.mu.Lock()
	o.byID[s.ID], o.byAPI[s.APIHash] = s, s.ID
	var drop []string
	if len(o.byID) > maxSessions {
		all := o.listLocked()
		for _, old := range all[maxSessions:] {
			drop = append(drop, old.ID)
		}
	}
	o.mu.Unlock()
	for _, id := range drop {
		_ = o.Revoke(ctx, id)
	}
	return secret, nil
}

// PageToken returns the API token for the browser holding secret.
func (o *Owner) PageToken(ctx context.Context, secret string) (string, bool) {
	if secret == "" {
		return "", false
	}
	id := hash(secret)
	if !o.live(ctx, id) {
		return "", false
	}
	return apiToken(secret), true
}

// SessionForToken returns the session an API token belongs to.
func (o *Owner) SessionForToken(ctx context.Context, token string) (string, bool) {
	if token == "" {
		return "", false
	}
	o.mu.Lock()
	id, ok := o.byAPI[hash(token)]
	o.mu.Unlock()
	if !ok || !o.live(ctx, id) {
		return "", false
	}
	return id, true
}

// live reports whether a session is valid, renewing it as it is used.
func (o *Owner) live(ctx context.Context, id string) bool {
	o.mu.Lock()
	s, ok := o.byID[id]
	now := o.now()
	if ok && now.After(s.ExpiresAt) {
		delete(o.byID, id)
		delete(o.byAPI, s.APIHash)
		ok = false
	}
	touch := ok && now.Sub(s.LastSeenAt) > touchEvery
	if touch {
		s.LastSeenAt, s.ExpiresAt = now, now.Add(sessionTTL)
	}
	o.mu.Unlock()
	if touch {
		_ = o.store.TouchOwnerSession(ctx, id, now, now.Add(sessionTTL))
	}
	return ok
}

// SessionView is a signed-in browser as shown to the owner.
type SessionView struct {
	ID         string    `json:"id"`
	Label      string    `json:"label"`
	CreatedAt  time.Time `json:"created_at"`
	LastSeenAt time.Time `json:"last_seen_at"`
	Current    bool      `json:"current"`
}

// PublicID names a session in the API without revealing anything usable.
func PublicID(id string) string { return id[:16] }

func (o *Owner) listLocked() []*memory.OwnerSession {
	out := make([]*memory.OwnerSession, 0, len(o.byID))
	for _, s := range o.byID {
		out = append(out, s)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].LastSeenAt.After(out[j].LastSeenAt) })
	return out
}

// List returns the signed-in browsers; current is the caller's session.
func (o *Owner) List(current string) []SessionView {
	o.mu.Lock()
	defer o.mu.Unlock()
	var out []SessionView
	for _, s := range o.listLocked() {
		out = append(out, SessionView{ID: PublicID(s.ID), Label: s.Label, CreatedAt: s.CreatedAt, LastSeenAt: s.LastSeenAt, Current: s.ID == current})
	}
	return out
}

// Revoke signs a browser out (by its full id or public id).
func (o *Owner) Revoke(ctx context.Context, id string) error {
	o.mu.Lock()
	var found *memory.OwnerSession
	for full, s := range o.byID {
		if full == id || (len(id) == 16 && PublicID(full) == id) {
			found = s
			break
		}
	}
	if found != nil {
		delete(o.byID, found.ID)
		delete(o.byAPI, found.APIHash)
	}
	o.mu.Unlock()
	if found == nil {
		return memory.ErrNotFound
	}
	return o.store.DeleteOwnerSession(ctx, found.ID)
}

// truncateLabel keeps a readable browser description (from User-Agent).
func truncateLabel(ua string) string {
	label := "a browser"
	for _, b := range []struct{ needle, name string }{
		{"Edg/", "Edge"}, {"OPR/", "Opera"}, {"Firefox/", "Firefox"}, {"Chrome/", "Chrome"}, {"Safari/", "Safari"},
	} {
		if strings.Contains(ua, b.needle) {
			label = b.name
			break
		}
	}
	for _, p := range []struct{ needle, name string }{
		{"iPhone", "iPhone"}, {"iPad", "iPad"}, {"Android", "Android"}, {"Mac OS X", "macOS"}, {"Windows", "Windows"}, {"Linux", "Linux"},
	} {
		if strings.Contains(ua, p.needle) {
			return label + " on " + p.name
		}
	}
	return label
}
