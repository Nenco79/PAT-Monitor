package push

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"math/big"
	"net/url"
	"strings"
	"testing"
	"time"
)

// **The token is read the way a push service reads it**: the header split
// off the `Authorization` value by hand, the three parts decoded, the claims
// checked against RFC 8292, and the signature verified with the public key the
// header itself carries — not with the signer's own copy.
func TestTheTokenIsWhatAPushServiceChecks(t *testing.T) {
	key, err := newKey(randomSource)
	if err != nil {
		t.Fatal(err)
	}
	s := newSigner(key, randomSource)
	endpoint, _ := url.Parse("https://web.push.apple.com/QGuQyavXutnMH")
	now := time.Unix(1_800_000_000, 0)

	auth, err := s.authorization(endpoint, now)
	if err != nil {
		t.Fatal(err)
	}
	rest, ok := strings.CutPrefix(auth, "vapid t=")
	if !ok {
		t.Fatalf("not a vapid header: %q", auth)
	}
	jwt, k, ok := strings.Cut(rest, ", k=")
	if !ok {
		t.Fatalf("no k= in %q", auth)
	}
	parts := strings.Split(jwt, ".")
	if len(parts) != 3 {
		t.Fatalf("%d parts in the token", len(parts))
	}
	dec := base64.RawURLEncoding
	var header, claims map[string]any
	for i, into := range []*map[string]any{&header, &claims} {
		raw, err := dec.DecodeString(parts[i])
		if err != nil {
			t.Fatal(err)
		}
		if err := json.Unmarshal(raw, into); err != nil {
			t.Fatal(err)
		}
	}
	if header["alg"] != "ES256" {
		t.Errorf("alg %v", header["alg"])
	}
	if claims["aud"] != "https://web.push.apple.com" {
		t.Errorf("aud %v: it is the push service's origin, no path", claims["aud"])
	}
	exp := int64(claims["exp"].(float64))
	if life := time.Unix(exp, 0).Sub(now); life <= 0 || life > 24*time.Hour {
		t.Errorf("the token lives %v: RFC 8292 allows up to a day", life)
	}
	sub, _ := claims["sub"].(string)
	if !strings.HasPrefix(sub, "https://") && !strings.HasPrefix(sub, "mailto:") {
		t.Errorf("sub %q is neither a URL nor mailto:", sub)
	}
	for _, local := range []string{"localhost", ".local", "127.0.0.1", "example"} {
		if strings.Contains(sub, local) {
			t.Errorf("sub %q names a place nobody can reach: Apple refuses it", sub)
		}
	}

	pub, err := dec.DecodeString(k)
	if err != nil || len(pub) != 65 || pub[0] != 4 {
		t.Fatalf("k is not an uncompressed P-256 point: %x", pub)
	}
	x, y := new(big.Int).SetBytes(pub[1:33]), new(big.Int).SetBytes(pub[33:])
	verifier := &ecdsa.PublicKey{Curve: elliptic.P256(), X: x, Y: y}
	sig, err := dec.DecodeString(parts[2])
	if err != nil || len(sig) != 64 {
		t.Fatalf("the signature is %d bytes, JWS wants r||s in 64", len(sig))
	}
	digest := sha256.Sum256([]byte(parts[0] + "." + parts[1]))
	r, ss := new(big.Int).SetBytes(sig[:32]), new(big.Int).SetBytes(sig[32:])
	if !ecdsa.Verify(verifier, digest[:], r, ss) {
		t.Fatal("the signature does not verify with the key the header carries")
	}
	if k != s.publicKey() {
		t.Fatal("the header carries a key other than the one browsers subscribe with")
	}
}

// **Apple asks not to be sent a new token more than once an hour**, so one
// token serves an audience for a stretch — and two audiences are two tokens,
// because `aud` is part of what is signed.
func TestATokenIsReusedAndNotSignedPerMessage(t *testing.T) {
	key, _ := newKey(randomSource)
	s := newSigner(key, randomSource)
	apple, _ := url.Parse("https://web.push.apple.com/a")
	appleAgain, _ := url.Parse("https://web.push.apple.com/b")
	google, _ := url.Parse("https://fcm.googleapis.com/fcm/send/c")
	now := time.Unix(1_800_000_000, 0)

	first, _ := s.authorization(apple, now)
	if again, _ := s.authorization(appleAgain, now.Add(59*time.Minute)); again != first {
		t.Fatal("a second token for the same service inside the hour")
	}
	if other, _ := s.authorization(google, now); other == first {
		t.Fatal("one token for two audiences")
	}
	if later, _ := s.authorization(apple, now.Add(tokenReuse)); later == first {
		t.Fatal("the token outlived its reuse: it would expire in somebody's queue")
	}
	if tokenReuse < time.Hour || tokenReuse >= tokenLife {
		t.Fatalf("reuse %v, life %v: at least an hour, and less than the life", tokenReuse, tokenLife)
	}
}
