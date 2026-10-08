package runtime

import (
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
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
	ended map[string]chan struct{}        // closed when a session ends

	// Password sign-in (SEED_OWNER_USER / SEED_OWNER_PASSWORD, passed at
	// start like any secret). Only keyed hashes are kept.
	credKey      []byte
	userMAC      []byte
	passMAC      []byte
	passwordNote string                 // why password sign-in is off, if it was asked for
	failures     map[string][]time.Time // client → recent failed attempts
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
	o := &Owner{store: store, now: time.Now, byID: map[string]*memory.OwnerSession{}, byAPI: map[string]string{}, codes: map[string]time.Time{}, ended: map[string]chan struct{}{}, failures: map[string][]time.Time{}}
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
	return o.newSession(ctx, label)
}

func (o *Owner) newSession(ctx context.Context, label string) (string, error) {
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
		o.endLocked(s)
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
		o.endLocked(found)
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

// endLocked forgets a session and closes its open streams.
func (o *Owner) endLocked(s *memory.OwnerSession) {
	delete(o.byID, s.ID)
	delete(o.byAPI, s.APIHash)
	if ch, ok := o.ended[s.ID]; ok {
		close(ch)
		delete(o.ended, s.ID)
	}
}

// Ended returns a channel closed when the session ends (immediately, if it
// already has).
func (o *Owner) Ended(id string) <-chan struct{} {
	o.mu.Lock()
	defer o.mu.Unlock()
	if _, ok := o.byID[id]; !ok {
		ch := make(chan struct{})
		close(ch)
		return ch
	}
	ch, ok := o.ended[id]
	if !ok {
		ch = make(chan struct{})
		o.ended[id] = ch
	}
	return ch
}

// Valid reports whether a session is still live (without renewing it).
func (o *Owner) Valid(id string) bool {
	o.mu.Lock()
	defer o.mu.Unlock()
	s, ok := o.byID[id]
	if ok && o.now().After(s.ExpiresAt) {
		o.endLocked(s)
		return false
	}
	return ok
}

// ---- password sign-in

const (
	MinPasswordLen   = 12
	DefaultOwnerUser = "owner"
	failureWindow    = 15 * time.Minute
	maxClientFails   = 5 // per client, per window
	// Everyone together, per window: a ceiling on distributed guessing. Tripping
	// it takes many networks (IPv6 counts per /48), and while it holds, signed-in
	// browsers and `seed login` links still work.
	maxGlobalFails    = 1000
	maxTrackedClients = 10000 // bounds memory under address rotation
	globalFailureKey  = "*"
)

var (
	ErrBadPassword  = errors.New("wrong username or password")
	ErrTooManyTries = errors.New("too many failed sign-ins; wait a few minutes and try again")
)

// SetPassword turns on password sign-in. An empty password leaves it off.
func (o *Owner) SetPassword(user, password string) error {
	o.mu.Lock()
	defer o.mu.Unlock()
	o.userMAC, o.passMAC, o.passwordNote = nil, nil, ""
	if password == "" {
		return nil
	}
	if len([]rune(password)) < MinPasswordLen {
		o.passwordNote = fmt.Sprintf("SEED_OWNER_PASSWORD must be at least %d characters", MinPasswordLen)
		return errors.New(o.passwordNote)
	}
	if user == "" {
		user = DefaultOwnerUser
	}
	o.credKey = []byte(randomHex(32))
	o.userMAC, o.passMAC = o.mac("user", user), o.mac("pass", password)
	return nil
}

func (o *Owner) mac(kind, v string) []byte {
	m := hmac.New(sha256.New, o.credKey)
	m.Write([]byte(kind + "\x00" + v))
	return m.Sum(nil)
}

// PasswordEnabled reports whether password sign-in is on, and if it was
// asked for but is off, why.
func (o *Owner) PasswordEnabled() (bool, string) {
	o.mu.Lock()
	defer o.mu.Unlock()
	return o.passMAC != nil, o.passwordNote
}

// SignIn checks a username and password and starts a session. client
// identifies the caller for rate limiting.
func (o *Owner) SignIn(ctx context.Context, user, password, client, label string) (string, error) {
	o.mu.Lock()
	if o.passMAC == nil {
		o.mu.Unlock()
		return "", errors.New("password sign-in is off")
	}
	now := o.now()
	for key, ts := range o.failures {
		kept := ts[:0]
		for _, t := range ts {
			if now.Sub(t) < failureWindow {
				kept = append(kept, t)
			}
		}
		if len(kept) == 0 {
			delete(o.failures, key)
		} else {
			o.failures[key] = kept
		}
	}
	// While limited, nothing is checked: no answer to learn from.
	if len(o.failures[client]) >= maxClientFails || len(o.failures[globalFailureKey]) >= maxGlobalFails {
		o.mu.Unlock()
		return "", ErrTooManyTries
	}
	userOK := hmac.Equal(o.mac("user", user), o.userMAC)
	passOK := hmac.Equal(o.mac("pass", password), o.passMAC)
	if !userOK || !passOK {
		if _, known := o.failures[client]; !known && len(o.failures) >= maxTrackedClients {
			client = "overflow" // too many distinct clients: they share one budget
		}
		o.failures[client] = append(o.failures[client], now)
		o.failures[globalFailureKey] = append(o.failures[globalFailureKey], now)
		o.mu.Unlock()
		return "", ErrBadPassword
	}
	delete(o.failures, client)
	o.mu.Unlock()
	return o.newSession(ctx, label)
}
