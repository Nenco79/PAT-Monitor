package push

import (
	"context"
	"crypto/ecdh"
	"encoding/base64"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"net/url"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"patmonitor/internal/alerts"
)

// fakeService is a push service on loopback that answers what the test says,
// and keeps what it was sent.
type fakeService struct {
	*httptest.Server
	mu       sync.Mutex
	requests []*http.Request
	bodies   [][]byte
	answer   func(n int) (status int, header http.Header)
}

func newFakeService(t *testing.T, answer func(n int) (int, http.Header)) *fakeService {
	t.Helper()
	f := &fakeService{answer: answer}
	f.Server = httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		f.mu.Lock()
		n := len(f.requests)
		f.requests = append(f.requests, r)
		f.bodies = append(f.bodies, body)
		f.mu.Unlock()
		status, h := f.answer(n)
		for k, v := range h {
			w.Header()[k] = v
		}
		w.WriteHeader(status)
		_, _ = io.WriteString(w, "the service's reason")
	}))
	t.Cleanup(f.Close)
	return f
}

func (f *fakeService) count() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.requests)
}

// testRig is a notifier writing to a fake service, with a clock that the
// sender's own waits advance.
type testRig struct {
	store  *Store
	send   *sender
	n      *Notifier
	ua     *ecdh.PrivateKey
	auth   []byte
	waits  []time.Duration
	clock  time.Time
	clockM sync.Mutex
}

func newRig(t *testing.T, svc *fakeService) *testRig {
	t.Helper()
	store, err := OpenStore(t.TempDir(), randomSource)
	if err != nil {
		t.Fatal(err)
	}
	r := &testRig{store: store, clock: time.Unix(1_800_000_000, 0)}
	r.send = newSender(newSigner(store.Key(), randomSource), randomSource)
	r.send.client = svc.Client()
	// The fake service is on loopback, which the real check refuses: here the
	// check is the scheme alone, and the real one has tests of its own.
	r.send.admits = func(raw string) (*url.URL, error) {
		u, err := url.Parse(raw)
		if err != nil || u.Scheme != "https" {
			return nil, ErrNotAllowed
		}
		return u, nil
	}
	r.send.now = func() time.Time {
		r.clockM.Lock()
		defer r.clockM.Unlock()
		return r.clock
	}
	r.send.sleep = func(_ context.Context, d time.Duration) bool {
		r.clockM.Lock()
		defer r.clockM.Unlock()
		r.waits = append(r.waits, d)
		r.clock = r.clock.Add(d)
		// A bound of the test's own, so that a retry with no end fails here
		// instead of hanging the run.
		return len(r.waits) < 50
	}
	r.n = newNotifier(store, r.send, discard(), randomSource)
	t.Cleanup(r.n.Close)
	r.ua, _ = ecdh.P256().GenerateKey(randomSource)
	r.auth = make([]byte, 16)
	_, _ = io.ReadFull(randomSource, r.auth)
	return r
}

// subscribe puts a subscription pointing at the fake service.
func (r *testRig) subscribe(t *testing.T, svc *fakeService, path, lang string) Subscription {
	t.Helper()
	enc := base64.RawURLEncoding
	sub := Subscription{
		Endpoint: svc.URL + path,
		P256dh:   enc.EncodeToString(r.ua.PublicKey().Bytes()),
		Auth:     enc.EncodeToString(r.auth),
		Lang:     lang,
		Origin:   "https://patmon-1a2b3c.quercia-lieve.ts.net",
	}
	// Put checks the endpoint with the real rule, which refuses loopback: the
	// list is written directly instead.
	r.store.mu.Lock()
	r.store.subs = append(r.store.subs, sub)
	r.store.mu.Unlock()
	return sub
}

func discard() *slog.Logger { return slog.New(slog.DiscardHandler) }

// **Only an ended subscription is removed.** A 404 and a 410 say the
// endpoint is gone; every other refusal is about the message or about us, and
// dropping the device for it would switch somebody's notifications off
// because a service had a bad minute.
func TestOnlyAnEndedSubscriptionIsRemoved(t *testing.T) {
	for _, tc := range []struct {
		status  int
		outcome Outcome
		removed bool
	}{
		{201, Delivered, false},
		{404, Gone, true},
		{410, Gone, true},
		{413, TooLarge, false},
		{400, Refused, false},
		{401, Refused, false},
		{403, Refused, false},
		{429, Throttled, false},
		{500, Unavailable, false},
		{503, Unavailable, false},
	} {
		svc := newFakeService(t, func(int) (int, http.Header) { return tc.status, nil })
		r := newRig(t, svc)
		sub := r.subscribe(t, svc, "/push/one", "en")
		a := r.send.once(context.Background(), sub, message{ttl: time.Minute, payload: []byte("{}")})
		r.n.settle(sub, a)
		if a.outcome != tc.outcome {
			t.Errorf("%d: outcome %s, want %s", tc.status, a.outcome, tc.outcome)
		}
		if _, still := r.store.Get(sub.Endpoint); still == tc.removed {
			t.Errorf("%d: removed=%v, want %v", tc.status, !still, tc.removed)
		}
	}
}

// **A retry stops where the message stops being worth having.** A service
// that is down is tried again with longer waits, and the last attempt falls
// inside the TTL: a cry retried past it would arrive as news about a room
// that has changed.
func TestARetryStaysInsideTheTTL(t *testing.T) {
	svc := newFakeService(t, func(int) (int, http.Header) { return 503, nil })
	r := newRig(t, svc)
	sub := r.subscribe(t, svc, "/push/one", "en")
	a := r.send.deliver(context.Background(), sub, message{ttl: eventTTL, payload: []byte("{}")})
	if a.outcome != Unavailable {
		t.Fatalf("outcome %s", a.outcome)
	}
	var total time.Duration
	for _, w := range r.waits {
		total += w
	}
	if svc.count() < 3 {
		t.Fatalf("%d attempts in a five-minute TTL: a service down for a moment loses the cry", svc.count())
	}
	if total >= eventTTL {
		t.Fatalf("waited %v in all, past the %v TTL", total, eventTTL)
	}
}

// **And a service that asks for a wait gets it**, instead of the retry's own.
func TestAThrottledServiceIsWaitedFor(t *testing.T) {
	svc := newFakeService(t, func(n int) (int, http.Header) {
		if n == 0 {
			return 429, http.Header{"Retry-After": {"17"}}
		}
		return 201, nil
	})
	r := newRig(t, svc)
	sub := r.subscribe(t, svc, "/push/one", "en")
	a := r.send.deliver(context.Background(), sub, message{ttl: eventTTL, payload: []byte("{}")})
	if a.outcome != Delivered || len(r.waits) != 1 || r.waits[0] != 17*time.Second {
		t.Fatalf("outcome %s after waits %v, want delivered after one wait of 17s", a.outcome, r.waits)
	}

	// A wait longer than the TTL is not taken: the message is given up.
	svc2 := newFakeService(t, func(int) (int, http.Header) {
		return 429, http.Header{"Retry-After": {"3600"}}
	})
	r2 := newRig(t, svc2)
	sub2 := r2.subscribe(t, svc2, "/push/one", "en")
	if a := r2.send.deliver(context.Background(), sub2, message{ttl: eventTTL, payload: []byte("{}")}); a.outcome != Throttled || svc2.count() != 1 {
		t.Fatalf("outcome %s in %d attempts, want one throttled attempt", a.outcome, svc2.count())
	}
}

// **The request is what RFC 8030 and 8292 ask for**, read on the service's
// side: the headers, the signature's audience, and a body that the browser the
// subscription belongs to can decrypt into the words of its own language.
func TestTheRequestIsWhatAPushServiceExpects(t *testing.T) {
	svc := newFakeService(t, func(int) (int, http.Header) { return 201, nil })
	r := newRig(t, svc)
	r.subscribe(t, svc, "/push/it", "it")

	r.n.Alerted([]alerts.Alert{{ID: 1, Code: alerts.Cry, Level: alerts.Event}}, nil)
	r.n.wg.Wait()
	if svc.count() != 1 {
		t.Fatalf("%d requests for one alert and one device", svc.count())
	}
	req, body := svc.requests[0], svc.bodies[0]
	h := req.Header
	if h.Get("Urgency") != "high" || h.Get("Topic") != "cry" ||
		h.Get("Content-Encoding") != "aes128gcm" || h.Get("TTL") != "300" {
		t.Fatalf("headers: Urgency=%q Topic=%q Content-Encoding=%q TTL=%q",
			h.Get("Urgency"), h.Get("Topic"), h.Get("Content-Encoding"), h.Get("TTL"))
	}
	if !strings.HasPrefix(h.Get("Authorization"), "vapid t=") || !strings.Contains(h.Get("Authorization"), ", k="+r.n.PublicKey()) {
		t.Fatalf("Authorization %q", h.Get("Authorization"))
	}

	plain, err := decryptAsTheBrowser(body, r.ua, r.auth)
	if err != nil {
		t.Fatalf("the device cannot decrypt what it was sent: %v", err)
	}
	var msg struct {
		WebPush      int `json:"web_push"`
		Notification struct {
			Title, Body, Lang, Navigate, Tag string
		} `json:"notification"`
	}
	if err := json.Unmarshal(plain, &msg); err != nil {
		t.Fatalf("%v: %s", err, plain)
	}
	it := readCatalogue(t, "it")
	n := msg.Notification
	if msg.WebPush != 8030 || n.Title != "PAT Monitor" || n.Body != it["viewer.alert.cry"] || n.Lang != "it" || n.Tag != "cry" {
		t.Fatalf("message %s", plain)
	}
	if !strings.HasPrefix(n.Navigate, "https://patmon-1a2b3c.quercia-lieve.ts.net/?n=") {
		t.Fatalf("a tap would open %q, not the page that subscribed", n.Navigate)
	}
}

// **A fault recovers on the phone, and an event does not.** The recovery of
// a fault carries the fault's topic and tag, so it takes its place; an
// event's "it has stopped" would take the place of the cry, which is the
// notification whoever looks at the phone later wants to find.
func TestAFaultRecoversAndAnEventDoesNot(t *testing.T) {
	svc := newFakeService(t, func(int) (int, http.Header) { return 201, nil })
	r := newRig(t, svc)
	r.subscribe(t, svc, "/push/one", "en")

	r.n.Alerted(nil, []alerts.Code{alerts.Cry, alerts.Motion, alerts.CaptureStopped, alerts.DiskFull})
	r.n.wg.Wait()
	var topics []string
	for _, req := range svc.requests {
		topics = append(topics, req.Header.Get("Topic"))
	}
	sorted := slices.Clone(topics)
	slices.Sort(sorted)
	got := strings.Join(sorted, ",")
	if got != "capture-stopped,disk-full" {
		t.Fatalf("recoveries sent for %q, want the fault and the notice only", got)
	}
	en := readCatalogue(t, "en")
	for i, body := range svc.bodies {
		plain, err := decryptAsTheBrowser(body, r.ua, r.auth)
		if err != nil {
			t.Fatal(err)
		}
		want := en["viewer.recovered."+topics[i]]
		if !strings.Contains(string(plain), want) {
			t.Fatalf("recovery %s says %s, want %q", topics[i], plain, want)
		}
	}
}

func TestNoSubscriptionIsNoRequest(t *testing.T) {
	svc := newFakeService(t, func(int) (int, http.Header) { return 201, nil })
	r := newRig(t, svc)
	r.n.Alerted([]alerts.Alert{{ID: 1, Code: alerts.CaptureStopped, Level: alerts.Fault}}, nil)
	r.n.wg.Wait()
	if svc.count() != 0 {
		t.Fatalf("%d requests with nobody subscribed", svc.count())
	}
}

// readCatalogue is a catalogue as the file says it, not through the package
// the notifier uses.
func readCatalogue(t *testing.T, lang string) map[string]string {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("..", "i18n", "catalogs", lang+".json"))
	if err != nil {
		t.Fatal(err)
	}
	var raw map[string]any
	if err := json.Unmarshal(data, &raw); err != nil {
		t.Fatal(err)
	}
	out := map[string]string{}
	for k, v := range raw {
		if s, ok := v.(string); ok {
			out[k] = s
		}
	}
	return out
}

// **An endpoint is held to what a push service is**, and the addresses a page
// could name to make the monitor call into the house are refused.
func TestAnEndpointInsideTheHouseIsRefused(t *testing.T) {
	for _, raw := range []string{
		"http://fcm.googleapis.com/fcm/send/x",
		"https://127.0.0.1/x",
		"https://[::1]/x",
		"https://192.168.1.1/x",
		"https://10.0.0.1/x",
		"https://100.101.102.103/x",
		"https://[fd7a:115c:a1e0::1]/x",
		"https://169.254.1.1/x",
		"https://fcm.googleapis.com:8443/x",
		"https://user:pw@fcm.googleapis.com/x",
		"ftp://fcm.googleapis.com/x",
		"https:///x",
	} {
		if _, err := endpointURL(raw); err == nil {
			t.Errorf("%s was accepted", raw)
		}
	}
	for _, raw := range []string{
		"https://fcm.googleapis.com/fcm/send/abc:def",
		"https://web.push.apple.com/QGuQyavXutnMH",
		"https://updates.push.services.mozilla.com/wpush/v2/gAAAA",
		"https://wns2-par02p.notify.windows.com/w/?token=BQYAAAB",
		"https://push.example:443/x",
	} {
		if _, err := endpointURL(raw); err != nil {
			t.Errorf("%s was refused: %v", raw, err)
		}
	}
	// And at the moment of dialling, which is where a name that resolves into
	// the house is caught.
	for _, addr := range []string{"127.0.0.1:443", "192.168.1.20:443", "100.64.0.1:443", "[fd7a:115c:a1e0::5]:443"} {
		if dialPublic("tcp", addr, nil) == nil {
			t.Errorf("dialling %s was allowed", addr)
		}
	}
	if err := dialPublic("tcp", "142.250.180.10:443", nil); err != nil {
		t.Errorf("a public address was refused: %v", err)
	}
	if publicAddress(netip.MustParseAddr("::ffff:192.168.1.1")) {
		t.Error("an IPv4-mapped private address counts as public")
	}
}
