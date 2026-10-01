package push

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/url"
	"sync"
	"time"

	"patmonitor/internal/version"
)

// Subject is who the push services are told is sending, the `sub` of RFC 8292.
//
// **It is the project's address and not anybody's email.** The field is for a
// service that wants to reach whoever runs the sender, and the person running
// this one did not write it: their address would be handed to four companies
// for a question only the authors can answer. Apple refuses a `sub` it cannot
// reach — `mailto:admin@localhost` answers `BadJwtToken` — so it is a real
// URL, not a placeholder.
const Subject = "https://github.com/" + version.Repository

// tokenLife is how long a signature is good for, and tokenReuse how long one
// is handed out before a new one is made.
//
// RFC 8292 allows up to a day; twelve hours leaves room for a clock that is
// off, which is what web.dev recommends. **Apple asks not to be sent a new
// token more than once an hour**, so a token is reused and not made per
// message: a night of alerts is one signature per service, not one per cry.
const (
	tokenLife  = 12 * time.Hour
	tokenReuse = 6 * time.Hour
)

// signer holds the monitor's VAPID key and the tokens made with it.
type signer struct {
	key    *ecdsa.PrivateKey
	random io.Reader

	mu     sync.Mutex
	tokens map[string]token // by audience: the push service's origin
}

type token struct {
	jwt  string
	made time.Time
}

func newSigner(key *ecdsa.PrivateKey, random io.Reader) *signer {
	return &signer{key: key, random: random, tokens: map[string]token{}}
}

// publicKey is the key a browser subscribes with (`applicationServerKey`), in
// the uncompressed form, base64url. **It is the subscription's identity**: a
// push sent with any other key is refused, so this key is made once and kept.
func (s *signer) publicKey() string {
	b, _ := s.key.PublicKey.Bytes()
	return base64.RawURLEncoding.EncodeToString(b)
}

// authorization is the `Authorization` header for a message to endpoint.
func (s *signer) authorization(endpoint *url.URL, now time.Time) (string, error) {
	aud := endpoint.Scheme + "://" + endpoint.Host
	s.mu.Lock()
	defer s.mu.Unlock()
	t, ok := s.tokens[aud]
	if !ok || now.Sub(t.made) >= tokenReuse || now.Before(t.made) {
		jwt, err := s.sign(aud, now)
		if err != nil {
			return "", err
		}
		t = token{jwt: jwt, made: now}
		s.tokens[aud] = t
	}
	return "vapid t=" + t.jwt + ", k=" + s.publicKey(), nil
}

// sign is an ES256 JSON Web Token for one audience.
func (s *signer) sign(aud string, now time.Time) (string, error) {
	enc := base64.RawURLEncoding
	header := enc.EncodeToString([]byte(`{"typ":"JWT","alg":"ES256"}`))
	claims, err := json.Marshal(map[string]any{
		"aud": aud,
		"exp": now.Add(tokenLife).Unix(),
		"sub": Subject,
	})
	if err != nil {
		return "", err
	}
	input := header + "." + enc.EncodeToString(claims)
	digest := sha256.Sum256([]byte(input))
	r, ss, err := ecdsa.Sign(s.random, s.key, digest[:])
	if err != nil {
		return "", fmt.Errorf("push: signing the token: %w", err)
	}
	// **JWS wants r and s as two 32-byte integers side by side**, not the
	// ASN.1 sequence Go's other signing function gives: a DER signature is
	// refused with a 403 whose reason says nothing about its shape.
	sig := make([]byte, 64)
	r.FillBytes(sig[:32])
	ss.FillBytes(sig[32:])
	return input + "." + enc.EncodeToString(sig), nil
}

// newKey makes the monitor's VAPID key.
func newKey(random io.Reader) (*ecdsa.PrivateKey, error) {
	return ecdsa.GenerateKey(elliptic.P256(), random)
}
