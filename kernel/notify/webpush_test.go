package notify

import (
	"bytes"
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/ecdh"
	"crypto/hkdf"
	"crypto/sha256"
	"encoding/binary"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"
)

// decrypt is the browser's side of RFC 8291.
func decrypt(t *testing.T, ua *ecdh.PrivateKey, auth, body []byte) []byte {
	t.Helper()
	salt, rs, idlen := body[:16], binary.BigEndian.Uint32(body[16:20]), int(body[20])
	if rs != 4096 || idlen != 65 {
		t.Fatalf("header: rs %d idlen %d", rs, idlen)
	}
	asPub, err := ecdh.P256().NewPublicKey(body[21 : 21+idlen])
	if err != nil {
		t.Fatal(err)
	}
	shared, _ := ua.ECDH(asPub)
	info := append(append([]byte("WebPush: info\x00"), ua.PublicKey().Bytes()...), asPub.Bytes()...)
	ikm, _ := hkdf.Key(sha256.New, shared, auth, string(info), 32)
	cek, _ := hkdf.Key(sha256.New, ikm, salt, "Content-Encoding: aes128gcm\x00", 16)
	nonce, _ := hkdf.Key(sha256.New, ikm, salt, "Content-Encoding: nonce\x00", 12)
	block, _ := aes.NewCipher(cek)
	gcm, _ := cipher.NewGCM(block)
	plain, err := gcm.Open(nil, nonce, body[21+idlen:], nil)
	if err != nil {
		t.Fatal(err)
	}
	if plain[len(plain)-1] != 2 {
		t.Fatal("missing last-record delimiter")
	}
	return plain[:len(plain)-1]
}

// RFC 8291, Appendix A.
func TestEncryptRFC8291Example(t *testing.T) {
	d := func(s string) []byte {
		b, err := b64.DecodeString(s)
		if err != nil {
			t.Fatal(err)
		}
		return b
	}
	as, err := ecdh.P256().NewPrivateKey(d("yfWPiYE-n46HLnH0KqZOF1fJJU3MYrct3AELtAQ-oRw"))
	if err != nil {
		t.Fatal(err)
	}
	sub := Subscription{P256dh: "BCVxsr7N_eNgVRqvHtD0zTZsEc6-VV-JvLexhqUzORcxaOzi6-AYWXvTBHm4bjyPjs7Vd8pZGH6SRpkNtoIAiw4", Auth: "BTBZMqHH6r4Tts7J_aSIgg"}
	got, err := encrypt(sub, []byte("When I grow up, I want to be a watermelon"), as, d("DGv6ra1nlYgDCS1FRnbzlw"))
	if err != nil {
		t.Fatal(err)
	}
	want := "DGv6ra1nlYgDCS1FRnbzlwAAEABBBP4z9KsN6nGRTbVYI_c7VJSPQTBtkgcy27mlmlMoZIIgDll6e3vCYLocInmYWAmS6TlzAC8wEqKK6PBru3jl7A_yl95bQpu6cVPTpK4Mqgkf1CXztLVBSt2Ks3oZwbuwXPXLWyouBWLVWGNWQexSgSxsj_Qulcy4a-fN"
	if b64.EncodeToString(got) != want {
		t.Fatalf("encrypted:\n got %s\nwant %s", b64.EncodeToString(got), want)
	}
	ua, _ := ecdh.P256().NewPrivateKey(d("q1dXpw3UpT5VOmu_cf_v6ih07Aems3njxI-JWgLcM94"))
	if p := decrypt(t, ua, d(sub.Auth), got); string(p) != "When I grow up, I want to be a watermelon" {
		t.Fatalf("decrypted %q", p)
	}
}

type captureRT struct {
	req    *http.Request
	body   []byte
	status int
}

func (c *captureRT) RoundTrip(r *http.Request) (*http.Response, error) {
	c.req = r
	c.body, _ = io.ReadAll(r.Body)
	return &http.Response{StatusCode: c.status, Body: io.NopCloser(strings.NewReader("")), Header: http.Header{}, Request: r}, nil
}

func TestSendSignsAndEncrypts(t *testing.T) {
	v, _ := NewVAPID()
	again, err := LoadVAPID(v.Encode())
	if err != nil || again.PublicKey() != v.PublicKey() {
		t.Fatal("a saved key loads back")
	}
	ua, _ := ecdh.P256().GenerateKey(nil)
	auth := []byte("0123456789abcdef")
	sub := Subscription{Endpoint: "https://fcm.googleapis.com/fcm/send/abc", P256dh: b64.EncodeToString(ua.PublicKey().Bytes()), Auth: b64.EncodeToString(auth)}
	rt := &captureRT{status: 201}
	s := &Sender{VAPID: v, Subject: "mailto:me@example.com", Client: &http.Client{Transport: rt}}
	if err := s.Send(context.Background(), sub, Message{Title: "Ready to try", URL: "/_seed/evolutions/x"}, "high"); err != nil {
		t.Fatal(err)
	}
	h := rt.req.Header
	if h.Get("Content-Encoding") != "aes128gcm" || h.Get("TTL") == "" || h.Get("Urgency") != "high" {
		t.Fatalf("headers: %v", h)
	}
	// The VAPID token: signed by my key, for this push service, expiring.
	authz := h.Get("Authorization")
	if !strings.HasPrefix(authz, "vapid t=") || !strings.HasSuffix(authz, ", k="+v.PublicKey()) {
		t.Fatalf("authorization: %s", authz)
	}
	jwt := strings.TrimSuffix(strings.TrimPrefix(authz, "vapid t="), ", k="+v.PublicKey())
	parts := strings.Split(jwt, ".")
	sig, _ := b64.DecodeString(parts[2])
	if !verifyES256(&v.priv.PublicKey, parts[0]+"."+parts[1], sig) {
		t.Fatal("VAPID signature doesn't verify")
	}
	var claims struct {
		Aud string `json:"aud"`
		Exp int64  `json:"exp"`
		Sub string `json:"sub"`
	}
	cb, _ := b64.DecodeString(parts[1])
	_ = json.Unmarshal(cb, &claims)
	if claims.Aud != "https://fcm.googleapis.com" || claims.Sub != "mailto:me@example.com" || claims.Exp < time.Now().Unix() || claims.Exp > time.Now().Add(24*time.Hour).Unix() {
		t.Fatalf("claims: %+v", claims)
	}
	var m Message
	if err := json.Unmarshal(decrypt(t, ua, auth, rt.body), &m); err != nil || m.Title != "Ready to try" || m.URL != "/_seed/evolutions/x" {
		t.Fatalf("payload: %+v %v", m, err)
	}
	if !bytes.Contains(rt.body[:21], []byte{0, 0, 16, 0}) {
		t.Fatal("record size 4096")
	}

	rt.status = 410
	if err := s.Send(context.Background(), sub, Message{Title: "x"}, ""); err != ErrGone {
		t.Fatalf("a gone subscription: %v", err)
	}
}

func TestOnlyPushServices(t *testing.T) {
	ua, _ := ecdh.P256().GenerateKey(nil)
	key, auth := b64.EncodeToString(ua.PublicKey().Bytes()), b64.EncodeToString([]byte("0123456789abcdef"))
	for _, ok := range []string{
		"https://fcm.googleapis.com/fcm/send/x",
		"https://updates.push.services.mozilla.com/wpush/v2/x",
		"https://web.push.apple.com/QX",
		"https://wns2-par02p.notify.windows.com/w/?token=x",
	} {
		if err := CheckSubscription(Subscription{Endpoint: ok, P256dh: key, Auth: auth}); err != nil {
			t.Errorf("%s: %v", ok, err)
		}
	}
	for _, bad := range []string{
		"http://fcm.googleapis.com/x",
		"https://fcm.googleapis.com.evil.example/x",
		"https://evil.example/fcm.googleapis.com",
		"https://fcm.googleapis.com:8443/x",
		"https://user@fcm.googleapis.com/x",
		"https://localhost/x",
		"https://169.254.169.254/latest",
		"https://notify.windows.com.evil/x",
	} {
		if err := CheckSubscription(Subscription{Endpoint: bad, P256dh: key, Auth: auth}); err == nil {
			t.Errorf("%s: accepted", bad)
		}
	}
	if CheckSubscription(Subscription{Endpoint: "https://fcm.googleapis.com/x", P256dh: "AAAA", Auth: auth}) == nil {
		t.Error("a malformed key is refused")
	}
}
