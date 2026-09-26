// Package update says whether a newer release exists. It downloads nothing.
//
// **It exists because a program in somebody else's house cannot be reached.**
// The monitor is a portable executable somebody downloaded once; when a fault is
// found and fixed, there is no channel back to them, and the fix sits in a
// repository they have no reason to look at. A baby monitor that runs every
// night for a year is exactly the program this matters most for, and exactly the
// one nobody thinks to go and check on.
//
// **What it does not do is as much of the design as what it does.** It fetches
// one small JSON document, compares two version numbers, and remembers the
// answer. It does not download the release, does not replace the executable and
// does not restart anything: applying an update is the user's gesture, made in
// front of the machine, and this package's part ends at saying there is one.
//
// The shape is `internal/tunnel/reach.go`'s, and deliberately so: an outbound
// request with a deadline, a bounded read, and a **tri-state** answer whose
// third value is the load-bearing one — "I could not ask" must never be rendered
// as "you are up to date".
package update

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"patmonitor/internal/version"
)

// State is what the check found. The zero value is "nobody has asked yet",
// which is the same as not knowing, and which renders as silence.
type State struct {
	Code Code
	// Version is the release's number with the tag's leading "v" removed, and
	// URL is its page. Both are empty unless Code is Available.
	//
	// **The URL comes from the release and is not composed here.** A link built
	// from the repository name and the tag would be a second way of naming the
	// same page, and it would point at nothing the day a tag is renamed —
	// whereas html_url is what GitHub itself says the page is.
	Version string
	URL     string
}

// Code is the outcome, as a code. The words belong to whoever draws them.
type Code string

const (
	// Unknown: no answer. No network, a refusal, a body that would not parse,
	// or nobody has asked yet.
	//
	// **It is the zero value on purpose.** "I do not know" is not "you are up to
	// date": the first is silence and the second is a claim, and a program that
	// makes the second one out of no evidence tells somebody their monitor is
	// current on the night the fix they need was published.
	Unknown Code = ""
	// Current: the newest release is this version, or older than it.
	Current Code = "current"
	// Available: a newer release exists.
	Available Code = "available"
)

const (
	// endpoint is the release the check asks about.
	//
	// **/releases/latest excludes pre-releases and drafts**, and that is the
	// whole of the pre-release policy: a release marked pre-release on GitHub is
	// invisible here, with no code of ours deciding it and no second list to
	// keep in step. A version that should not be offered is a checkbox.
	endpoint = "https://api.github.com/repos/" + repository + "/releases/latest"

	// repository is whose releases these are, and releasePages is where their
	// pages live: see releasePage.
	repository   = "Nenco79/PAT-Monitor"
	releasePages = "https://github.com/" + repository + "/releases/"

	// timeout bounds one attempt. The check is a background courtesy with
	// nobody waiting on it, so it gives up quickly rather than holding a
	// goroutine against a socket that will never answer.
	timeout = 20 * time.Second

	// bodyLimit is what we are willing to read. The document wanted is a few
	// kilobytes and release notes can be long; on the other side of this
	// request is the Internet.
	bodyLimit = 1 << 20

	// Interval is how often the question is asked, and **the number is about
	// somebody else's budget rather than ours**. Unauthenticated GitHub allows
	// on the order of sixty requests an hour per IP address, and several
	// installations in one house share one. Daily leaves that untouched and is
	// as often as the answer can change in a way anybody cares about.
	Interval = 24 * time.Hour

	// FirstDelay keeps the check out of start-up, where the camera, the
	// microphone and the tunnel are all opening at once and the network may not
	// be up yet. Nothing waits on this, so it can afford to go last.
	FirstDelay = 3 * time.Minute
)

// Checker asks, and remembers the answer between asks.
//
// It holds the ETag rather than having it passed in: it is the one thing that
// has to survive from one call to the next, and it belongs to whoever asks.
//
// **It is read from a different thread than it is written from**, which is not
// obvious from either side: Check runs on the update goroutine, and State is
// called by the tray's message loop every time the icon is refreshed. Without
// the mutex a State read while an answer is being stored can hand back a
// version string whose pointer and length come from two different answers, or a
// URL belonging to one release beside the number of another — and Go's race
// detector cannot be run here at all, because it wants cgo and there is no C
// compiler on this machine. Every other field that closure reads is already
// guarded; this one was the exception.
type Checker struct {
	// Client is the HTTP client. Nil means the default one, which is what the
	// monitor uses: this request travels the road any browser travels, proxy
	// included, and its one difference is refusing a redirect off https — see
	// httpsOnly.
	Client *http.Client
	// URL overrides the endpoint. For the tests.
	URL string

	mu   sync.Mutex
	etag string
	last State
}

// release is the part of GitHub's answer that is read.
//
// The document carries some dozens of fields and four are used. Declaring only
// those is not laziness: encoding/json ignores the rest, so this struct says
// exactly what the code depends on, and whoever reads it knows nothing else is
// quietly load-bearing.
type release struct {
	TagName    string `json:"tag_name"`
	HTMLURL    string `json:"html_url"`
	Prerelease bool   `json:"prerelease"`
	Draft      bool   `json:"draft"`
}

// Check asks once and returns what is known now.
//
// **A build that matches no commit never reports an update.** With uncommitted
// changes the binary is the code of no release, and pointing its owner at a
// download would be telling them to throw away whatever they were testing. It
// is the fact version.Full already prints as "modified", acted on rather than
// merely displayed.
func (c *Checker) Check(ctx context.Context) (State, error) {
	if version.Modified != "" {
		return State{}, nil
	}

	rel, etag, unchanged, err := c.fetch(ctx)
	if err != nil {
		// **The previous answer survives a failed attempt.** A monitor that
		// learned about 1.0.1 yesterday still knows it through a night with no
		// network; forgetting would turn every hiccup into silence about a fix
		// that is still there.
		return c.State(), err
	}
	if unchanged {
		return c.State(), nil
	}

	// **An answer that produced no verdict is a failed attempt, whatever its
	// status code was.** A 200 carrying no tag parses perfectly and says
	// nothing, and the first version treated it as an answer: it stored the tag
	// and overwrote what was known. From the next check on the server replies
	// 304 — so one unreadable answer switched the check off for the life of the
	// process **and** threw away an update already found. The guard against
	// that existed one branch up, for bodies that will not parse at all, and
	// this is the same fault through the door it did not cover.
	st := verdict(rel)
	if st.Code == Unknown {
		return c.State(), errors.New("the release could not be read")
	}

	c.mu.Lock()
	defer c.mu.Unlock()
	c.etag, c.last = etag, st
	return st, nil
}

// State is the last answer, without asking again.
//
// It is called from the tray's message loop while Check may be writing from the
// update goroutine, which is why it takes the lock rather than reading the
// field.
func (c *Checker) State() State {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.last
}

// verdict turns a release into an outcome.
//
// **The pre-release and draft flags are read even though the endpoint excludes
// them.** Two comparisons, and they remove a silent dependency on somebody
// else's behaviour staying as it is: were that endpoint ever to answer with a
// pre-release, the policy this program promises — marked pre-release means not
// offered — would go on holding instead of failing in the one direction nobody
// would look at.
func verdict(r release) State {
	if r.Prerelease || r.Draft {
		return State{Code: Current}
	}
	tag := strings.TrimSpace(r.TagName)
	// **A tag that cannot be read is Unknown, and the empty one was only the
	// half that had been thought of.** Saying "you are up to date" here would
	// be a claim built on nothing, and the branch that said so covered `""`
	// alone — while the likely shape of the fault is a tag that is present and
	// is not one of ours: `v1.0` with two fields, a date, a name. `Newer`
	// answers false to both an older release and an unreadable one, so those
	// fell through to Current, and Check then stored the ETag: from there the
	// answer is served from memory until that release stops being the latest.
	//
	// It costs a day's silence where the endpoint has been mangled, and it
	// removes a monitor that says it is current on evidence it never had. If
	// the tagging scheme itself ever changes, this is also the difference
	// between a check that goes quiet and one that lies.
	if !version.Readable(tag) {
		return State{}
	}
	if !version.Newer(version.Number, tag) {
		return State{Code: Current}
	}
	if !releasePage(r.HTMLURL) {
		return State{}
	}
	return State{
		Code:    Available,
		Version: strings.TrimPrefix(tag, "v"),
		URL:     r.HTMLURL,
	}
}

// releasePage says whether a URL is a page of this program's releases.
//
// **The URL goes to the shell, and the shell opens whatever it is given.** The
// panel's "update" row hands it to ShellExecute's "open", which for an https
// address starts a browser — and for a file path, a share on somebody's server
// or any registered protocol handler, starts whatever that is. The value comes
// from an answer over TLS, which is why this was never urgent; it is also the
// one string in this package that is executed rather than displayed, and a
// machine behind an inspecting proxy trusts whatever that proxy says. So it
// has to be what the question asked about: an https page under this
// repository's releases, which is all a release's html_url ever is.
//
// A URL that is not one is Unknown rather than an update with no link: an
// answer that names a page we would not open is an answer we did not get.
func releasePage(raw string) bool {
	u, err := url.Parse(raw)
	if err != nil || u.Scheme != "https" || u.User != nil || u.Host != "github.com" {
		return false
	}
	return len(raw) > len(releasePages) && strings.EqualFold(raw[:len(releasePages)], releasePages)
}

// httpsOnly refuses a redirect that leaves https.
//
// Go's client follows up to ten redirects and does not mind a step from https
// to plain http, where the rest of the answer could be written by anybody on
// the way. GitHub has no reason to send one, so one is a refusal.
func httpsOnly(req *http.Request, via []*http.Request) error {
	if req.URL.Scheme != "https" {
		return fmt.Errorf("redirected off https to %s", req.URL.Redacted())
	}
	if len(via) >= 10 {
		return errors.New("stopped after 10 redirects")
	}
	return nil
}

// defaultClient is the monitor's: the default one, with httpsOnly.
var defaultClient = &http.Client{CheckRedirect: httpsOnly}

// fetch asks GitHub, and reports whether the answer was "nothing changed".
//
// **It hands the ETag back rather than storing it**, because whether the answer
// is worth remembering is not a question this layer can answer: a 200 that
// parses can still carry nothing usable, and storing the tag there would make
// every later check a 304 about a document nobody ever read.
func (c *Checker) fetch(ctx context.Context) (rel release, etag string, unchanged bool, err error) {
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	target := c.URL
	if target == "" {
		target = endpoint
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, target, nil)
	if err != nil {
		return rel, "", false, err
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("X-GitHub-Api-Version", "2022-11-28")
	// **GitHub refuses a request carrying no User-Agent**, so this header is
	// not decoration. It names the version because that is the one thing that
	// makes the request legible at the other end, and it is already the subject
	// of the question being asked.
	req.Header.Set("User-Agent", "PAT-Monitor/"+version.Number)
	// **The conditional request is what makes a daily check free.** The answer
	// is the same document for weeks at a time, and a 304 costs nothing against
	// a rate limit that several houses behind one address are sharing.
	c.mu.Lock()
	etag = c.etag
	c.mu.Unlock()
	if etag != "" {
		req.Header.Set("If-None-Match", etag)
	}
	etag = ""

	client := c.Client
	if client == nil {
		client = defaultClient
	}
	resp, err := client.Do(req)
	if err != nil {
		return rel, "", false, err
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusNotModified {
		return rel, "", true, nil
	}
	if resp.StatusCode != http.StatusOK {
		return rel, "", false, fmt.Errorf("unexpected status %d", resp.StatusCode)
	}

	body, err := io.ReadAll(io.LimitReader(resp.Body, bodyLimit))
	if err != nil {
		return rel, "", false, err
	}
	if err := json.Unmarshal(body, &rel); err != nil {
		return rel, "", false, errors.New("the answer could not be read")
	}
	// **The tag is carried back and not stored here.** Whether this answer is
	// worth remembering is decided by whoever can see the verdict: a body that
	// parses is not yet a release anybody can act on, and a tag stored for one
	// of those makes every later check a 304 about a document that said
	// nothing.
	return rel, resp.Header.Get("ETag"), false, nil
}
