package server

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

// **"Try again in 0 seconds" is the sentence this rounding exists to prevent**,
// and it used to produce it.
//
// The wait travels to the page as a whole number of seconds, because a duration
// written inside the sentence belongs to one language as much as the words do.
// It was rounded to the **nearest** second, so anything under half a second
// travelled as zero, and `i18n.js` then composed `err.too-many` with n = 0:
// "Too many attempts. Try again in 0 seconds."
//
// The check in `TErr` cannot see it. That one guards an **absent** retryAfter —
// its own comment says so, in those words — and a zero that arrived is not
// absent. The defect was on this side, in the rounding.
//
// It is reachable by exactly the caller the wait exists for. `limiter.retryAfter`
// answers `time.Until(lockedUntil)`, a continuous duration, so every lock passes
// through its last half second, and whoever is retrying in a tight loop is who
// lands there.
func TestTheWaitIsNeverAnnouncedAsZeroSeconds(t *testing.T) {
	for _, d := range []time.Duration{
		time.Nanosecond, time.Millisecond, 400 * time.Millisecond,
		499 * time.Millisecond, 500 * time.Millisecond, time.Second,
	} {
		if got := retryAfterSeconds(d); got < 1 {
			t.Errorf("%v is announced as %d seconds: the page says \"try again in "+
				"0 seconds\", which invites the retry that is refused again", d, got)
		}
	}
}

// It rounds **up**, which is the honest direction: a second announced where four
// hundred milliseconds are left costs a wait nobody notices, and the other way
// round costs the loop above.
func TestTheWaitRoundsUp(t *testing.T) {
	for _, c := range []struct {
		d    time.Duration
		want int
	}{
		{400 * time.Millisecond, 1},
		{time.Second, 1},
		{1500 * time.Millisecond, 2},
		{90 * time.Second, 90},
		{90500 * time.Millisecond, 91},
	} {
		if got := retryAfterSeconds(c.d); got != c.want {
			t.Errorf("%v announced as %d, wanted %d", c.d, got, c.want)
		}
	}
	// And nothing is announced when nothing is left: the caller only writes the
	// field when the wait is positive, and a zero here must stay a zero.
	if got := retryAfterSeconds(0); got != 0 {
		t.Errorf("a wait of zero announced %d", got)
	}
}

// **The header says the number the body says**, on both roads.
//
// `Retry-After` was written by hand at each refusal as the whole seconds plus
// one, while the body went through retryAfterSeconds: a lockout is whole
// seconds, so a caller told to wait one second by the page was told two by the
// header. The attempt already in flight is the refusal whose wait is exactly one
// second, busyRetry, and it is the one held open here.
//
// **The defect was put back and this test fails with it**: with the header
// written by hand again, it reads "2" on both roads.
func TestTheRetryAfterHeaderSaysWhatTheBodySays(t *testing.T) {
	s, _ := serverWithPassword(t, "a-long-password")
	held := func(r *http.Request) *http.Request {
		t.Helper()
		release, wait := s.limiter.begin(clientKey(r))
		if wait > 0 {
			t.Fatalf("the address was refused before the test held it: %v", wait)
		}
		t.Cleanup(release)
		return r
	}

	w := httptest.NewRecorder()
	s.Handler().ServeHTTP(w, held(loginReq("a-wrong-guess", fromTheLAN)))
	if w.Code != http.StatusTooManyRequests {
		t.Fatalf("a second attempt in flight answered %d, wanted 429", w.Code)
	}
	var body struct {
		RetryAfter int `json:"retryAfter"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if got := w.Header().Get("Retry-After"); got != "1" || body.RetryAfter != 1 {
		t.Errorf("the header says %q and the body %d: both should say 1", got, body.RetryAfter)
	}

	w = httptest.NewRecorder()
	s.Handler().ServeHTTP(w, held(formPost("/api/login", map[string]string{"password": "a-wrong-guess"})))
	if w.Code != http.StatusSeeOther {
		t.Fatalf("the form answered %d, wanted 303", w.Code)
	}
	if got := w.Header().Get("Retry-After"); got != "1" {
		t.Errorf("the redirect carries Retry-After %q, wanted 1", got)
	}
}
