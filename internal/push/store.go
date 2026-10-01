package push

import (
	"crypto/ecdh"
	"crypto/ecdsa"
	"crypto/elliptic"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net/url"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"time"
)

// FileName is the store's file, in the folder the node's identity lives in.
//
// **Beside the node and not beside the configuration**, and the reason is
// what a subscription is bound to: the browser subscribed on one origin, and
// the origin is the node's name, which belongs to this machine. The
// configuration's folder roams with the profile; a subscription carried to
// another machine would be pushed to from a monitor whose address it never
// saw, with a key it never subscribed with.
const FileName = "push.json"

// MaxSubscriptions bounds the list. A house subscribes a handful of phones
// and computers; with no bound, anybody holding a session could fill the file
// and make every alert a thousand requests.
const MaxSubscriptions = 32

// ErrFull is a subscription refused because the list is at its bound.
var ErrFull = errors.New("push: too many subscriptions")

// Subscription is one browser that asked to be told.
type Subscription struct {
	// Endpoint is the push service's address for this browser, and the key
	// the list is kept by: a browser that subscribes again gets the same one,
	// or a new one that replaces nothing.
	Endpoint string `json:"endpoint"`
	// P256dh and Auth are the browser's key and secret, base64url, as the
	// subscription hands them over.
	P256dh string `json:"p256dh"`
	Auth   string `json:"auth"`
	// Lang is the language the page was speaking when it subscribed: the
	// notification is composed here, so the words are chosen here too.
	Lang string `json:"lang"`
	// Origin is scheme and host of the page that subscribed, which is where a
	// tap on the notification goes.
	Origin string `json:"origin"`
	// Created and LastOK are Unix seconds: when it subscribed, and when a
	// push service last took a message for it.
	Created int64 `json:"created"`
	LastOK  int64 `json:"last_ok,omitempty"`
}

// check refuses a subscription that could never be written to.
func (s Subscription) check() error {
	if _, err := endpointURL(s.Endpoint); err != nil {
		return err
	}
	// The same decoding the sender uses, so the two cannot disagree about
	// which encodings a browser may hand over.
	pub, auth, err := s.keys()
	if err != nil {
		return fmt.Errorf("push: the browser's key or secret is not base64url: %w", err)
	}
	if _, err := ecdh.P256().NewPublicKey(pub); err != nil {
		return fmt.Errorf("push: the browser's key is not a P-256 point: %w", err)
	}
	if len(auth) != 16 {
		return errors.New("push: the browser's secret is not 16 bytes")
	}
	if len(s.Lang) > 35 {
		return errors.New("push: a language tag that long is not one")
	}
	o, err := url.Parse(s.Origin)
	if err != nil || o.Scheme != "https" || o.Host == "" || o.Path != "" {
		return errors.New("push: the origin is not an https origin")
	}
	return nil
}

// keys decodes the browser's key and secret, for check and for the sender.
func (s Subscription) keys() (pub, auth []byte, err error) {
	if pub, err = base64.RawURLEncoding.DecodeString(strings.TrimRight(s.P256dh, "=")); err != nil {
		return nil, nil, err
	}
	auth, err = base64.RawURLEncoding.DecodeString(strings.TrimRight(s.Auth, "="))
	return pub, auth, err
}

// Store keeps the VAPID key and the subscriptions, in one file.
type Store struct {
	path string

	mu   sync.Mutex
	key  *ecdsa.PrivateKey
	subs []Subscription
	// dirty is a LastOK changed in memory and not yet on disk.
	dirty bool
}

// file is the shape on disk.
type file struct {
	// Key is the VAPID private key, raw and base64url. **It is made once and
	// never again**: every subscription was made against its public half, and
	// a new key is every browser subscribed to nothing.
	Key           string         `json:"key"`
	Subscriptions []Subscription `json:"subscriptions"`
}

// OpenStore reads the store in dir, making the key the first time.
//
// **The folder is handed in**, because a function that asks the system where
// to write runs on the disk of whoever runs its tests.
func OpenStore(dir string, random io.Reader) (*Store, error) {
	s := &Store{path: filepath.Join(dir, FileName)}
	data, err := os.ReadFile(s.path)
	switch {
	case errors.Is(err, fs.ErrNotExist):
		key, err := newKey(random)
		if err != nil {
			return nil, err
		}
		s.key = key
		if err := s.save(); err != nil {
			return nil, err
		}
		return s, nil
	case err != nil:
		return nil, fmt.Errorf("push: reading %s: %w", s.path, err)
	}
	var f file
	if err := json.Unmarshal(data, &f); err != nil {
		return nil, fmt.Errorf("push: %s is not readable: %w", s.path, err)
	}
	raw, err := base64.RawURLEncoding.DecodeString(f.Key)
	if err != nil {
		return nil, fmt.Errorf("push: the key in %s: %w", s.path, err)
	}
	if s.key, err = ecdsa.ParseRawPrivateKey(elliptic.P256(), raw); err != nil {
		return nil, fmt.Errorf("push: the key in %s: %w", s.path, err)
	}
	for _, sub := range f.Subscriptions {
		if sub.check() == nil && len(s.subs) < MaxSubscriptions {
			s.subs = append(s.subs, sub)
		}
	}
	return s, nil
}

// Key is the VAPID key.
func (s *Store) Key() *ecdsa.PrivateKey { return s.key }

// Put adds a subscription, or refreshes the one with the same endpoint.
//
// **It is idempotent on purpose**: the page sends its subscription again at
// every opening, because a browser can lose one without saying so — iOS never
// fires the event that would tell — and the only cure is to hand it over again
// each time and let the store keep one copy.
func (s *Store) Put(sub Subscription, now time.Time) error {
	if err := sub.check(); err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if i := s.find(sub.Endpoint); i >= 0 {
		old := s.subs[i]
		sub.Created, sub.LastOK = old.Created, old.LastOK
		if sub == old {
			return nil // nothing to write
		}
		s.subs[i] = sub
		return s.save()
	}
	if len(s.subs) >= MaxSubscriptions {
		return ErrFull
	}
	sub.Created, sub.LastOK = now.Unix(), 0
	s.subs = append(s.subs, sub)
	return s.save()
}

// Remove drops the subscription with that endpoint, and says whether there
// was one.
func (s *Store) Remove(endpoint string) (bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	i := s.find(endpoint)
	if i < 0 {
		return false, nil
	}
	s.subs = slices.Delete(s.subs, i, i+1)
	return true, s.save()
}

// Delivered records that a push service took a message for endpoint.
//
// **In memory, and on disk with the next write or at Flush**: nothing reads
// LastOK while the monitor runs, and a night of alerts to several devices was
// one rewrite of the whole file per device per alert, all under this lock.
func (s *Store) Delivered(endpoint string, now time.Time) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if i := s.find(endpoint); i >= 0 {
		s.subs[i].LastOK = now.Unix()
		s.dirty = true
	}
}

// Flush writes what Delivered kept in memory, if anything.
func (s *Store) Flush() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.dirty {
		return nil
	}
	return s.save()
}

// Get is the subscription with that endpoint.
func (s *Store) Get(endpoint string) (Subscription, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if i := s.find(endpoint); i >= 0 {
		return s.subs[i], true
	}
	return Subscription{}, false
}

// All is a copy of the list.
func (s *Store) All() []Subscription {
	s.mu.Lock()
	defer s.mu.Unlock()
	return slices.Clone(s.subs)
}

func (s *Store) find(endpoint string) int {
	return slices.IndexFunc(s.subs, func(x Subscription) bool { return x.Endpoint == endpoint })
}

// save writes the file whole, through a temporary name, so that an
// interruption leaves the old file or the new one and never half of either.
// The caller holds the lock, or is the only one who can see the store.
func (s *Store) save() error {
	raw, err := s.key.Bytes()
	if err != nil {
		return err
	}
	data, err := json.MarshalIndent(file{
		Key:           base64.RawURLEncoding.EncodeToString(raw),
		Subscriptions: s.subs,
	}, "", "  ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(s.path), 0o700); err != nil {
		return fmt.Errorf("push: making the folder: %w", err)
	}
	tmp := s.path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o600); err != nil {
		return fmt.Errorf("push: writing %s: %w", tmp, err)
	}
	if err := os.Rename(tmp, s.path); err != nil {
		_ = os.Remove(tmp)
		return fmt.Errorf("push: replacing %s: %w", s.path, err)
	}
	s.dirty = false
	return nil
}
