// Package webpush implements just enough of the Web Push protocol for Pastel to
// deliver "deal on your watch" notifications to a browser without a Matrix DM.
//
// It is a small, dependency-free implementation of:
//   - VAPID (RFC 8292): an ES256 JWT signed with a long-lived P-256 key that
//     identifies Pastel to the browser's push service.
//   - Message encryption (RFC 8291) using the "aes128gcm" content encoding
//     (RFC 8188): an ephemeral ECDH per message, an HKDF key schedule keyed by
//     the subscription's auth secret, and a single AES-128-GCM record.
//
// Only the primitives in the standard library (crypto/ecdh, crypto/ecdsa,
// crypto/aes) plus golang.org/x/crypto/hkdf — already in the module graph — are
// used, so there is no new dependency and the package builds offline.
package webpush

import (
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/ecdh"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"io"
	"math/big"
	"net/http"
	"net/url"
	"strings"
	"time"

	"golang.org/x/crypto/hkdf"
)

// b64 is the URL-safe, unpadded base64 alphabet the Web Push specs use
// everywhere (keys, salt, JWT segments).
var b64 = base64.RawURLEncoding

// Subscription is a browser PushSubscription as posted by the service worker:
// the push service Endpoint plus the client's public key (P256dh, an
// uncompressed P-256 point) and Auth secret (16 bytes), both base64url.
type Subscription struct {
	Endpoint string
	P256dh   string
	Auth     string
}

// Sender signs and encrypts Web Push messages with a fixed VAPID identity.
type Sender struct {
	priv    *ecdsa.PrivateKey // VAPID signing key (P-256)
	pubB64  string            // VAPID public key, base64url uncompressed point
	subject string            // VAPID "sub" contact, e.g. "mailto:admin@host"
	client  *http.Client
}

// GenerateVAPIDKeys returns a fresh VAPID keypair as base64url strings: the
// private value is the 32-byte P-256 scalar; the public value is the 65-byte
// uncompressed point that the browser uses as its applicationServerKey.
func GenerateVAPIDKeys() (privB64, pubB64 string, err error) {
	priv, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return "", "", err
	}
	pub, err := priv.PublicKey.ECDH()
	if err != nil {
		return "", "", err
	}
	d := make([]byte, 32)
	priv.D.FillBytes(d)
	return b64.EncodeToString(d), b64.EncodeToString(pub.Bytes()), nil
}

// NewSender reconstructs a Sender from a stored base64url VAPID private scalar
// and the contact subject (a mailto: or https: URI required by RFC 8292).
func NewSender(privB64, subject string) (*Sender, error) {
	d, err := b64.DecodeString(privB64)
	if err != nil {
		return nil, fmt.Errorf("webpush: bad VAPID private key: %w", err)
	}
	curve := elliptic.P256()
	priv := &ecdsa.PrivateKey{D: new(big.Int).SetBytes(d)}
	priv.PublicKey.Curve = curve
	priv.PublicKey.X, priv.PublicKey.Y = curve.ScalarBaseMult(d)
	if priv.PublicKey.X == nil {
		return nil, fmt.Errorf("webpush: invalid VAPID private key")
	}
	pub, err := priv.PublicKey.ECDH()
	if err != nil {
		return nil, err
	}
	if subject == "" {
		subject = "mailto:admin@localhost"
	}
	return &Sender{
		priv:    priv,
		pubB64:  b64.EncodeToString(pub.Bytes()),
		subject: subject,
		client:  &http.Client{Timeout: 15 * time.Second},
	}, nil
}

// PublicKey returns the VAPID application server key (base64url uncompressed
// P-256 point) that the frontend passes to pushManager.subscribe().
func (s *Sender) PublicKey() string { return s.pubB64 }

// Send encrypts payload for the subscription and POSTs it to the push service,
// returning the HTTP status. A 201 is success; use Gone(status) to detect a
// dead subscription (404/410) that the caller should delete.
func (s *Sender) Send(ctx context.Context, sub Subscription, payload []byte) (int, error) {
	uaPub, err := b64.DecodeString(sub.P256dh)
	if err != nil {
		return 0, fmt.Errorf("webpush: bad p256dh: %w", err)
	}
	auth, err := b64.DecodeString(sub.Auth)
	if err != nil {
		return 0, fmt.Errorf("webpush: bad auth: %w", err)
	}

	body, err := encrypt(uaPub, auth, payload, nil, nil)
	if err != nil {
		return 0, err
	}

	auth0, err := s.vapidAuth(sub.Endpoint)
	if err != nil {
		return 0, err
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, sub.Endpoint, strings.NewReader(string(body)))
	if err != nil {
		return 0, err
	}
	req.Header.Set("Content-Encoding", "aes128gcm")
	req.Header.Set("Content-Type", "application/octet-stream")
	req.Header.Set("TTL", "86400")
	req.Header.Set("Urgency", "normal")
	req.Header.Set("Authorization", auth0)

	resp, err := s.client.Do(req)
	if err != nil {
		return 0, err
	}
	defer resp.Body.Close()
	_, _ = io.Copy(io.Discard, resp.Body)
	return resp.StatusCode, nil
}

// Gone reports whether a push status means the subscription no longer exists and
// should be pruned (per RFC 8030 the push service returns 404 or 410).
func Gone(status int) bool { return status == http.StatusNotFound || status == http.StatusGone }

// vapidAuth builds the "Authorization: vapid t=<jwt>, k=<pubkey>" header for the
// push service that hosts endpoint (RFC 8292).
func (s *Sender) vapidAuth(endpoint string) (string, error) {
	u, err := url.Parse(endpoint)
	if err != nil || u.Host == "" {
		return "", fmt.Errorf("webpush: bad endpoint %q", endpoint)
	}
	aud := u.Scheme + "://" + u.Host
	jwt, err := s.signJWT(aud, time.Now().Add(12*time.Hour))
	if err != nil {
		return "", err
	}
	return "vapid t=" + jwt + ", k=" + s.pubB64, nil
}

// signJWT produces a compact ES256 JWT with the VAPID claims (aud/exp/sub).
func (s *Sender) signJWT(aud string, exp time.Time) (string, error) {
	header := b64.EncodeToString([]byte(`{"typ":"JWT","alg":"ES256"}`))
	claims, err := json.Marshal(map[string]any{
		"aud": aud,
		"exp": exp.Unix(),
		"sub": s.subject,
	})
	if err != nil {
		return "", err
	}
	signingInput := header + "." + b64.EncodeToString(claims)

	digest := sha256.Sum256([]byte(signingInput))
	r, ss, err := ecdsa.Sign(rand.Reader, s.priv, digest[:])
	if err != nil {
		return "", err
	}
	// JOSE encodes the signature as the raw R||S, each left-padded to 32 bytes —
	// not the ASN.1 DER that ecdsa.Sign conceptually represents.
	sig := make([]byte, 64)
	r.FillBytes(sig[:32])
	ss.FillBytes(sig[32:])
	return signingInput + "." + b64.EncodeToString(sig), nil
}

// encrypt produces an RFC 8188 "aes128gcm" message body encrypting plaintext for
// the subscription whose public key is uaPub and auth secret is authSecret.
//
// asEphemeral and salt are injectable only so tests can pin the RFC 8291 vector;
// production passes nil for both and fresh randomness is generated.
func encrypt(uaPub, authSecret, plaintext []byte, asEphemeral *ecdh.PrivateKey, salt []byte) ([]byte, error) {
	curve := ecdh.P256()
	uaKey, err := curve.NewPublicKey(uaPub)
	if err != nil {
		return nil, fmt.Errorf("webpush: bad subscription key: %w", err)
	}
	if asEphemeral == nil {
		if asEphemeral, err = curve.GenerateKey(rand.Reader); err != nil {
			return nil, err
		}
	}
	asPub := asEphemeral.PublicKey().Bytes() // 65-byte uncompressed point

	if salt == nil {
		salt = make([]byte, 16)
		if _, err = io.ReadFull(rand.Reader, salt); err != nil {
			return nil, err
		}
	}

	// 1. ECDH, then combine with the auth secret (RFC 8291 §3.4). key_info binds
	//    both parties' public keys so a key can't be reused across recipients.
	shared, err := asEphemeral.ECDH(uaKey)
	if err != nil {
		return nil, err
	}
	keyInfo := append([]byte("WebPush: info\x00"), uaPub...)
	keyInfo = append(keyInfo, asPub...)
	ikm := make([]byte, 32)
	if _, err = io.ReadFull(hkdf.New(sha256.New, shared, authSecret, keyInfo), ikm); err != nil {
		return nil, err
	}

	// 2. Derive the content-encryption key and nonce from the IKM and salt
	//    (RFC 8188 §2.2). The info strings are fixed by the spec.
	cek := make([]byte, 16)
	if _, err = io.ReadFull(hkdf.New(sha256.New, ikm, salt, []byte("Content-Encoding: aes128gcm\x00")), cek); err != nil {
		return nil, err
	}
	nonce := make([]byte, 12)
	if _, err = io.ReadFull(hkdf.New(sha256.New, ikm, salt, []byte("Content-Encoding: nonce\x00")), nonce); err != nil {
		return nil, err
	}

	// 3. A single AES-128-GCM record. The plaintext is followed by a 0x02
	//    delimiter marking the last (here only) record before encryption.
	block, err := aes.NewCipher(cek)
	if err != nil {
		return nil, err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}
	record := append(append([]byte{}, plaintext...), 0x02)
	ciphertext := gcm.Seal(nil, nonce, record, nil)

	// 4. Prepend the aes128gcm header: salt(16) || rs(4) || idlen(1) || keyid.
	//    keyid is the server's ephemeral public key, so the receiver can complete
	//    the ECDH; rs is the record size (4096 is the conventional default).
	header := make([]byte, 0, 16+4+1+len(asPub)+len(ciphertext))
	header = append(header, salt...)
	header = binary.BigEndian.AppendUint32(header, 4096)
	header = append(header, byte(len(asPub)))
	header = append(header, asPub...)
	return append(header, ciphertext...), nil
}
