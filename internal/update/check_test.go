package update

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"patmonitor/internal/version"
)

// serve stands in for GitHub, answering one release document.
//
// It counts the requests, because half of what this package promises is about
// **how often** it asks and what it sends the second time.
type fake struct {
	body     release
	etag     string
	requests int
	lastReq  http.Header
	status   int
	raw      string
}

func (f *fake) start(t *testing.T) *Checker {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		f.requests++
		f.lastReq = r.Header.Clone()
		if f.etag != "" {
			if r.Header.Get("If-None-Match") == f.etag {
				w.WriteHeader(http.StatusNotModified)
				return
			}
			w.Header().Set("ETag", f.etag)
		}
		if f.status != 0 && f.status != http.StatusOK {
			w.WriteHeader(f.status)
			return
		}
		if f.raw != "" {
			_, _ = w.Write([]byte(f.raw))
			return
		}
		_ = json.NewEncoder(w).Encode(f.body)
	}))
	t.Cleanup(srv.Close)
	return &Checker{URL: srv.URL}
}

// notModified keeps a build from looking like a development one.
//
// version.Modified is a package variable the linker stamps, and Check refuses
// to report anything while it is set. A test that forgot this would pass for
// the wrong reason — every case answering Unknown — so it is cleared here and
// its own behaviour is asserted separately below.
func notModified(t *testing.T) {
	t.Helper()
	was := version.Modified
	version.Modified = ""
	t.Cleanup(func() { version.Modified = was })
}

// A newer release is offered, with the number and the page GitHub named.
func TestANewerReleaseIsOffered(t *testing.T) {
	notModified(t)
	f := &fake{body: release{TagName: "v99.0.0", HTMLURL: "https://github.com/Nenco79/PAT-Monitor/releases/tag/v99.0.0"}}
	c := f.start(t)

	st, err := c.Check(context.Background())
	if err != nil {
		t.Fatalf("check failed: %v", err)
	}
	if st.Code != Available {
		t.Fatalf("code %q instead of %q", st.Code, Available)
	}
	if st.Version != "99.0.0" {
		t.Errorf("version %q: the tag's leading v must not survive", st.Version)
	}
	if st.URL != "https://github.com/Nenco79/PAT-Monitor/releases/tag/v99.0.0" {
		t.Errorf("url %q is not the one the release named", st.URL)
	}
}

// The running version, and an older one, are both "current".
//
// The second is not hypothetical: a release can be deleted, or a tag moved
// backwards, and answering Available to a version older than the running one
// would send somebody to download what they are already past.
func TestTheRunningVersionAndOlderOnesAreCurrent(t *testing.T) {
	notModified(t)
	for _, tag := range []string{"v" + version.Number, version.Number, "v0.0.1"} {
		t.Run(tag, func(t *testing.T) {
			f := &fake{body: release{TagName: tag, HTMLURL: "https://github.com/Nenco79/PAT-Monitor/releases/latest"}}
			st, err := f.start(t).Check(context.Background())
			if err != nil {
				t.Fatalf("check failed: %v", err)
			}
			if st.Code != Current {
				t.Fatalf("code %q instead of %q", st.Code, Current)
			}
			if st.Version != "" || st.URL != "" {
				t.Errorf("a current answer carries %q / %q", st.Version, st.URL)
			}
		})
	}
}

// **A pre-release is never offered, even if the endpoint hands one over.**
//
// The policy is a checkbox on GitHub and the endpoint is documented to honour
// it; this asserts the belt underneath, which is the half that keeps the
// promise holding if somebody else's behaviour changes. The same for a draft.
func TestAPrereleaseIsNeverOffered(t *testing.T) {
	notModified(t)
	for _, c := range []struct {
		name string
		rel  release
	}{
		{"prerelease", release{TagName: "v99.0.0", Prerelease: true}},
		{"draft", release{TagName: "v99.0.0", Draft: true}},
	} {
		t.Run(c.name, func(t *testing.T) {
			c.rel.HTMLURL = "https://github.com/Nenco79/PAT-Monitor/releases/tag/v99.0.0"
			st, err := (&fake{body: c.rel}).start(t).Check(context.Background())
			if err != nil {
				t.Fatalf("check failed: %v", err)
			}
			if st.Code == Available {
				t.Fatalf("%s %q was offered", c.name, st.Version)
			}
		})
	}
}

// **Nothing that fails is ever reported as current**, which is the one claim
// this package must not make out of thin air.
//
// Four ways of getting no answer, and every one of them has to end in Unknown:
// a refusal, a rate limit, a body that is not JSON, and a release with no tag.
// Rendered as "up to date", any of them would tell somebody their monitor is
// current on the night the fix they need was published.
func TestNoFailureIsEverReportedAsCurrent(t *testing.T) {
	notModified(t)
	for _, c := range []struct {
		name string
		f    *fake
	}{
		{"not found", &fake{status: http.StatusNotFound}},
		{"rate limited", &fake{status: http.StatusForbidden}},
		{"server error", &fake{status: http.StatusInternalServerError}},
		{"not json", &fake{raw: "<html>no</html>"}},
		{"no tag", &fake{body: release{HTMLURL: "https://github.com/Nenco79/PAT-Monitor/releases/latest"}}},
	} {
		t.Run(c.name, func(t *testing.T) {
			st, _ := c.f.start(t).Check(context.Background())
			if st.Code != Unknown {
				t.Fatalf("code %q instead of %q", st.Code, Unknown)
			}
		})
	}
}

// An unreachable server is Unknown too, and it is the ordinary case: a laptop
// with the network down, a house with the router off.
func TestNoNetworkIsUnknown(t *testing.T) {
	notModified(t)
	srv := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	url := srv.URL
	srv.Close() // nothing is listening there now

	c := &Checker{URL: url}
	st, err := c.Check(context.Background())
	if err == nil {
		t.Error("a dead server produced no error")
	}
	if st.Code != Unknown {
		t.Fatalf("code %q instead of %q", st.Code, Unknown)
	}
}

// **What was learned survives a failed attempt.**
//
// A monitor that found 99.0.0 yesterday still knows about it through a night
// with no network. Forgetting would turn every hiccup into silence about a fix
// that has not gone anywhere.
func TestAFailedAttemptDoesNotForgetWhatWasFound(t *testing.T) {
	notModified(t)
	f := &fake{body: release{TagName: "v99.0.0", HTMLURL: "https://github.com/Nenco79/PAT-Monitor/releases/tag/v99.0.0"}}
	c := f.start(t)

	if st, err := c.Check(context.Background()); err != nil || st.Code != Available {
		t.Fatalf("first check: %v / %q", err, st.Code)
	}
	f.status = http.StatusInternalServerError
	st, err := c.Check(context.Background())
	if err == nil {
		t.Error("the refusal produced no error")
	}
	if st.Code != Available || st.Version != "99.0.0" {
		t.Fatalf("the earlier answer was lost: %q %q", st.Code, st.Version)
	}
	if c.State().Code != Available {
		t.Error("State() lost it too")
	}
}

// The second ask sends the tag back, and a 304 keeps the answer.
//
// This is what makes a daily check cost nothing against a rate limit shared by
// every installation behind one address.
func TestTheSecondAskIsConditional(t *testing.T) {
	notModified(t)
	f := &fake{
		body: release{TagName: "v99.0.0", HTMLURL: "https://github.com/Nenco79/PAT-Monitor/releases/tag/v99.0.0"},
		etag: `W/"abc123"`,
	}
	c := f.start(t)

	if _, err := c.Check(context.Background()); err != nil {
		t.Fatalf("first check: %v", err)
	}
	st, err := c.Check(context.Background())
	if err != nil {
		t.Fatalf("second check: %v", err)
	}
	if got := f.lastReq.Get("If-None-Match"); got != f.etag {
		t.Errorf("If-None-Match %q instead of %q", got, f.etag)
	}
	if st.Code != Available || st.Version != "99.0.0" {
		t.Fatalf("a 304 lost the answer: %q %q", st.Code, st.Version)
	}
	if f.requests != 2 {
		t.Errorf("%d requests instead of 2", f.requests)
	}
}

// **A body that will not parse must not poison every later check.**
//
// The ETag is recorded only once the document has been read. Were it stored
// first, one malformed answer would be met with a 304 for ever after — the
// check switched off for the life of the process, in silence.
func TestAnUnreadableAnswerDoesNotSwitchTheCheckOff(t *testing.T) {
	notModified(t)
	f := &fake{raw: "<html>no</html>", etag: `W/"abc123"`}
	c := f.start(t)

	if st, _ := c.Check(context.Background()); st.Code != Unknown {
		t.Fatalf("code %q instead of %q", st.Code, Unknown)
	}
	// The server is fixed; the next ask must be able to see it.
	f.raw = ""
	f.body = release{TagName: "v99.0.0", HTMLURL: "https://github.com/Nenco79/PAT-Monitor/releases/tag/v99.0.0"}

	st, err := c.Check(context.Background())
	if err != nil {
		t.Fatalf("second check: %v", err)
	}
	if st.Code != Available {
		t.Fatalf("code %q: the bad answer's tag was kept and the check went deaf", st.Code)
	}
}

// The headers GitHub requires are sent.
//
// A request with no User-Agent is refused outright, which would make the check
// fail for everybody, always — and look exactly like a network problem.
func TestTheRequestCarriesWhatGitHubRequires(t *testing.T) {
	notModified(t)
	f := &fake{body: release{TagName: "v0.0.1"}}
	if _, err := f.start(t).Check(context.Background()); err != nil {
		t.Fatalf("check failed: %v", err)
	}
	if ua := f.lastReq.Get("User-Agent"); !strings.HasPrefix(ua, "PAT-Monitor/") {
		t.Errorf("User-Agent %q does not name the product", ua)
	}
	if a := f.lastReq.Get("Accept"); a != "application/vnd.github+json" {
		t.Errorf("Accept %q", a)
	}
	if v := f.lastReq.Get("X-GitHub-Api-Version"); v == "" {
		t.Error("no API version pinned")
	}
}

// **A build with uncommitted changes asks nothing and reports nothing.**
//
// That binary is the code of no release. Offering its owner a download would be
// telling them to throw away whatever they were in the middle of testing — and
// the request would go out from a machine that has no business making it.
func TestAModifiedBuildNeverReportsAnUpdate(t *testing.T) {
	was := version.Modified
	version.Modified = "yes"
	t.Cleanup(func() { version.Modified = was })

	f := &fake{body: release{TagName: "v99.0.0", HTMLURL: "https://github.com/Nenco79/PAT-Monitor/releases/tag/v99.0.0"}}
	c := f.start(t)

	st, err := c.Check(context.Background())
	if err != nil {
		t.Fatalf("check failed: %v", err)
	}
	if st.Code != Unknown {
		t.Fatalf("code %q instead of %q", st.Code, Unknown)
	}
	if f.requests != 0 {
		t.Errorf("%d requests were made from a modified build", f.requests)
	}
}

// The check gives up rather than holding a goroutine against a dead socket.
func TestACheckThatHangsIsAbandoned(t *testing.T) {
	notModified(t)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		<-r.Context().Done()
	}))
	t.Cleanup(srv.Close)

	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()

	done := make(chan struct{})
	go func() {
		defer close(done)
		if st, _ := (&Checker{URL: srv.URL}).Check(ctx); st.Code != Unknown {
			t.Errorf("code %q instead of %q", st.Code, Unknown)
		}
	}()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("the check did not give up")
	}
}

// The cadence is a promise about somebody else's rate limit.
//
// Unauthenticated GitHub allows on the order of sixty requests an hour per IP
// address, and several installations in one house share one. This is not a
// tuning knob: a number small enough to be neighbourly is the whole reason the
// check can exist without being authenticated.
func TestTheCadenceStaysNeighbourly(t *testing.T) {
	if Interval < time.Hour {
		t.Errorf("an interval of %v asks too often to share one address", Interval)
	}
	if FirstDelay <= 0 {
		t.Error("the first check must not fall inside start-up")
	}
}

// The endpoint is the one that excludes pre-releases, and it names this repo.
//
// It is the whole pre-release policy, so it is asserted rather than assumed: a
// change to /releases would start offering every beta with nothing complaining,
// which is precisely the direction nobody looks in.
func TestTheEndpointIsTheOneThatExcludesPrereleases(t *testing.T) {
	if !strings.HasSuffix(endpoint, "/releases/latest") {
		t.Errorf("%q does not end in /releases/latest, so pre-releases would be offered", endpoint)
	}
	if !strings.Contains(endpoint, "/Nenco79/PAT-Monitor/") {
		t.Errorf("%q does not name this repository", endpoint)
	}
	if !strings.HasPrefix(endpoint, "https://") {
		t.Errorf("%q is not https", endpoint)
	}
}

// A body far larger than any release document does not get read into memory.
func TestAHugeAnswerIsBounded(t *testing.T) {
	notModified(t)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = fmt.Fprint(w, `{"tag_name":"v99.0.0","notes":"`)
		chunk := strings.Repeat("x", 64*1024)
		for range 64 { // 4 MB, four times the ceiling
			_, _ = fmt.Fprint(w, chunk)
		}
		_, _ = fmt.Fprint(w, `"}`)
	}))
	t.Cleanup(srv.Close)

	st, _ := (&Checker{URL: srv.URL}).Check(context.Background())
	if st.Code == Available {
		t.Error("an oversized body was read and believed")
	}
}

// **A 200 that parses and says nothing must not switch the check off either.**
//
// It is the unreadable-body guard one door across, and the door it did not
// cover: a release document with no tag parses perfectly, so the first version
// stored its ETag and overwrote what was known. From the next check on the
// server answers 304 — the check dead for the life of the process — and an
// update already found was thrown away on the way.
func TestAnAnswerWithNoTagDoesNotSwitchTheCheckOff(t *testing.T) {
	notModified(t)
	f := &fake{
		body: release{TagName: "v99.0.0", HTMLURL: "https://github.com/Nenco79/PAT-Monitor/releases/tag/v99.0.0"},
		etag: `W/"abc123"`,
	}
	c := f.start(t)

	if st, err := c.Check(context.Background()); err != nil || st.Code != Available {
		t.Fatalf("first check: %v / %q", err, st.Code)
	}

	// The server now answers 200 with a document carrying no tag, and a new
	// ETag with it — which is what makes the trap: stored, it is sent back and
	// answered 304 for ever.
	f.body = release{HTMLURL: "https://github.com/Nenco79/PAT-Monitor/releases/latest"}
	f.etag = `W/"def456"`

	st, err := c.Check(context.Background())
	if err == nil {
		t.Error("an answer with no tag produced no error")
	}
	if st.Code != Available || st.Version != "99.0.0" {
		t.Fatalf("the earlier answer was overwritten: %q %q", st.Code, st.Version)
	}

	// The server is fixed, and the next ask has to be able to see it.
	f.body = release{TagName: "v99.1.0", HTMLURL: "https://github.com/Nenco79/PAT-Monitor/releases/tag/v99.0.01"}
	if st, err = c.Check(context.Background()); err != nil {
		t.Fatalf("third check: %v", err)
	}
	if st.Version != "99.1.0" {
		t.Fatalf("version %q: the empty answer's tag was kept and the check went deaf", st.Version)
	}
}

// **A tag that is not one of ours is Unknown, not Current.**
//
// The empty tag was refused by the branch this test sits beside; a tag that is
// **present** and unreadable was not, and that is the likelier shape of the
// fault — `v1.0` with two fields, a date, a name, a tagging scheme changed one
// day. `Newer` answers false to an older release and false to a tag nobody can
// parse, so those fell through to Current: the monitor said "you are up to
// date" on the strength of a document it had not understood, and Check then
// stored the ETag, so it went on saying it out of memory.
//
// **The defect was put back and this test fails with it**: with the readability
// check removed, the second check overwrites an update already found and comes
// back Current.
func TestATagThatIsNotAVersionDoesNotBecomeCurrent(t *testing.T) {
	notModified(t)
	f := &fake{
		body: release{TagName: "v99.0.0", HTMLURL: "https://github.com/Nenco79/PAT-Monitor/releases/tag/v99.0.0"},
		etag: `W/"abc123"`,
	}
	c := f.start(t)
	if st, err := c.Check(context.Background()); err != nil || st.Code != Available {
		t.Fatalf("first check: %v / %q", err, st.Code)
	}

	for _, tag := range []string{"v1.0", "latest", "2026-09-16", "release-final", "1.0.0.1"} {
		f.body = release{TagName: tag, HTMLURL: "https://github.com/Nenco79/PAT-Monitor/releases/latest"}
		f.etag = `W/"` + tag + `"`

		st, err := c.Check(context.Background())
		if err == nil {
			t.Errorf("tag %q: read as an answer", tag)
		}
		if st.Code != Available || st.Version != "99.0.0" {
			t.Errorf("tag %q: the update already found became %q %q", tag, st.Code, st.Version)
		}
	}

	// And a readable older tag is still Current, or the refusal would be a way
	// of never saying anything at all.
	f.body = release{TagName: "v0.0.1", HTMLURL: "https://example.invalid/r/001"}
	f.etag = `W/"old"`
	if st, err := c.Check(context.Background()); err != nil || st.Code != Current {
		t.Fatalf("an older release answered %v / %q, wanted current", err, st.Code)
	}
}

// State and Check are called from two threads, and the type has to survive it.
//
// The tray's message loop asks for the state every time it refreshes the icon,
// while the update goroutine is storing an answer. **Go's race detector cannot
// be run on this machine** — it wants cgo and there is no C compiler — so this
// exercises the pair rather than proving the absence of a race: what it catches
// is a State torn between two answers, a version from one beside a URL from
// another.
func TestTheStateCanBeReadWhileItIsBeingWritten(t *testing.T) {
	notModified(t)
	f := &fake{body: release{TagName: "v99.0.0", HTMLURL: "https://github.com/Nenco79/PAT-Monitor/releases/tag/v99.0.0"}}
	c := f.start(t)

	stop := make(chan struct{})
	done := make(chan struct{})
	go func() {
		defer close(done)
		for {
			select {
			case <-stop:
				return
			default:
			}
			st := c.State()
			// Whatever is read must be one answer and not two halves of two.
			if st.Code == Available && (st.Version == "" || st.URL == "") {
				t.Errorf("a torn state: %q %q %q", st.Code, st.Version, st.URL)
				return
			}
			if st.Code == Current && (st.Version != "" || st.URL != "") {
				t.Errorf("a torn state: %q %q %q", st.Code, st.Version, st.URL)
				return
			}
		}
	}()

	for i := range 200 {
		if i%2 == 0 {
			f.body = release{TagName: "v99.0.0", HTMLURL: "https://github.com/Nenco79/PAT-Monitor/releases/tag/v99.0.0"}
		} else {
			f.body = release{TagName: "v0.0.1", HTMLURL: "https://example.invalid/r/1"}
		}
		if _, err := c.Check(context.Background()); err != nil {
			t.Fatalf("check %d: %v", i, err)
		}
	}
	close(stop)
	<-done
}

// **The release's URL is executed, not displayed**: the panel hands it to the
// shell's "open", which starts whatever it names — a browser for a page, and
// for a path, a share or a protocol handler, whatever that is. So an answer
// naming anything but a page under this repository's releases is an answer
// not received.
//
// **The defect was put back and this test fails with it**: with releasePage
// no longer asked, every one of these reaches the panel as the update's link.
func TestOnlyAReleasePageIsHandedToTheShell(t *testing.T) {
	for _, bad := range []string{
		`\somebody.example\share\setup.exe`,
		"file:///C:/Windows/System32/calc.exe",
		"search-ms:query=setup",
		"http://github.com/Nenco79/PAT-Monitor/releases/tag/v99.0.0",
		"https://github.com.example/Nenco79/PAT-Monitor/releases/tag/v99.0.0",
		"https://github.com/somebody-else/PAT-Monitor/releases/tag/v99.0.0",
		"https://github.com/Nenco79/PAT-Monitor/archive/refs/tags/v99.0.0.zip",
		"https://user@github.com/Nenco79/PAT-Monitor/releases/tag/v99.0.0",
		"https://github.com/Nenco79/PAT-Monitor/releases/",
		"",
	} {
		st := verdict(release{TagName: "v99.0.0", HTMLURL: bad})
		if st.Code != Unknown || st.URL != "" {
			t.Errorf("%q: verdict %q with URL %q, wanted Unknown", bad, st.Code, st.URL)
		}
	}
	good := "https://github.com/Nenco79/PAT-Monitor/releases/tag/v99.0.0"
	if st := verdict(release{TagName: "v99.0.0", HTMLURL: good}); st.Code != Available || st.URL != good {
		t.Errorf("the real release page gave %q with %q", st.Code, st.URL)
	}
}

// A redirect off https is refused, and the monitor's client is the one that
// refuses it.
func TestTheCheckDoesNotFollowARedirectOffHTTPS(t *testing.T) {
	plain, _ := http.NewRequest(http.MethodGet, "http://api.github.com/x", nil)
	if httpsOnly(plain, nil) == nil {
		t.Error("a redirect to plain http was followed")
	}
	enc, _ := http.NewRequest(http.MethodGet, "https://api.github.com/x", nil)
	if err := httpsOnly(enc, nil); err != nil {
		t.Errorf("a redirect that stays on https was refused: %v", err)
	}
	if defaultClient.CheckRedirect == nil {
		t.Error("the monitor's client follows redirects wherever they go")
	}
}
