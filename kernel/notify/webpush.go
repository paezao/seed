// Package notify sends Web Push notifications to my owner's browsers
// (RFC 8030 delivery, RFC 8291 message encryption, RFC 8292 VAPID), with
// nothing but the standard library.
package notify

import (
	"bytes"
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/ecdh"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/hkdf"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math/big"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

var b64 = base64.RawURLEncoding

// VAPID identifies me to push services: they only deliver what I sign to
// subscriptions made with my public key.
type VAPID struct {
	priv *ecdsa.PrivateKey
}

// NewVAPID makes a new key pair.
func NewVAPID() (*VAPID, error) {
	k, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return nil, err
	}
	return &VAPID{priv: k}, nil
}

// LoadVAPID reads a key saved with Encode.
func LoadVAPID(s string) (*VAPID, error) {
	d, err := b64.DecodeString(s)
	if err != nil || len(d) != 32 {
		return nil, errors.New("malformed VAPID key")
	}
	k, err := ecdsa.ParseRawPrivateKey(elliptic.P256(), d)
	if err != nil {
		return nil, err
	}
	return &VAPID{priv: k}, nil
}

// Encode is the private key, for keeping.
func (v *VAPID) Encode() string {
	b, _ := v.priv.Bytes()
	return b64.EncodeToString(b)
}

// PublicKey is what browsers subscribe with (the applicationServerKey).
func (v *VAPID) PublicKey() string {
	b, _ := v.priv.PublicKey.Bytes()
	return b64.EncodeToString(b)
}

// authorization is the VAPID header for a push to endpoint.
func (v *VAPID) authorization(endpoint, subject string, now time.Time) (string, error) {
	u, err := url.Parse(endpoint)
	if err != nil {
		return "", err
	}
	header := b64.EncodeToString([]byte(`{"typ":"JWT","alg":"ES256"}`))
	claims, _ := json.Marshal(map[string]any{"aud": u.Scheme + "://" + u.Host, "exp": now.Add(12 * time.Hour).Unix(), "sub": subject})
	signing := header + "." + b64.EncodeToString(claims)
	sum := sha256.Sum256([]byte(signing))
	r, s, err := ecdsa.Sign(rand.Reader, v.priv, sum[:])
	if err != nil {
		return "", err
	}
	sig := make([]byte, 64)
	r.FillBytes(sig[:32])
	s.FillBytes(sig[32:])
	return "vapid t=" + signing + "." + b64.EncodeToString(sig) + ", k=" + v.PublicKey(), nil
}

// Subscription is a browser's push subscription.
type Subscription struct {
	Endpoint string `json:"endpoint"`
	P256dh   string `json:"p256dh"`
	Auth     string `json:"auth"`
}

// pushHosts are the push services browsers use. I only ever send to them:
// an endpoint is a URL a browser hands me, and I won't post to anything else.
var pushHosts = []string{
	"fcm.googleapis.com",                // Chrome, Edge on Android, Opera, Brave…
	"updates.push.services.mozilla.com", // Firefox
	".push.apple.com",                   // Safari
	".notify.windows.com",               // Edge on Windows
}

// CheckSubscription refuses subscriptions I wouldn't deliver to.
func CheckSubscription(s Subscription) error {
	u, err := url.Parse(s.Endpoint)
	if err != nil || u.Scheme != "https" || u.User != nil || u.Port() != "" {
		return errors.New("a push endpoint must be a plain https URL")
	}
	host := strings.ToLower(u.Hostname())
	known := false
	for _, h := range pushHosts {
		if host == strings.TrimPrefix(h, ".") || (strings.HasPrefix(h, ".") && strings.HasSuffix(host, h)) {
			known = true
		}
	}
	if !known {
		return fmt.Errorf("%s isn't a push service I know", host)
	}
	if p, err := b64.DecodeString(strings.TrimRight(s.P256dh, "=")); err != nil || len(p) != 65 || p[0] != 4 {
		return errors.New("malformed subscription key")
	}
	if a, err := b64.DecodeString(strings.TrimRight(s.Auth, "=")); err != nil || len(a) != 16 {
		return errors.New("malformed subscription secret")
	}
	return nil
}

// Encrypt encrypts a payload for a subscription (RFC 8291, aes128gcm).
func Encrypt(s Subscription, payload []byte) ([]byte, error) {
	as, err := ecdh.P256().GenerateKey(rand.Reader)
	if err != nil {
		return nil, err
	}
	salt := make([]byte, 16)
	if _, err := rand.Read(salt); err != nil {
		return nil, err
	}
	return encrypt(s, payload, as, salt)
}

func encrypt(s Subscription, payload []byte, as *ecdh.PrivateKey, salt []byte) ([]byte, error) {
	uaRaw, err := b64.DecodeString(strings.TrimRight(s.P256dh, "="))
	if err != nil {
		return nil, err
	}
	auth, err := b64.DecodeString(strings.TrimRight(s.Auth, "="))
	if err != nil {
		return nil, err
	}
	ua, err := ecdh.P256().NewPublicKey(uaRaw)
	if err != nil {
		return nil, err
	}
	shared, err := as.ECDH(ua)
	if err != nil {
		return nil, err
	}
	asPub := as.PublicKey().Bytes()
	// IKM = HKDF(auth, shared, "WebPush: info" || 0 || ua || as, 32)
	info := append(append([]byte("WebPush: info\x00"), uaRaw...), asPub...)
	ikm, err := hkdf.Key(sha256.New, shared, auth, string(info), 32)
	if err != nil {
		return nil, err
	}
	cek, err := hkdf.Key(sha256.New, ikm, salt, "Content-Encoding: aes128gcm\x00", 16)
	if err != nil {
		return nil, err
	}
	nonce, err := hkdf.Key(sha256.New, ikm, salt, "Content-Encoding: nonce\x00", 12)
	if err != nil {
		return nil, err
	}
	block, err := aes.NewCipher(cek)
	if err != nil {
		return nil, err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}
	// One record: the payload, then the last-record delimiter.
	plain := append(append([]byte{}, payload...), 2)
	var out bytes.Buffer
	out.Write(salt)
	_ = binary.Write(&out, binary.BigEndian, uint32(4096))
	out.WriteByte(byte(len(asPub)))
	out.Write(asPub)
	out.Write(gcm.Seal(nil, nonce, plain, nil))
	return out.Bytes(), nil
}

// ErrGone means the subscription no longer exists (the browser dropped it).
var ErrGone = errors.New("subscription gone")

// Sender delivers notifications.
type Sender struct {
	VAPID   *VAPID
	Subject string // mailto: or https: contact, required by some services
	Client  *http.Client
}

// Message is what my service worker shows.
type Message struct {
	Title string `json:"title"`
	Body  string `json:"body,omitempty"`
	URL   string `json:"url,omitempty"` // a control-plane path
	Tag   string `json:"tag,omitempty"` // replaces an earlier notification with the same tag
}

// Send delivers a message to one subscription.
func (s *Sender) Send(ctx context.Context, sub Subscription, m Message, urgency string) error {
	if err := CheckSubscription(sub); err != nil {
		return err
	}
	payload, _ := json.Marshal(m)
	body, err := Encrypt(sub, payload)
	if err != nil {
		return err
	}
	auth, err := s.VAPID.authorization(sub.Endpoint, s.Subject, time.Now())
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, sub.Endpoint, bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/octet-stream")
	req.Header.Set("Content-Encoding", "aes128gcm")
	req.Header.Set("TTL", strconv.Itoa(24*3600))
	req.Header.Set("Authorization", auth)
	if urgency != "" {
		req.Header.Set("Urgency", urgency)
	}
	client := s.Client
	if client == nil {
		client = &http.Client{Timeout: 30 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	}
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	msg, _ := io.ReadAll(io.LimitReader(resp.Body, 2048))
	switch {
	case resp.StatusCode == http.StatusNotFound || resp.StatusCode == http.StatusGone:
		return ErrGone
	case resp.StatusCode/100 != 2:
		return fmt.Errorf("push service answered %d: %s", resp.StatusCode, strings.TrimSpace(string(msg)))
	}
	return nil
}

// verifyES256 checks a VAPID signature (for tests).
func verifyES256(pub *ecdsa.PublicKey, signing string, sig []byte) bool {
	if len(sig) != 64 {
		return false
	}
	sum := sha256.Sum256([]byte(signing))
	r, s := new(big.Int).SetBytes(sig[:32]), new(big.Int).SetBytes(sig[32:])
	return ecdsa.Verify(pub, sum[:], r, s)
}
