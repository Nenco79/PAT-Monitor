package push

import (
	"crypto/ecdh"
	"encoding/base64"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func aSubscription(t *testing.T, endpoint string) Subscription {
	t.Helper()
	ua, err := ecdh.P256().GenerateKey(randomSource)
	if err != nil {
		t.Fatal(err)
	}
	enc := base64.RawURLEncoding
	return Subscription{
		Endpoint: endpoint,
		P256dh:   enc.EncodeToString(ua.PublicKey().Bytes()),
		Auth:     enc.EncodeToString(make([]byte, 16)),
		Lang:     "en",
		Origin:   "https://patmon-1a2b3c.quercia-lieve.ts.net",
	}
}

// **The key is made once and survives every reopening.** Every browser
// subscribed against its public half: a monitor that made a new one at start
// would keep a list of subscriptions no push service would take a message for.
func TestTheKeyOutlivesTheProcess(t *testing.T) {
	dir := t.TempDir()
	first, err := OpenStore(dir, randomSource)
	if err != nil {
		t.Fatal(err)
	}
	sub := aSubscription(t, "https://fcm.googleapis.com/fcm/send/one")
	if err := first.Put(sub, time.Unix(100, 0)); err != nil {
		t.Fatal(err)
	}
	again, err := OpenStore(dir, randomSource)
	if err != nil {
		t.Fatal(err)
	}
	a, _ := first.Key().Bytes()
	b, _ := again.Key().Bytes()
	if string(a) != string(b) {
		t.Fatal("the key changed across a reopening")
	}
	if got, ok := again.Get(sub.Endpoint); !ok || got.Created != 100 {
		t.Fatalf("the subscription did not survive: %+v %v", got, ok)
	}
	if _, err := os.Stat(filepath.Join(dir, FileName+".tmp")); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("a temporary file was left behind")
	}
}

// **A subscription handed over again is the same one**, because the page
// hands it over at every opening: the list keeps one copy and its history.
func TestASubscriptionHandedOverAgainIsTheSameOne(t *testing.T) {
	s, _ := OpenStore(t.TempDir(), randomSource)
	sub := aSubscription(t, "https://web.push.apple.com/abc")
	_ = s.Put(sub, time.Unix(100, 0))
	s.Delivered(sub.Endpoint, time.Unix(150, 0))
	sub.Lang = "it"
	if err := s.Put(sub, time.Unix(200, 0)); err != nil {
		t.Fatal(err)
	}
	all := s.All()
	if len(all) != 1 || all[0].Lang != "it" || all[0].Created != 100 || all[0].LastOK != 150 {
		t.Fatalf("after a second hand-over: %+v", all)
	}
}

// **A delivery is not a write**: the time a service last took a message is
// kept in memory and reaches the file with the next change or at Flush, not
// once per device per alert.
func TestADeliveryIsWrittenAtFlushAndNotEachTime(t *testing.T) {
	dir := t.TempDir()
	s, _ := OpenStore(dir, randomSource)
	sub := aSubscription(t, "https://web.push.apple.com/abc")
	_ = s.Put(sub, time.Unix(100, 0))
	before, _ := os.ReadFile(filepath.Join(dir, FileName))
	s.Delivered(sub.Endpoint, time.Unix(150, 0))
	if after, _ := os.ReadFile(filepath.Join(dir, FileName)); string(after) != string(before) {
		t.Fatal("a delivery rewrote the file")
	}
	if err := s.Flush(); err != nil {
		t.Fatal(err)
	}
	again, _ := OpenStore(dir, randomSource)
	if got, _ := again.Get(sub.Endpoint); got.LastOK != 150 {
		t.Fatalf("after Flush the file says LastOK %d", got.LastOK)
	}
}

func TestTheListHasABound(t *testing.T) {
	s, _ := OpenStore(t.TempDir(), randomSource)
	for i := range MaxSubscriptions {
		if err := s.Put(aSubscription(t, fmt.Sprintf("https://fcm.googleapis.com/fcm/send/%d", i)), time.Now()); err != nil {
			t.Fatal(err)
		}
	}
	err := s.Put(aSubscription(t, "https://fcm.googleapis.com/fcm/send/one-more"), time.Now())
	if !errors.Is(err, ErrFull) {
		t.Fatalf("past the bound: %v, want ErrFull", err)
	}
	// And one already there can still refresh itself.
	if err := s.Put(aSubscription(t, "https://fcm.googleapis.com/fcm/send/0"), time.Now()); err != nil {
		t.Fatalf("a refresh at the bound was refused: %v", err)
	}
}

func TestASubscriptionThatCouldNeverBeWrittenToIsRefused(t *testing.T) {
	s, _ := OpenStore(t.TempDir(), randomSource)
	good := aSubscription(t, "https://fcm.googleapis.com/fcm/send/x")
	for name, spoil := range map[string]func(*Subscription){
		"endpoint in the house": func(x *Subscription) { x.Endpoint = "https://192.168.1.1/x" },
		"key not a point":       func(x *Subscription) { x.P256dh = base64.RawURLEncoding.EncodeToString(make([]byte, 65)) },
		"secret of 15 bytes":    func(x *Subscription) { x.Auth = base64.RawURLEncoding.EncodeToString(make([]byte, 15)) },
		"origin in plain http":  func(x *Subscription) { x.Origin = "http://192.168.1.5:8080" },
		"origin with a path":    func(x *Subscription) { x.Origin = "https://patmon-1a2b3c.quercia-lieve.ts.net/clips" },
	} {
		bad := good
		spoil(&bad)
		if err := s.Put(bad, time.Now()); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
	if len(s.All()) != 0 {
		t.Fatal("something refused ended up in the list")
	}
}
