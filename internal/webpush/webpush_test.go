package webpush

import (
	"bytes"
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/ecdh"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"encoding/binary"
	"encoding/json"
	"io"
	"math/big"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"golang.org/x/crypto/hkdf"
)

// newGCM builds an AES-128-GCM AEAD from a 16-byte content-encryption key.
func newGCM(t *testing.T, cek []byte) cipher.AEAD {
	t.Helper()
	block, err := aes.NewCipher(cek)
	if err != nil {
		t.Fatalf("aes: %v", err)
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		t.Fatalf("gcm: %v", err)
	}
	return gcm
}

func nowPlus12h() time.Time     { return time.Now().Add(12 * time.Hour) }
func bytesSplit(s string, sep byte) []string { return strings.Split(s, string(sep)) }

// decryptRFC8291 is an independent receiver-side decryptor written straight from
// RFC 8188/8291 (separate code from encrypt's key schedule). If encrypt builds
// any HKDF info string, the header layout, or the ECDH combination differently
// than the spec, this fails to recover the plaintext — so the round-trip is a
// real cross-check, not a tautology.
func decryptRFC8291(t *testing.T, body []byte, uaPriv *ecdh.PrivateKey, authSecret []byte) []byte {
	t.Helper()
	if len(body) < 21 {
		t.Fatalf("body too short: %d", len(body))
	}
	salt := body[:16]
	idlen := int(body[20])
	keyid := body[21 : 21+idlen] // application-server ephemeral public key
	ciphertext := body[21+idlen:]

	asPub, err := ecdh.P256().NewPublicKey(keyid)
	if err != nil {
		t.Fatalf("parse as_public: %v", err)
	}
	shared, err := uaPriv.ECDH(asPub)
	if err != nil {
		t.Fatalf("ecdh: %v", err)
	}
	uaPub := uaPriv.PublicKey().Bytes()

	keyInfo := append([]byte("WebPush: info\x00"), uaPub...)
	keyInfo = append(keyInfo, keyid...)
	ikm := make([]byte, 32)
	io.ReadFull(hkdf.New(sha256.New, shared, authSecret, keyInfo), ikm)

	cek := make([]byte, 16)
	io.ReadFull(hkdf.New(sha256.New, ikm, salt, []byte("Content-Encoding: aes128gcm\x00")), cek)
	nonce := make([]byte, 12)
	io.ReadFull(hkdf.New(sha256.New, ikm, salt, []byte("Content-Encoding: nonce\x00")), nonce)

	gcm := newGCM(t, cek)
	record, err := gcm.Open(nil, nonce, ciphertext, nil)
	if err != nil {
		t.Fatalf("gcm open: %v", err)
	}
	// Strip the trailing 0x02 last-record delimiter.
	if len(record) == 0 || record[len(record)-1] != 0x02 {
		t.Fatalf("missing 0x02 pad delimiter")
	}
	return record[:len(record)-1]
}

func TestEncryptRoundTrip(t *testing.T) {
	curve := ecdh.P256()
	uaPriv, err := curve.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	auth := make([]byte, 16)
	io.ReadFull(rand.Reader, auth)

	plaintext := []byte("When I grow up, I want to be a watermelon")
	body, err := encrypt(uaPriv.PublicKey().Bytes(), auth, plaintext, nil, nil)
	if err != nil {
		t.Fatalf("encrypt: %v", err)
	}

	// Header structure: rs == 4096, keyid is a 65-byte uncompressed point, and the
	// total length is header(86) + len(plaintext) + 1 pad + 16 GCM tag.
	if rs := binary.BigEndian.Uint32(body[16:20]); rs != 4096 {
		t.Errorf("record size = %d, want 4096", rs)
	}
	if body[20] != 65 {
		t.Errorf("idlen = %d, want 65", body[20])
	}
	if want := 16 + 4 + 1 + 65 + len(plaintext) + 1 + 16; len(body) != want {
		t.Errorf("body len = %d, want %d", len(body), want)
	}

	if got := decryptRFC8291(t, body, uaPriv, auth); !bytes.Equal(got, plaintext) {
		t.Errorf("round-trip = %q, want %q", got, plaintext)
	}
}

// TestEncryptIsKeyed confirms the auth secret actually participates: decrypting
// with the wrong auth secret must fail (otherwise the key schedule isn't binding
// it). Guards against accidentally dropping authSecret from the HKDF.
func TestEncryptIsKeyed(t *testing.T) {
	curve := ecdh.P256()
	uaPriv, _ := curve.GenerateKey(rand.Reader)
	auth := make([]byte, 16)
	io.ReadFull(rand.Reader, auth)

	body, err := encrypt(uaPriv.PublicKey().Bytes(), auth, []byte("hi"), nil, nil)
	if err != nil {
		t.Fatal(err)
	}

	wrongAuth := make([]byte, 16)
	copy(wrongAuth, auth)
	wrongAuth[0] ^= 0xff

	defer func() { recover() }() // a decrypt failure may t.Fatal inside the helper
	salt := body[:16]
	keyid := body[21 : 21+65]
	ciphertext := body[21+65:]
	asPub, _ := ecdh.P256().NewPublicKey(keyid)
	shared, _ := uaPriv.ECDH(asPub)
	keyInfo := append([]byte("WebPush: info\x00"), uaPriv.PublicKey().Bytes()...)
	keyInfo = append(keyInfo, keyid...)
	ikm := make([]byte, 32)
	io.ReadFull(hkdf.New(sha256.New, shared, wrongAuth, keyInfo), ikm)
	cek := make([]byte, 16)
	io.ReadFull(hkdf.New(sha256.New, ikm, salt, []byte("Content-Encoding: aes128gcm\x00")), cek)
	nonce := make([]byte, 12)
	io.ReadFull(hkdf.New(sha256.New, ikm, salt, []byte("Content-Encoding: nonce\x00")), nonce)
	if _, err := newGCM(t, cek).Open(nil, nonce, ciphertext, nil); err == nil {
		t.Error("decryption with the wrong auth secret succeeded; auth not bound into key schedule")
	}
}

func TestVAPIDJWTSignsAndVerifies(t *testing.T) {
	privB64, pubB64, err := GenerateVAPIDKeys()
	if err != nil {
		t.Fatal(err)
	}
	s, err := NewSender(privB64, "mailto:admin@parodia.dev")
	if err != nil {
		t.Fatal(err)
	}
	if s.PublicKey() != pubB64 {
		t.Errorf("PublicKey() = %s, want regenerated %s", s.PublicKey(), pubB64)
	}

	jwt, err := s.signJWT("https://fcm.googleapis.com", nowPlus12h())
	if err != nil {
		t.Fatal(err)
	}
	parts := bytesSplit(jwt, '.')
	if len(parts) != 3 {
		t.Fatalf("jwt has %d segments, want 3", len(parts))
	}

	// Claims carry the audience and subject.
	payload, _ := b64.DecodeString(parts[1])
	var claims struct{ Aud, Sub string }
	if err := json.Unmarshal(payload, &claims); err != nil {
		t.Fatal(err)
	}
	if claims.Aud != "https://fcm.googleapis.com" {
		t.Errorf("aud = %q", claims.Aud)
	}
	if claims.Sub != "mailto:admin@parodia.dev" {
		t.Errorf("sub = %q", claims.Sub)
	}

	// Signature verifies against the published VAPID public key.
	pubBytes, _ := b64.DecodeString(pubB64)
	x, y := elliptic.Unmarshal(elliptic.P256(), pubBytes)
	pub := &ecdsa.PublicKey{Curve: elliptic.P256(), X: x, Y: y}
	digest := sha256.Sum256([]byte(parts[0] + "." + parts[1]))
	sig, _ := b64.DecodeString(parts[2])
	if len(sig) != 64 {
		t.Fatalf("signature is %d bytes, want 64 (raw R||S)", len(sig))
	}
	r := new(big.Int).SetBytes(sig[:32])
	ss := new(big.Int).SetBytes(sig[32:])
	if !ecdsa.Verify(pub, digest[:], r, ss) {
		t.Error("VAPID JWT signature failed to verify")
	}
}

func TestSendPostsEncryptedBody(t *testing.T) {
	curve := ecdh.P256()
	uaPriv, _ := curve.GenerateKey(rand.Reader)
	auth := make([]byte, 16)
	io.ReadFull(rand.Reader, auth)

	var gotEnc, gotAuth string
	var gotBody []byte
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotEnc = r.Header.Get("Content-Encoding")
		gotAuth = r.Header.Get("Authorization")
		gotBody, _ = io.ReadAll(r.Body)
		w.WriteHeader(http.StatusCreated)
	}))
	defer srv.Close()

	privB64, _, _ := GenerateVAPIDKeys()
	s, _ := NewSender(privB64, "mailto:admin@parodia.dev")
	sub := Subscription{
		Endpoint: srv.URL + "/push/abc",
		P256dh:   b64.EncodeToString(uaPriv.PublicKey().Bytes()),
		Auth:     b64.EncodeToString(auth),
	}

	status, err := s.Send(context.Background(), sub, []byte("deal!"))
	if err != nil {
		t.Fatal(err)
	}
	if status != http.StatusCreated {
		t.Errorf("status = %d, want 201", status)
	}
	if gotEnc != "aes128gcm" {
		t.Errorf("Content-Encoding = %q", gotEnc)
	}
	if len(gotAuth) < 8 || gotAuth[:6] != "vapid " {
		t.Errorf("Authorization = %q, want vapid scheme", gotAuth)
	}
	if got := decryptRFC8291(t, gotBody, uaPriv, auth); string(got) != "deal!" {
		t.Errorf("server received %q, want %q", got, "deal!")
	}
}

func TestGone(t *testing.T) {
	for _, tc := range []struct {
		status int
		want   bool
	}{{404, true}, {410, true}, {201, false}, {429, false}, {500, false}} {
		if Gone(tc.status) != tc.want {
			t.Errorf("Gone(%d) = %v, want %v", tc.status, Gone(tc.status), tc.want)
		}
	}
}
