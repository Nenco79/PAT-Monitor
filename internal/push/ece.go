// Package push sends Web Push notifications to the devices that asked for
// them, and keeps the list of who asked.
//
// **It is the one channel the monitor has to somebody whose page is closed**,
// and it goes through a service chosen by the browser, not by us: Edge hands
// its subscriptions to `notify.windows.com`, Chrome and Brave to
// `fcm.googleapis.com`, Firefox to Mozilla, Safari to Apple. The monitor
// writes to whichever endpoint the browser gave it, encrypted for that
// browser alone, and the service in the middle carries bytes it cannot read.
//
// **Nothing here is somebody else's code.** The two specifications this needs
// — the payload's encryption (RFC 8291 on RFC 8188) and the sender's signature
// (RFC 8292) — are an ECDH, an HKDF, an AES-GCM and an ES256, all of which the
// standard library has. A module for them would be a dependency for forty
// lines.
package push

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/ecdh"
	"crypto/hkdf"
	"crypto/rand"
	"crypto/sha256"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
)

// recordSize is the one record a message fits in, and the size declared in
// its header. RFC 8030 has every push service accept 4096 bytes of body, so a
// message is one record, and a payload that would need two is refused rather
// than split.
const recordSize = 4096

// headerSize is salt, record size, key length and the sender's public key.
const headerSize = 16 + 4 + 1 + 65

// MaxPayload is the plaintext that fits: the record, less the header, the
// GCM tag and the padding delimiter.
const MaxPayload = recordSize - headerSize - 16 - 1

// ErrTooLarge is a payload that would not fit in one record.
var ErrTooLarge = errors.New("push: payload larger than one record")

// encrypt seals plaintext for one browser, as RFC 8291 says: the `aes128gcm`
// content coding of RFC 8188, with the key agreed between a fresh key of ours
// and the browser's, bound to the browser's authentication secret.
//
// **A badly encrypted body is accepted by the push service with a 201**, and
// that is why this function is tested from the receiving side: the service
// cannot read what it carries, so it cannot say it is wrong either, and the
// browser drops what it cannot decrypt without telling anybody.
//
// uaPublic is the browser's key in the uncompressed form it hands over
// (`p256dh`), auth its 16-byte secret. random supplies the salt and the
// ephemeral key; it is a parameter so that the example in RFC 8291 can be
// reproduced byte for byte.
func encrypt(plaintext, uaPublic, auth []byte, random io.Reader) ([]byte, error) {
	if len(plaintext) > MaxPayload {
		return nil, ErrTooLarge
	}
	curve := ecdh.P256()
	ua, err := curve.NewPublicKey(uaPublic)
	if err != nil {
		return nil, fmt.Errorf("push: the browser's key: %w", err)
	}
	if len(auth) != 16 {
		return nil, fmt.Errorf("push: the browser's secret is %d bytes, not 16", len(auth))
	}
	salt := make([]byte, 16)
	if _, err := io.ReadFull(random, salt); err != nil {
		return nil, fmt.Errorf("push: salt: %w", err)
	}
	as, err := ephemeralKey(curve, random)
	if err != nil {
		return nil, err
	}
	return seal(plaintext, ua, as, auth, salt)
}

// ephemeralKey is a fresh P-256 key, drawn from random. crypto/ecdh ignores
// the reader it is given, so the scalar is read here and handed over as
// bytes: that is what lets the RFC's example key in.
func ephemeralKey(curve ecdh.Curve, random io.Reader) (*ecdh.PrivateKey, error) {
	for range 8 {
		d := make([]byte, 32)
		if _, err := io.ReadFull(random, d); err != nil {
			return nil, fmt.Errorf("push: ephemeral key: %w", err)
		}
		if k, err := curve.NewPrivateKey(d); err == nil {
			return k, nil
		}
		// A scalar of zero or past the order: vanishingly rare with a real
		// source, and the answer is to draw again.
	}
	return nil, errors.New("push: no valid ephemeral key after eight draws")
}

// seal is the derivation and the encryption, with every input chosen.
func seal(plaintext []byte, ua *ecdh.PublicKey, as *ecdh.PrivateKey, auth, salt []byte) ([]byte, error) {
	shared, err := as.ECDH(ua)
	if err != nil {
		return nil, fmt.Errorf("push: key agreement: %w", err)
	}
	uaBytes, asBytes := ua.Bytes(), as.PublicKey().Bytes()

	// The shared secret is bound to the browser's authentication secret and
	// to both keys: "WebPush: info" || 0x00 || ua_public || as_public.
	keyInfo := append([]byte("WebPush: info\x00"), uaBytes...)
	keyInfo = append(keyInfo, asBytes...)
	prkKey, err := hkdf.Extract(sha256.New, shared, auth)
	if err != nil {
		return nil, err
	}
	ikm, err := hkdf.Expand(sha256.New, prkKey, string(keyInfo), 32)
	if err != nil {
		return nil, err
	}

	// Then RFC 8188's own derivation, salted per message.
	prk, err := hkdf.Extract(sha256.New, ikm, salt)
	if err != nil {
		return nil, err
	}
	cek, err := hkdf.Expand(sha256.New, prk, "Content-Encoding: aes128gcm\x00", 16)
	if err != nil {
		return nil, err
	}
	nonce, err := hkdf.Expand(sha256.New, prk, "Content-Encoding: nonce\x00", 12)
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

	out := make([]byte, 0, headerSize+len(plaintext)+1+gcm.Overhead())
	out = append(out, salt...)
	out = binary.BigEndian.AppendUint32(out, recordSize)
	out = append(out, byte(len(asBytes)))
	out = append(out, asBytes...)
	// 0x02 is the delimiter of the last record, and there is no padding after
	// it: the length of a notification says nothing a code does not already.
	padded := append(append([]byte{}, plaintext...), 0x02)
	return gcm.Seal(out, nonce, padded, nil), nil
}

// randomSource is where salts and keys come from outside the tests.
var randomSource io.Reader = rand.Reader
