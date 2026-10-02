package push

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

// Outcome is what a push service answered, as a code: the page shows it and
// the log counts it, and neither should have to read a status number.
type Outcome string

const (
	// Delivered: the service took the message. **It is not "it was shown"**
	// — a service holds a message for a phone that is off, and a browser drops
	// one it cannot decrypt — and only the receipt says the second.
	Delivered Outcome = "delivered"
	// Gone: the subscription no longer exists (404, 410). It is removed.
	Gone Outcome = "gone"
	// TooLarge: the service refused the size (413). That is a defect here.
	TooLarge Outcome = "too-large"
	// Throttled: too many messages (429). The wait it asks for is honoured.
	Throttled Outcome = "throttled"
	// Refused: the service refused the request (400, 401, 403 and the other
	// 4xx) — the signature, the key, a header. It is a configuration fault
	// and says so in the log with the service's own reason.
	Refused Outcome = "refused"
	// Unavailable: no answer, or a 5xx. Tried again while the message is
	// still worth delivering.
	Unavailable Outcome = "unavailable"
	// NotAllowed: an endpoint we will not write to. See endpointURL.
	NotAllowed Outcome = "not-allowed"
)

// AllOutcomes is the complete list, for the guard that wants a word for each
// in every catalogue.
func AllOutcomes() []Outcome {
	return []Outcome{Delivered, Gone, TooLarge, Throttled, Refused, Unavailable, NotAllowed}
}

// message is one notification, before it is encrypted for anybody.
type message struct {
	// topic replaces an undelivered message with the same topic: a
	// "back to normal" still in the queue takes the place of the fault.
	topic   string
	ttl     time.Duration
	payload []byte
}

// answer is one push service's reply.
type answer struct {
	outcome    Outcome
	status     int
	retryAfter time.Duration
	// reason is the start of the body of a refusal: the services say why
	// there, Mozilla at length and Apple in one word.
	reason string
	// superseded: the retries stopped because newer news took this one's place.
	superseded bool
}

// sender writes messages to push services.
type sender struct {
	client *http.Client
	// admits is the endpoint check. It is a field so that a test server on
	// loopback can be written to; everywhere else it is endpointURL.
	admits func(string) (*url.URL, error)
	sign   *signer
	random io.Reader
	now    func() time.Time
	// sleep waits, or returns false if the context ends first.
	sleep func(ctx context.Context, d time.Duration) bool
}

func newSender(sign *signer, random io.Reader) *sender {
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.DialContext = publicDialer()
	transport.Proxy = nil // a proxy would be dialled instead, and checked instead
	return &sender{
		client: &http.Client{
			Transport: transport,
			Timeout:   20 * time.Second,
			// A push service answers; it does not send us elsewhere, and a
			// redirect is a second address nobody checked.
			CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
		},
		admits: endpointURL,
		sign:   sign,
		random: random,
		now:    time.Now,
		sleep: func(ctx context.Context, d time.Duration) bool {
			t := time.NewTimer(d)
			defer t.Stop()
			select {
			case <-t.C:
				return true
			case <-ctx.Done():
				return false
			}
		},
	}
}

// once sends one message to one browser and says what came back.
func (s *sender) once(ctx context.Context, sub Subscription, m message) answer {
	u, err := s.admits(sub.Endpoint)
	if err != nil {
		return answer{outcome: NotAllowed, reason: err.Error()}
	}
	pub, auth, err := sub.keys()
	if err != nil {
		return answer{outcome: NotAllowed, reason: err.Error()}
	}
	body, err := encrypt(m.payload, pub, auth, s.random)
	if err != nil {
		if errors.Is(err, ErrTooLarge) {
			return answer{outcome: TooLarge, reason: err.Error()}
		}
		return answer{outcome: NotAllowed, reason: err.Error()}
	}
	authz, err := s.sign.authorization(u, s.now())
	if err != nil {
		return answer{outcome: Refused, reason: err.Error()}
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, u.String(), bytes.NewReader(body))
	if err != nil {
		return answer{outcome: NotAllowed, reason: err.Error()}
	}
	h := req.Header
	h.Set("TTL", strconv.Itoa(int(m.ttl/time.Second)))
	// **Every message is urgent**, because every message is one of the
	// alerts: `high` is what FCM delivers through Doze and what Apple
	// delivers at once.
	h.Set("Urgency", "high")
	if m.topic != "" {
		h.Set("Topic", m.topic)
	}
	h.Set("Content-Encoding", "aes128gcm")
	h.Set("Content-Type", "application/octet-stream")
	h.Set("Authorization", authz)

	resp, err := s.client.Do(req)
	if err != nil {
		return answer{outcome: Unavailable, reason: err.Error()}
	}
	defer resp.Body.Close()
	reason, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
	a := answer{status: resp.StatusCode, reason: strings.TrimSpace(string(reason))}
	switch c := resp.StatusCode; {
	case c >= 200 && c < 300:
		a.outcome = Delivered
	case c == http.StatusNotFound || c == http.StatusGone:
		a.outcome = Gone
	case c == http.StatusRequestEntityTooLarge:
		a.outcome = TooLarge
	case c == http.StatusTooManyRequests:
		a.outcome = Throttled
		a.retryAfter = retryAfter(resp.Header.Get("Retry-After"), s.now())
	case c >= 500:
		a.outcome = Unavailable
	default:
		a.outcome = Refused
	}
	return a
}

// deliver sends and, when the answer is one that passes, sends again — **but
// only while the message is still worth having.** The TTL is how long the
// service would have kept it; a cry retried past that arrives as news about a
// room that has since changed.
//
// **And only while nothing newer has been said about it.** A fault retried
// past its own recovery arrives after it, and with the same tag it takes its
// place: the phone then says the microphone is missing while it works.
//
// **Two contexts, and they do not do the same thing.** `ctx` carries every
// attempt and ends only with the monitor. `waits` is `ctx` or one derived from
// it, and it ends the waits between attempts and nothing else: cutting an
// attempt under way would leave the older message neither with the service
// nor abandoned when the newer one leaves, which is the order this keeps.
func (s *sender) deliver(ctx, waits context.Context, sub Subscription, m message) answer {
	start := s.now()
	wait := 2 * time.Second
	for {
		a := s.once(ctx, sub, m)
		if a.outcome != Unavailable && a.outcome != Throttled {
			return a
		}
		next := wait
		if a.outcome == Throttled && a.retryAfter > 0 {
			next = a.retryAfter
		}
		// What is left of the TTL is what the service would still hold it for.
		left := m.ttl - s.now().Sub(start)
		if next >= left {
			return a
		}
		if !s.sleep(waits, next) {
			a.superseded = ctx.Err() == nil
			return a
		}
		wait = min(wait*4, 2*time.Minute)
	}
}

// retryAfter reads the header in either of its two forms.
func retryAfter(v string, now time.Time) time.Duration {
	v = strings.TrimSpace(v)
	if v == "" {
		return 0
	}
	if n, err := strconv.Atoi(v); err == nil && n >= 0 {
		return time.Duration(n) * time.Second
	}
	if t, err := http.ParseTime(v); err == nil && t.After(now) {
		return t.Sub(now)
	}
	return 0
}

// String is the answer as a log value.
func (a answer) String() string {
	if a.status == 0 {
		return string(a.outcome)
	}
	return fmt.Sprintf("%s (%d)", a.outcome, a.status)
}
