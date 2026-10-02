package push

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"io"
	"log/slog"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"patmonitor/internal/alerts"
	"patmonitor/internal/guard"
	"patmonitor/internal/i18n"
	"patmonitor/internal/version"
)

// How long a message is worth delivering, which is how long a push service
// is asked to keep it for a device that is off.
//
// **These two are chosen and not measured**, and they are written as such:
// an event is news for minutes — a cry delivered after five of them is about
// a room that has since changed — while a fault is a state, and knowing an
// hour late that the camera stopped is still knowing. The receipt is what can
// measure them.
const (
	eventTTL = 5 * time.Minute
	stateTTL = time.Hour
	testTTL  = time.Minute
)

// maxInFlight bounds the deliveries under way at once: a subscription list
// at its bound, and a burst of alerts each retrying inside its TTL, must not
// become a goroutine per retry.
const maxInFlight = 64

// Notifier turns the alerts into notifications, and answers the page's
// requests about its own subscription.
type Notifier struct {
	store  *Store
	send   *sender
	log    *slog.Logger
	random io.Reader

	ctx    context.Context
	cancel context.CancelFunc
	wg     sync.WaitGroup
	slots  chan struct{}

	dictMu sync.Mutex
	dicts  map[string]*i18n.Dictionary

	// sent is when each message went out, by id, for the receipt: what the
	// log then says is how long the notification took to be shown.
	sentMu sync.Mutex
	sent   map[string]sentMessage

	// latest is the news under way for each code, so that newer news on the
	// same code stops it and goes out after it.
	latestMu sync.Mutex
	latest   map[alerts.Code]newsUnderWay
}

type newsUnderWay struct {
	supersede context.CancelFunc
	done      chan struct{}
}

type sentMessage struct {
	code alerts.Code
	at   time.Time
	// shown is how many devices have sent a receipt for it.
	shown int
}

// New opens the store in dir and makes a notifier over it.
func New(dir string, log *slog.Logger) (*Notifier, error) {
	store, err := OpenStore(dir, rand.Reader)
	if err != nil {
		return nil, err
	}
	return newNotifier(store, newSender(newSigner(store.Key(), rand.Reader), rand.Reader), log, rand.Reader), nil
}

func newNotifier(store *Store, send *sender, log *slog.Logger, random io.Reader) *Notifier {
	if log == nil {
		log = slog.Default()
	}
	ctx, cancel := context.WithCancel(context.Background())
	return &Notifier{
		store: store, send: send, log: log, random: random,
		ctx: ctx, cancel: cancel,
		slots:  make(chan struct{}, maxInFlight),
		dicts:  map[string]*i18n.Dictionary{},
		sent:   map[string]sentMessage{},
		latest: map[alerts.Code]newsUnderWay{},
	}
}

// Close stops the deliveries under way, waits for them, and writes the
// delivery times kept in memory.
func (n *Notifier) Close() {
	n.cancel()
	n.wg.Wait()
	if err := n.store.Flush(); err != nil {
		n.log.Debug("the delivery times were not written", "error", err)
	}
}

// PublicKey is what a browser subscribes with.
func (n *Notifier) PublicKey() string { return n.send.sign.publicKey() }

// Subscribe adds or refreshes a subscription.
func (n *Notifier) Subscribe(sub Subscription) error {
	_, known := n.store.Get(sub.Endpoint)
	if err := n.store.Put(sub, time.Now()); err != nil {
		return err
	}
	if !known {
		n.log.Info("notifications turned on for a device", "service", serviceOf(sub.Endpoint),
			"lang", sub.Lang, "devices", len(n.store.All()))
	}
	return nil
}

// LanguageOf is the language a subscription was made in.
func (n *Notifier) LanguageOf(endpoint string) (string, bool) {
	sub, ok := n.store.Get(endpoint)
	return sub.Lang, ok
}

// Unsubscribe drops a subscription.
func (n *Notifier) Unsubscribe(endpoint string) error {
	removed, err := n.store.Remove(endpoint)
	if removed {
		n.log.Info("notifications turned off for a device", "service", serviceOf(endpoint),
			"devices", len(n.store.All()))
	}
	return err
}

// Test sends one notification to one subscription, now, and says what the
// push service answered. **Once and not retried**: whoever pressed is waiting
// for the answer, and the answer is the point.
func (n *Notifier) Test(ctx context.Context, endpoint string) Outcome {
	sub, ok := n.store.Get(endpoint)
	if !ok {
		return Gone
	}
	id := n.newID()
	m := message{topic: "test", ttl: testTTL,
		payload: n.payload(sub, "push.test.body", "test", id, time.Now(), false)}
	// **Remembered before it is sent, not after it is taken**: measured, the
	// receipt of a test comes back from the device before the push service's
	// 201 has reached us, and remembered afterwards it found nothing.
	n.remember(id, "test")
	a := n.send.once(ctx, sub, m)
	n.settle(sub, a)
	n.log.Info("test notification", "service", serviceOf(endpoint), "answer", a.String(),
		"reason", shortReason(a))
	return a.outcome
}

// Seen is the receipt a service worker sends once it has shown a message.
//
// **The id is kept after the first receipt**, because one message goes to
// every subscription under one id: forgetting it there left the second
// device's delivery unmeasured. It goes with the others that `remember`
// clears, once nobody can answer for it any more.
func (n *Notifier) Seen(id string) {
	n.sentMu.Lock()
	m, ok := n.sent[id]
	if ok {
		m.shown++
		n.sent[id] = m
	}
	n.sentMu.Unlock()
	if !ok {
		return
	}
	n.log.Info("notification shown", "code", m.code,
		"after", time.Since(m.at).Round(10*time.Millisecond), "receipt", m.shown)
}

// Alerted is the watching round's news: the alerts that appeared and the ones
// that cleared. **It does not wait for the network** — the round runs once a
// second and a push service can take twenty to answer — so each piece of news
// goes out on its own.
//
// A recovery is sent for a fault or a notice, and not for an event: "it has
// stopped" would replace "a baby crying" in the list of notifications, and
// the parent who looks at the phone ten minutes later wants to find the cry.
//
// **A recovery is quiet.** It takes the fault's place on the phone and does
// not sound: a fault is worth waking somebody for at three in the morning, and
// the news that it has cleared is not.
func (n *Notifier) Alerted(appeared []alerts.Alert, recovered []alerts.Code) {
	for _, a := range appeared {
		ttl := stateTTL
		if a.Level == alerts.Event {
			ttl = eventTTL
		}
		n.dispatch(a.Code, "viewer.alert."+string(a.Code), ttl, false)
	}
	for _, c := range recovered {
		if alerts.LevelOf(c) == alerts.Event {
			continue
		}
		n.dispatch(c, "viewer.recovered."+string(c), stateTTL, true)
	}
}

// dispatch sends one piece of news to every subscription, and writes one line
// about it when every delivery has finished.
//
// **News on a code goes out after the news before it, never beside it.**
// "The microphone is missing" and "it works now" share a topic and a tag,
// so whichever arrives last is what the phone keeps; sent side by side, a
// fault still being retried reached the service after its own recovery. The
// newer one stops the older one's retries, waits for the attempt under way to
// end, and only then is sent. The push services do not promise an order
// either, and that part is not ours.
//
// **And news overtaken while it waited is not sent at all**, nor news still
// waiting when the monitor closes: only the newest says what holds, and a
// service that hangs would otherwise release a queue of stale faults, each
// sounding. The wait is for every device of the news before, so one service
// that hangs holds the others back for the length of one attempt, the
// client's twenty seconds; the devices are read once the wait is over.
//
// The time it carries is when it happened, taken here: a message held by a
// service for a phone that was off shows when it arrived otherwise.
func (n *Notifier) dispatch(code alerts.Code, key string, ttl time.Duration, quiet bool) {
	if len(n.store.All()) == 0 {
		return
	}
	at := time.Now()
	superseded, supersede := context.WithCancel(n.ctx)
	done := make(chan struct{})
	n.latestMu.Lock()
	before := n.latest[code]
	n.latest[code] = newsUnderWay{supersede: supersede, done: done}
	n.latestMu.Unlock()
	if before.supersede != nil {
		before.supersede()
	}
	n.wg.Add(1)
	guard.Go(n.log, "a notification", func() {
		defer n.wg.Done()
		defer func() {
			n.latestMu.Lock()
			if n.latest[code].done == done {
				delete(n.latest, code)
			}
			n.latestMu.Unlock()
			supersede()
			close(done)
		}()
		if before.done != nil {
			<-before.done
		}
		if superseded.Err() != nil {
			return
		}
		subs := n.store.All()
		id := n.newID()
		n.remember(id, code)
		answers := make([]answer, len(subs))
		var each sync.WaitGroup
		for i, sub := range subs {
			select {
			case n.slots <- struct{}{}:
			default:
				answers[i] = answer{outcome: Unavailable, reason: "too many deliveries under way"}
				continue
			}
			each.Add(1)
			guard.Go(n.log, "a notification to one device", func() {
				defer each.Done()
				defer func() { <-n.slots }()
				m := message{topic: string(code), ttl: ttl, payload: n.payload(sub, key, code, id, at, quiet)}
				answers[i] = n.send.deliver(n.ctx, superseded, sub, m)
				n.settle(sub, answers[i])
			})
		}
		each.Wait()
		n.report(code, answers)
	})
}

// settle applies what an answer means for the list.
func (n *Notifier) settle(sub Subscription, a answer) {
	switch a.outcome {
	case Delivered:
		n.store.Delivered(sub.Endpoint, time.Now())
	case Gone:
		if removed, err := n.store.Remove(sub.Endpoint); removed {
			n.log.Info("a device's subscription has ended and was removed",
				"service", serviceOf(sub.Endpoint), "answer", a.String())
		} else if err != nil {
			n.log.Warn("an ended subscription could not be removed", "error", err)
		}
	}
}

// report is the one line per piece of news: how many devices, and what each
// kind of answer was. **Not one line per device and per attempt**, which
// would put the volume of the log in the hands of how many phones there are
// and how often a service is down.
func (n *Notifier) report(code alerts.Code, answers []answer) {
	counts := map[Outcome]int{}
	var refused answer
	superseded := 0
	for _, a := range answers {
		counts[a.outcome]++
		if a.superseded {
			superseded++
		}
		if (a.outcome == Refused || a.outcome == TooLarge) && refused.outcome == "" {
			refused = a
		}
	}
	var parts []string
	for o, c := range counts {
		parts = append(parts, string(o)+"="+strconv.Itoa(c))
	}
	sort.Strings(parts)
	attrs := []any{"code", code, "devices", len(answers), "answers", strings.Join(parts, " ")}
	if superseded > 0 {
		attrs = append(attrs, "superseded", superseded)
	}
	if refused.outcome != "" {
		// A refusal is ours to fix, and the service's own reason is the only
		// place the cause is written.
		n.log.Warn("notification refused by a push service", append(attrs,
			"status", refused.status, "reason", shortReason(refused))...)
		return
	}
	n.log.Info("notification", attrs...)
}

// payload composes the message in the subscription's language.
//
// **It is the Declarative Web Push shape, for every browser.** Safari on iOS
// 18.4 and later shows it by itself, even if the service worker fails; the
// others hand the same JSON to the service worker, which shows it. `mutable`
// lets our worker handle it where both exist, so that one code path shows
// every notification and sends the receipt.
//
// `silent` and `timestamp` are the Notifications API's own options, which the
// declarative shape carries: a quiet message replaces without sounding, and
// the time shown is `at` rather than the arrival. Whether a phone honours
// either is the phone's; neither stops it showing.
func (n *Notifier) payload(sub Subscription, key string, code alerts.Code, id string, at time.Time, quiet bool) []byte {
	d := n.dictionary(sub.Lang)
	body, _ := json.Marshal(map[string]any{
		"web_push": 8030,
		"notification": map[string]any{
			"title":     version.Product,
			"body":      d.T(key),
			"lang":      d.Language(),
			"navigate":  sub.Origin + "/?n=" + id,
			"tag":       string(code),
			"silent":    quiet,
			"timestamp": at.UnixMilli(),
			"mutable":   true,
		},
	})
	return body
}

// dictionary is the catalogue for a language, opened once.
func (n *Notifier) dictionary(lang string) *i18n.Dictionary {
	n.dictMu.Lock()
	defer n.dictMu.Unlock()
	if d, ok := n.dicts[lang]; ok {
		return d
	}
	d := i18n.Open([]string{lang})
	n.dicts[lang] = d
	return d
}

// remember keeps a message's send time for its receipt, and forgets the ones
// nobody will answer for: a phone that is off never sends one.
func (n *Notifier) remember(id string, code alerts.Code) {
	now := time.Now()
	n.sentMu.Lock()
	defer n.sentMu.Unlock()
	for k, m := range n.sent {
		if now.Sub(m.at) > stateTTL || len(n.sent) > 256 {
			delete(n.sent, k)
		}
	}
	n.sent[id] = sentMessage{code: code, at: now}
}

func (n *Notifier) newID() string {
	b := make([]byte, 8)
	_, _ = io.ReadFull(n.random, b)
	return hex.EncodeToString(b)
}

// serviceOf is the push service's host, which is what the log may say about
// an endpoint: the rest of it is the device's address at that service.
func serviceOf(endpoint string) string {
	if u, err := endpointURL(endpoint); err == nil {
		return u.Hostname()
	}
	return "?"
}

// shortReason is a refusal's reason cut to a line.
func shortReason(a answer) string {
	r := strings.Join(strings.Fields(a.reason), " ")
	if len(r) > 160 {
		r = r[:160] + "…"
	}
	return r
}
