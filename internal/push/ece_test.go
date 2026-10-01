package push

import (
	"bytes"
	"crypto/aes"
	"crypto/cipher"
	"crypto/ecdh"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/binary"
	"errors"
	"strings"
	"testing"
)

// b64 decodes the RFC's base64url, which it prints wrapped and with spaces.
func b64(t *testing.T, s string) []byte {
	t.Helper()
	s = strings.Join(strings.Fields(s), "")
	b, err := base64.RawURLEncoding.DecodeString(s)
	if err != nil {
		t.Fatalf("base64url %q: %v", s, err)
	}
	return b
}

// The example of RFC 8291, section 5, with its intermediate values in
// appendix A: the keys, the salt and the body a push service receives.
const (
	rfcPlaintext = "When I grow up, I want to be a watermelon"
	rfcAuth      = "BTBZMqHH6r4Tts7J_aSIgg"
	rfcUAPrivate = "q1dXpw3UpT5VOmu_cf_v6ih07Aems3njxI-JWgLcM94"
	rfcUAPublic  = "BCVxsr7N_eNgVRqvHtD0zTZsEc6-VV-JvLexhqUzORcx aOzi6-AYWXvTBHm4bjyPjs7Vd8pZGH6SRpkNtoIAiw4"
	rfcASPrivate = "yfWPiYE-n46HLnH0KqZOF1fJJU3MYrct3AELtAQ-oRw"
	rfcSalt      = "DGv6ra1nlYgDCS1FRnbzlw"
	rfcBody      = `DGv6ra1nlYgDCS1FRnbzlwAAEABBBP4z9KsN6nGRTbVYI_c7VJSPQTBtkgcy27ml
	                mlMoZIIgDll6e3vCYLocInmYWAmS6TlzAC8wEqKK6PBru3jl7A_yl95bQpu6cVPT
	                pK4Mqgkf1CXztLVBSt2Ks3oZwbuwXPXLWyouBWLVWGNWQexSgSxsj_Qulcy4a-fN`
)

// **The body is the RFC's, byte for byte.** It is the one check here that we
// did not write: two implementations of the same wrong idea agree with each
// other, and the example in the specification is somebody else's
// implementation, frozen.
func TestTheRFCExampleIsReproducedByteForByte(t *testing.T) {
	// The salt first and the ephemeral scalar second: the order encrypt draws
	// them in.
	random := bytes.NewReader(append(b64(t, rfcSalt), b64(t, rfcASPrivate)...))
	got, err := encrypt([]byte(rfcPlaintext), b64(t, rfcUAPublic), b64(t, rfcAuth), random)
	if err != nil {
		t.Fatal(err)
	}
	if want := b64(t, rfcBody); !bytes.Equal(got, want) {
		t.Fatalf("the body is not the RFC's:\n got %x\nwant %x", got, want)
	}
}

// **And a fresh message decrypts on the receiving side**, with the derivation
// written again here from the specification — HMAC by hand, not the HKDF the
// package calls — so that what is proved is agreement with RFC 8291 and not
// with ourselves.
func TestAMessageDecryptsWhereTheBrowserIs(t *testing.T) {
	ua, err := ecdh.P256().NewPrivateKey(b64(t, rfcUAPrivate))
	if err != nil {
		t.Fatal(err)
	}
	auth := b64(t, rfcAuth)
	msg := []byte(`{"web_push":8030,"notification":{"title":"PAT Monitor","body":"A baby crying"}}`)

	body, err := encrypt(msg, ua.PublicKey().Bytes(), auth, randomSource)
	if err != nil {
		t.Fatal(err)
	}
	got, err := decryptAsTheBrowser(body, ua, auth)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, msg) {
		t.Fatalf("decrypted %q, sent %q", got, msg)
	}

	// Two messages are two salts and two keys: the same plaintext twice is not
	// the same body twice.
	again, _ := encrypt(msg, ua.PublicKey().Bytes(), auth, randomSource)
	if bytes.Equal(body[:16], again[:16]) || bytes.Equal(body[21:86], again[21:86]) {
		t.Fatal("two messages share a salt or an ephemeral key")
	}
}

func TestAPayloadThatNeedsTwoRecordsIsRefused(t *testing.T) {
	ua, _ := ecdh.P256().NewPrivateKey(b64(t, rfcUAPrivate))
	pub := ua.PublicKey().Bytes()
	if _, err := encrypt(make([]byte, MaxPayload), pub, b64(t, rfcAuth), randomSource); err != nil {
		t.Fatalf("the largest payload that fits was refused: %v", err)
	}
	_, err := encrypt(make([]byte, MaxPayload+1), pub, b64(t, rfcAuth), randomSource)
	if !errors.Is(err, ErrTooLarge) {
		t.Fatalf("one byte past the record: %v, want ErrTooLarge", err)
	}
}

// decryptAsTheBrowser is RFC 8291 section 3.4 and RFC 8188 section 2, from
// the receiver's end, with nothing of the package's but the types.
func decryptAsTheBrowser(body []byte, ua *ecdh.PrivateKey, auth []byte) ([]byte, error) {
	hmacOf := func(key, data []byte) []byte {
		m := hmac.New(sha256.New, key)
		m.Write(data)
		return m.Sum(nil)
	}
	if len(body) < 21 {
		return nil, errors.New("short header")
	}
	salt := body[:16]
	rs := binary.BigEndian.Uint32(body[16:20])
	idlen := int(body[20])
	if rs != 4096 || idlen != 65 || len(body) < 21+idlen {
		return nil, errors.New("unexpected header")
	}
	asPub, err := ecdh.P256().NewPublicKey(body[21 : 21+idlen])
	if err != nil {
		return nil, err
	}
	shared, err := ua.ECDH(asPub)
	if err != nil {
		return nil, err
	}
	prkKey := hmacOf(auth, shared)
	info := append([]byte("WebPush: info\x00"), ua.PublicKey().Bytes()...)
	info = append(info, asPub.Bytes()...)
	ikm := hmacOf(prkKey, append(info, 0x01))
	prk := hmacOf(salt, ikm)
	cek := hmacOf(prk, []byte("Content-Encoding: aes128gcm\x00\x01"))[:16]
	nonce := hmacOf(prk, []byte("Content-Encoding: nonce\x00\x01"))[:12]

	block, err := aes.NewCipher(cek)
	if err != nil {
		return nil, err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}
	plain, err := gcm.Open(nil, nonce, body[21+idlen:], nil)
	if err != nil {
		return nil, err
	}
	// The last record ends with 0x02 and padding zeros after it.
	i := bytes.LastIndexByte(plain, 0x02)
	if i < 0 || bytes.ContainsFunc(plain[i+1:], func(r rune) bool { return r != 0 }) {
		return nil, errors.New("no last-record delimiter")
	}
	return plain[:i], nil
}
