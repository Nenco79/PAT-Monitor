// Package server serves the web UI, the authentication and the WebRTC
// signalling.
package server

import (
	"context"
	"embed"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"log/slog"
	"math"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/coder/websocket"
	"github.com/pion/webrtc/v4"

	"patmonitor/internal/alerts"
	"patmonitor/internal/config"
	"patmonitor/internal/guard"
	"patmonitor/internal/qr"
	"patmonitor/internal/record"
	"patmonitor/internal/rtc"
	"patmonitor/internal/tunnel"
	"patmonitor/internal/version"
)

//go:embed web
var webFS embed.FS

// Status describes the current state, shown in the UI and useful for support.
type Status struct {
	// Version is the first thing whoever is helping somebody else needs, so it
	// comes first. It does not arrive from whoever builds the Status: the
	// binary knows it.
	Version       string  `json:"version"`
	Ready         bool    `json:"ready"`
	Encoder       string  `json:"encoder"`
	EncoderVendor string  `json:"encoderVendor"`
	Resolution    string  `json:"resolution"`
	FPS           int     `json:"fps"`
	MeasuredFPS   float64 `json:"measuredFps"`
	// DeliveredFPS is the cadence the gate lets through, and it is worth showing
	// **only when it is lower than the declared one**: that is when frames are
	// being dropped on purpose, and the viewer has to know so as not to mistake
	// the saving for a stuck camera. VideoDropped is the other number that tells
	// the two apart.
	DeliveredFPS int   `json:"deliveredFps"`
	VideoDropped int64 `json:"videoDropped"`
	VideoKbps    int   `json:"videoKbps"`
	// LastFrameUnix is when the encoder last produced a frame, in Unix seconds,
	// and zero if it never has.
	//
	// **It is here because `Ready` cannot answer the question it was being
	// asked.** That field is a latch — it closes on the first keyframe and
	// stays closed — so a monitor whose picture stops after an hour goes on
	// declaring itself ready, and every reader of this struct believed it: the
	// banner, the notification area and the alert in the log, all three from
	// the same predicate. This is the instant they need in order to say
	// something about **now**, and the verdict is not taken here because two of
	// those three want it with a grace of their own. See `captureStopped`.
	LastFrameUnix int64 `json:"lastFrameUnix"`
	// Camera is the **name** of the webcam being captured, CameraChosen the
	// symbolic link of the one the configuration asks for — empty meaning "the
	// first usable one".
	//
	// **CameraFallback is not that comparison redone**, and it is a field of its
	// own for a reason the microphone does not have: the chosen link comes out
	// of a file and the open one out of the enumeration, and Windows gives the
	// same symbolic link back in different cases depending on who is asked. The
	// capture decides it once, where the two are matched, and whoever shows it
	// does not have to know that.
	Camera         string `json:"camera"`
	CameraChosen   string `json:"cameraChosen"`
	CameraFallback bool   `json:"cameraFallback"`
	// CameraDenied says Windows is refusing the camera because the permission
	// is off, and MicrophoneDenied the same for the microphone.
	//
	// **They are here because no other field can say it.** A refused camera
	// produces no frames and a refused microphone no level, so from
	// `LastFrameUnix` and `MicrophoneActive` a withdrawn consent is
	// indistinguishable from a cable out — and the two want different words and
	// different remedies. It is the same argument as `CameraFallback`: a fact
	// the capture already knows, published rather than left to be guessed at by
	// whoever draws the page.
	//
	// They are two and not one because the two permissions are two switches in
	// Windows, and a state that said only "denied" would leave the reader to go
	// and find out which.
	CameraDenied     bool `json:"cameraDenied"`
	MicrophoneDenied bool `json:"microphoneDenied"`
	// CameraOpening and MicrophoneOpening say the device is being opened right
	// now and the call has not answered.
	//
	// **They are the state before the pair above has anything to say.** A
	// refusal comes back and names itself; while Windows is still asking the
	// person in front of the machine there is no answer at all, and everything
	// downstream used to read that as the device being absent — which reaches
	// the page as *there is no microphone* at the one moment when what is
	// happening is that somebody is being asked about it. Packaged, the consent
	// is asked per package and at the first use, so the wait is ordinary.
	//
	// They are published rather than deduced for `CameraDenied`'s reason: no
	// other field can tell "not yet" from "not there".
	CameraOpening     bool `json:"cameraOpening"`
	MicrophoneOpening bool `json:"microphoneOpening"`
	// CameraOpenedUnix is when the camera last came open, zero before the first
	// time. It is what the start-up grace is counted from, because counting it
	// from the process starting says nothing useful once the open has waited
	// for somebody to answer a dialogue.
	CameraOpenedUnix int64 `json:"cameraOpenedUnix"`
	// Microphone is the device's **name**, and MicrophoneActive says whether it
	// is really capturing. They are two fields and not one because they used to
	// be one: the name was replaced by the word "absent", which whoever read the
	// status had to recognise in order to know there was no audio. A sentence
	// used as a sentinel value is a decision made in one language, and
	// translating the interface would have broken it in silence.
	Microphone       string `json:"microphone"`
	MicrophoneActive bool   `json:"microphoneActive"`
	// MicrophoneID is the endpoint that is capturing, MicrophoneChosen the one
	// chosen in the configuration — empty means "the Windows default", that is,
	// follow the role rather than pin a device.
	//
	// **They are two fields because they can diverge**, and the case is not
	// rare: the chosen microphone is unplugged, the capture falls back on what
	// is there, and the box has to say **what is capturing** rather than what is
	// written in the file. They are filled by whoever knows: the first by the
	// capture, the second by the server, which is the only one holding the
	// configuration.
	MicrophoneID     string `json:"microphoneId"`
	MicrophoneChosen string `json:"microphoneChosen"`
	RawAudio         bool   `json:"rawAudio"`
	// MicrophoneMuted says the endpoint is muted in Windows, which is the one
	// cause of silence with a remedy on this machine. See
	// pipeline.MicrophoneMuted for what it describes and what it cannot.
	MicrophoneMuted bool    `json:"microphoneMuted"`
	AudioLevelDBFS  float64 `json:"audioLevelDbfs"`
	// MicHealth is a **code**, not a sentence: `ok`, `digital-silence`, `quiet`,
	// `unknown`. See detect.MicCode*.
	MicHealth      string `json:"micHealth"`
	Viewers        int64  `json:"viewers"`
	VideoFrames    int64  `json:"videoFrames"`
	Keyframes      int64  `json:"keyframes"`
	KeyframeReqs   int64  `json:"keyframeReqs"`
	Restarts       int64  `json:"restarts"`
	ProfileLevelID string `json:"profileLevelId"`
	Uptime         string `json:"uptime"`
	// Devices is how many devices have an open session. The server fills it in,
	// being the only one that knows, and it gives the button that revokes them
	// a meaning: "disconnect everybody" without knowing how many is a leap in
	// the dark.
	Devices int `json:"devices"`
	// LocalURL is the address to open from the other devices in the house.
	//
	// It is computed by whoever starts the program and not by the page: from the
	// browser one would only see `location.origin`, which for whoever is in
	// front of the monitor is `localhost` — that is, the one address that
	// certainly does not work from the phone.
	LocalURL string `json:"localUrl"`
	// DetectCry and DetectBark are the state of the two toggles. They live in
	// the status and not in a route of their own because the buttons are drawn
	// with the rest of the page, and a second network round trip for two
	// booleans would be a second thing that can fail on its own.
	DetectCry    bool `json:"detectCry"`
	DetectBark   bool `json:"detectBark"`
	DetectMotion bool `json:"detectMotion"`
	// Alerts is what is wrong **now**, from the most serious to the most recent.
	//
	// It travels in the status and not on a channel of its own because the page
	// asks for the status anyway, and a second channel would be a second thing
	// that can break by itself. The identifiers are stable for as long as the
	// alert lasts: that is how the page tells a new fault from the previous one,
	// without the server having to keep a history.
	//
	// TalkbackBusy says somebody is speaking into the room. The page uses it for
	// a question about itself: if I pressed to speak and the monitor does not
	// confirm it, my voice is not getting through — either because somebody else
	// has the floor, or because the packets are not arriving at all. Both are
	// cured the same way, so there is no need to tell them apart.
	TalkbackBusy bool `json:"talkbackBusy"`
	// Recording says a clip is under way, asked for by hand or by an event.
	//
	// **The state is held by the monitor and not by the browser.** A clip lasts
	// ten seconds and whoever asked for it can close the page; and whoever opens
	// a second phone has to see that recording is happening, because it is the
	// room being recorded, not their page. For the same reason the button does
	// not colour itself when pressed: it waits for this answer.
	Recording bool `json:"recording"`

	Alerts []alerts.Alert `json:"alerts"`
	// Remote describes access from outside the house: whether it is active, and
	// otherwise which step is missing to make it so.
	Remote tunnel.State `json:"remote"`
}

// errPasswordAlreadySet is how `apiSetup` refuses from **inside** the store's
// update, where the check and the write have to be one operation. It never
// reaches the page: the route turns it into ErrAlreadySet, which is the code the
// catalogue has a sentence for.
var errPasswordAlreadySet = errors.New("server: the password is already set")

// Options configures the server.
type Options struct {
	// Config is the configuration, and the server keeps **no copy of its own**.
	//
	// It used to take one by value, and every route that changed a setting
	// rebuilt the whole struct from that copy and wrote it back. Anything
	// written to the file by somebody else in the meantime was therefore undone
	// by the next save from any page — concretely, the node name Tailscale
	// grants, which `main` records on the tunnel's goroutine and which no route
	// has any business carrying. The store is shared by pointer so that there is
	// one value and not two, and so that a route can say what it changes instead
	// of restating everything it did not.
	Config *config.Store
	Hub    *rtc.Hub
	// StatusFn supplies the up-to-date state on every request.
	StatusFn func() Status
	// Microphones lists the microphones to choose between.
	//
	// It is a function and not a list because the answer changes while the
	// monitor runs — a USB stick is plugged and unplugged — and because whoever
	// knows how to enumerate the WASAPI endpoints is the package that opens
	// them, not this one. Nil means there is no enumerating here: the page keeps
	// only the entry for the microphone in use and it cannot be changed, which
	// is also what the tests see.
	Microphones func() ([]Microphone, error)
	// UseMicrophone tells the capture which endpoint to open, without
	// restarting.
	//
	// The server checks the ID exists and persists the choice; applying it
	// belongs to whoever holds the microphone open — the same division as
	// EnableRemote.
	UseMicrophone func(id string)
	// Cameras lists the cameras to choose between, and UseCamera tells the
	// capture which one to open without restarting.
	//
	// They are the microphone's pair and hold the same division of labour: the
	// server checks the link is connected and persists the choice, applying it
	// belongs to whoever holds the camera open. Nil means there is no
	// enumerating here — the page keeps only the entry for the camera in use and
	// the box cannot be pressed, which is what the tests see.
	Cameras   func() ([]Camera, error)
	UseCamera func(id string)
	// EnableRemote switches on access from outside the house without
	// restarting.
	//
	// It is a function and not a flag because the only one who knows how to
	// switch it on is whoever built the tunnel. The server checks the right
	// (`CanExposePublicly`) and persists the choice; the switching on it
	// delegates.
	EnableRemote func()
	// Clips is the store of the event recordings.
	//
	// The server decides neither where they live nor how many are kept: it
	// reads, serves and deletes at the request of the viewer. It may be nil, and
	// then the page shows its empty state — which is also what the tests that do
	// not build one see.
	Clips *record.Store
	// Record asks for a clip of now and says whether there was anything to
	// record.
	//
	// It is a function because the recorder is owned by whoever captures: the
	// server does not know what is in the ring, and putting it here would mean a
	// second place from which recording is commanded. The same division as
	// `EnableRemote` and `UseMicrophone`.
	//
	// **The boolean is not an error**: in the first seconds the ring is empty
	// and there is no clip to save. The difference between "done" and "nothing
	// to do" is visible only to whoever holds the ring, and whoever pressed the
	// button has a right to know it.
	Record func() bool
	Log    *slog.Logger
}

// Server is the application's web server.
type Server struct {
	opts     Options
	log      *slog.Logger
	sessions *sessionStore
	limiter  *limiter
	global   *globalLimiter
	mux      *http.ServeMux
	assets   fs.FS

	// hashing and hashingHome are the argon2id hashes that may run at once:
	// the first for anybody, the second only for requests from home. See
	// hashSlot.
	hashing     chan struct{}
	hashingHome chan struct{}

	// clipsMissing makes it said once only that the clip store was not attached:
	// once per request would be a line a second.
	clipsMissing sync.Once

	// refusals counts the viewers turned away in a row: see refuseViewer.
	refusalsMu    sync.Mutex
	refusals      int
	refusalsSince time.Time
}

// refuseViewer writes a viewer's refusal **as an alert, not as an event**.
//
// Whoever cannot connect retries, and the cadence of the retries is decided by
// their browser: one line per attempt puts the volume of our log in the hands of
// whoever is on the other side. Measured on a test machine with a broken camera
// — **over a thousand identical lines in twenty minutes**, one a second, which
// covered the only four lines that said what had really happened.
//
// It is the same shape as the alerts at the top of the page: it is written when
// the condition **appears** and when it **clears**, never while it lasts. The
// reason the refusals continue is already in the log — the `capture-stopped`
// alert comes out on appearing and on clearing — so the line per attempt adds
// nothing that is not already there.
//
// The count is not lost: it comes back in the clearing line, which is the only
// place it is wanted, because there it says **how long it lasted** rather than
// that it is happening.
func (s *Server) refuseViewer(from origin, err error) {
	s.refusalsMu.Lock()
	first := s.refusals == 0
	if first {
		s.refusalsSince = time.Now()
	}
	s.refusals++
	n := s.refusals
	s.refusalsMu.Unlock()

	if first {
		s.log.Warn("viewer refused", "from", from.Kind, "address", from.Addr, "reason", err)
		return
	}
	// The rest stay readable at `-v`, where whoever is diagnosing asked for them
	// on purpose.
	s.log.Debug("viewer refused again", "from", from.Kind, "address", from.Addr,
		"reason", err, "in_a_row", n)
}

// viewerAdmitted closes the run, if there was one.
func (s *Server) viewerAdmitted() {
	s.refusalsMu.Lock()
	n, since := s.refusals, s.refusalsSince
	s.refusals = 0
	s.refusalsMu.Unlock()

	if n == 0 {
		return
	}
	s.log.Info("viewers admitted again", "refused", n,
		"lasted", time.Since(since).Round(time.Second))
}

func New(opts Options) (*Server, error) {
	if opts.Log == nil {
		opts.Log = slog.Default()
	}
	assets, err := fs.Sub(webFS, "web")
	if err != nil {
		return nil, fmt.Errorf("embedded assets: %w", err)
	}

	if opts.Config == nil {
		return nil, fmt.Errorf("server: no configuration store")
	}
	ttl := time.Duration(opts.Config.Get().SessionTTLHours) * time.Hour
	s := &Server{
		opts:        opts,
		log:         opts.Log,
		sessions:    newSessionStore(ttl),
		limiter:     newLimiter(),
		global:      newGlobalLimiter(),
		hashing:     make(chan struct{}, hashSlots-homeSlots),
		hashingHome: make(chan struct{}, homeSlots),
		mux:         http.NewServeMux(),
		assets:      assets,
	}
	s.routes()
	return s, nil
}

// conf returns the configuration as it stands.
func (s *Server) conf() config.Config { return s.opts.Config.Get() }

// **There is no way of replacing the configuration while running, and that is
// deliberate.** There used to be `setConf`, `Config` and `UpdateConfig` here,
// written for a change from the tray that does not exist: nobody called them.
// The lock stays because the status page is read from several goroutines, and
// because when the local interface arrives the place to write is already
// prepared — but until it does, the code for doing it must not be there.

func (s *Server) Close() { s.sessions.close() }

// Handler returns the complete HTTP handler.
func (s *Server) Handler() http.Handler {
	return securityHeaders(s.mux)
}

func (s *Server) routes() {
	// Public pages and endpoints: only the ones needed in order to
	// authenticate.
	s.mux.HandleFunc("GET /login", s.pageLogin)
	s.mux.HandleFunc("POST /api/login", s.apiLogin)
	s.mux.HandleFunc("GET /setup", s.pageSetup)
	s.mux.HandleFunc("POST /api/setup", s.apiSetup)
	// The first-configuration path is public like /setup, and for the same
	// reason: its first step **is** setting the password, so demanding a session
	// would make it unreachable to whoever needs it. The steps that show real
	// data ask for the session each on its own account, at the /api/ routes that
	// sit behind requireAuth.
	s.mux.HandleFunc("GET /onboarding", s.pageOnboarding)
	s.mux.HandleFunc("GET /onboarding.js", s.serveAsset("onboarding.js", "application/javascript; charset=utf-8"))
	s.mux.HandleFunc("GET /onboarding.css", s.serveAsset("onboarding.css", "text/css; charset=utf-8"))
	s.mux.HandleFunc("GET /api/onboarding/state", s.apiOnboardingState)
	// The QR code goes with an address the page already shows in the clear: it
	// adds no secret, it only changes how it is transcribed. So it sits among
	// the public routes like the page that uses it.
	s.mux.HandleFunc("GET /qr", s.apiQR)
	s.mux.HandleFunc("GET /style.css", s.serveAsset("style.css", "text/css; charset=utf-8"))
	// icons.css is read both by the viewer and by the configuration path, so it
	// sits among the public routes like the second of the two: the login page
	// does not ask for it, but serving it to whoever has no session reveals
	// nothing the two pages do not show anyway.
	s.mux.HandleFunc("GET /icons.css", s.serveAsset("icons.css", "text/css; charset=utf-8"))
	// The manifest and the icons it points at. **They are open**, like the
	// stylesheets: a browser fetches a manifest before anybody has signed in,
	// and an icon behind a session is an icon iOS saves as a broken image.
	s.mux.HandleFunc("GET /manifest.webmanifest", s.apiManifest)
	// One route per size, from the list that is the authority: a pattern with a
	// wildcard would let a request name any number and make us draw it.
	for _, n := range iconSizes {
		s.mux.HandleFunc("GET "+iconPath(n), s.serveIcon(n))
	}
	// auth.js serves the login pages, so it has to be reachable without a
	// session: otherwise the form would fall back on native submission.
	s.mux.HandleFunc("GET /auth.js", s.serveAsset("auth.js", "application/javascript; charset=utf-8"))
	// The translation's two halves sit among the public routes because the login
	// page asks for them too, and by definition it has no session. They reveal
	// nothing: `i18n.js` is code, and the dictionary carries the same sentences
	// the pages show anyway.
	s.mux.HandleFunc("GET /i18n.js", s.serveAsset("i18n.js", "application/javascript; charset=utf-8"))
	s.mux.HandleFunc("GET /i18n.css", s.serveAsset("i18n.css", "text/css; charset=utf-8"))
	s.mux.HandleFunc("GET /dictionary.js", s.serveDictionary)

	// Everything else requires a valid session.
	s.mux.Handle("GET /", s.requireAuth(http.HandlerFunc(s.pageViewer)))
	s.mux.Handle("GET /app.js", s.requireAuth(
		http.HandlerFunc(s.serveAsset("app.js", "application/javascript; charset=utf-8"))))
	s.mux.Handle("GET /api/status", s.requireAuth(http.HandlerFunc(s.apiStatus)))
	s.mux.Handle("POST /api/logout", s.requireAuth(http.HandlerFunc(s.apiLogout)))
	s.mux.Handle("GET /ws", s.requireAuth(http.HandlerFunc(s.wsSignaling)))
	s.mux.Handle("POST /api/onboarding/done", s.requireAuth(http.HandlerFunc(s.apiOnboardingDone)))
	s.mux.Handle("POST /api/remote/enable", s.requireAuth(http.HandlerFunc(s.apiRemoteEnable)))
	s.mux.Handle("POST /api/password", s.requireAuth(http.HandlerFunc(s.apiPassword)))
	s.mux.Handle("POST /api/detect", s.requireAuth(http.HandlerFunc(s.apiDetect)))
	s.mux.Handle("GET /api/microphones", s.requireAuth(http.HandlerFunc(s.apiMicrophones)))
	s.mux.Handle("POST /api/microphone", s.requireAuth(http.HandlerFunc(s.apiMicrophone)))
	s.mux.Handle("GET /api/cameras", s.requireAuth(http.HandlerFunc(s.apiCameras)))
	s.mux.Handle("POST /api/camera", s.requireAuth(http.HandlerFunc(s.apiCamera)))
	// The event recordings. The page and its code are two assets like the
	// others; the rest sits under `/api/` on purpose, see clips.go.
	s.mux.Handle("GET /clips", s.requireAuth(http.HandlerFunc(s.pageClips)))
	s.mux.Handle("GET /clips.js", s.requireAuth(
		http.HandlerFunc(s.serveAsset("clips.js", "application/javascript; charset=utf-8"))))
	s.mux.Handle("GET /api/clips", s.requireAuth(http.HandlerFunc(s.apiClips)))
	s.mux.Handle("GET /api/clips/{name}", s.requireAuth(http.HandlerFunc(s.apiClipFile)))
	s.mux.Handle("POST /api/clips/{name}/keep", s.requireAuth(http.HandlerFunc(s.apiClipKeep)))
	s.mux.Handle("POST /api/clips/{name}/release", s.requireAuth(http.HandlerFunc(s.apiClipRelease)))
	s.mux.Handle("POST /api/clips/{name}/delete", s.requireAuth(http.HandlerFunc(s.apiClipDelete)))
	// Recording by hand is not a clips route: it does not touch the folder, it
	// asks the recorder for a clip. It sits beside `/api/detect` for that reason
	// — they are the two commands the viewer's bar gives the monitor.
	s.mux.Handle("POST /api/record", s.requireAuth(http.HandlerFunc(s.apiRecord)))
}

// pageClips serves the recordings page.
func (s *Server) pageClips(w http.ResponseWriter, r *http.Request) {
	s.serveAsset("clips.html", "text/html; charset=utf-8")(w, r)
}

// ---------- middleware ----------

// securityHeaders sets defensive headers on every response.
func securityHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h := w.Header()
		// The UI is entirely local: no external resource is legitimate.
		//
		// script-src 'self' is declared explicitly so as not to leave the rule
		// implicit in default-src: that implicit rule is exactly what silently
		// blocked the login pages' inline scripts, making the forms fall back on
		// native submission. All the code therefore lives in external files, and
		// must not be reintroduced inline.
		h.Set("Content-Security-Policy",
			"default-src 'self'; script-src 'self'; img-src 'self' data:; "+
				"style-src 'self' 'unsafe-inline'; media-src 'self' blob:; "+
				"connect-src 'self' ws: wss:; frame-ancestors 'none'; form-action 'self'")
		h.Set("X-Content-Type-Options", "nosniff")
		h.Set("Referrer-Policy", "no-referrer")
		h.Set("X-Frame-Options", "DENY")
		// The stream must not end up in any intermediate cache.
		h.Set("Cache-Control", "no-store")
		// Only on an answer that arrived encrypted, which is the public address
		// and the tailnet name: said over the LAN's plain HTTP it would be
		// ignored by the browser, and it should be.
		if r.TLS != nil {
			h.Set("Strict-Transport-Security", "max-age=31536000")
		}
		next.ServeHTTP(w, r)
	})
}

// requireAuth blocks access to whoever has no valid session.
//
// The criterion is refusal by default: any route not listed among the public
// ones comes through here, so that adding an endpoint does not expose it by
// inattention.
func (s *Server) requireAuth(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// With no password set no access is possible: the request is diverted to
		// the initial configuration rather than leaving everything open.
		if !s.conf().HasPassword() {
			if !answersWithAPage(r) {
				writeJSONError(w, http.StatusForbidden, ErrNoPassword)
				return
			}
			http.Redirect(w, r, "/setup", http.StatusSeeOther)
			return
		}

		from := requestOrigin(r)
		verdict := sessionInvalid
		c, err := r.Cookie(sessionCookieName)
		if err == nil {
			verdict = s.sessions.check(c.Value, from.Public())
		}
		if verdict == sessionWrongRoad {
			// A cookie that only ever crossed the house in clear, presented at
			// the public address: somebody carried it there. The owner's own
			// browser cannot do it, so this line is worth reading.
			s.log.Warn("session refused: opened on the home network and presented "+
				"from the Internet", "from", from.Addr, "path", r.URL.Path)
		}
		if verdict == sessionInvalid || verdict == sessionWrongRoad {
			if !answersWithAPage(r) {
				writeJSONError(w, http.StatusUnauthorized, ErrNoSession)
				return
			}
			http.Redirect(w, r, "/login", http.StatusSeeOther)
			return
		}

		// **A command is taken only from our own pages**, and the cookie's
		// SameSite used to be the whole of that argument. It is not enough:
		// SameSite separates sites, not origins, so another port on this PC or
		// another node of the owner's own tailnet is the same site and its
		// requests carry the cookie. From there a page could switch detection
		// off, delete clips or open the Funnel with nobody having pressed
		// anything. fromOurOwnPages is the check the three credential routes
		// already use; reads are left alone, because a cross-origin page cannot
		// read what they answer.
		if r.Method != http.MethodGet && r.Method != http.MethodHead && !fromOurOwnPages(r) {
			s.log.Warn("command refused: it did not come from our own pages",
				"path", r.URL.Path, "from", from.Addr,
				"origin", r.Header.Get("Origin"),
				"sec_fetch_site", r.Header.Get("Sec-Fetch-Site"))
			writeJSONError(w, http.StatusForbidden, ErrCrossSite)
			return
		}

		if verdict == sessionValidStaleCookie {
			s.setSessionCookie(w, r, c.Value)
		}
		next.ServeHTTP(w, r)
	})
}

// answersWithAPage says whether it makes sense to answer this request with a
// redirect rather than with an error code.
//
// **`/ws` is not a page**, and the rule was written in only one of the two
// branches: whoever had no session received a 401, whoever arrived with the
// monitor having no password received a 303 towards /setup. To a WebSocket
// handshake a redirect means nothing — the client does not follow it and reports
// a generic failure, that is, the diagnosis is lost. It was not a security hole,
// because in neither case is the connection upgraded; it was the same rule
// applied by halves, and it is the kind of asymmetry noticed only by writing a
// test for the other branch.
func answersWithAPage(r *http.Request) bool {
	return !strings.HasPrefix(r.URL.Path, "/api/") && r.URL.Path != "/ws"
}

// ---------- pages ----------

func (s *Server) pageViewer(w http.ResponseWriter, r *http.Request) {
	// ServeMux routes "GET /" to any path not found: the root is told apart
	// from a real 404.
	if r.URL.Path != "/" {
		http.NotFound(w, r)
		return
	}
	s.serveAsset("index.html", "text/html; charset=utf-8")(w, r)
}

func (s *Server) pageLogin(w http.ResponseWriter, r *http.Request) {
	if !s.conf().HasPassword() {
		http.Redirect(w, r, "/setup", http.StatusSeeOther)
		return
	}
	s.serveAsset("login.html", "text/html; charset=utf-8")(w, r)
}

// setupNotFromThisPC refuses the first-configuration routes to whoever is not
// sitting at the PC the monitor runs on.
//
// **With no password there is no authentication, so `/setup` sets the password
// for whoever reaches it** — and rightly so at the first start, when the only way
// to get going is for somebody to set it. The problem is who "somebody" can be.
// If the password is cleared while the tunnel is already on, for the length of
// that window the public address means "set the password and take the camera":
// the first person through takes it, and the owner finds themselves locked out
// of their own house.
//
// **The home network is not the owner either, and it used to be let in.** The
// first version refused only the Internet, which left the window open to every
// device on the Wi-Fi: a guest's phone, a neighbour on a shared network, a
// compromised gadget. The privacy policy had to say "anyone on your home network
// can set it", which is a sentence nobody should have to publish. The proof of
// ownership is the same one the tray's commands use, physical presence at the
// machine, and on the wire that is a connection from this PC's own address,
// loopback or the one it arrived on (see sameAsLocal). The guided setup is
// opened on this PC by the program itself (`localhost`), so the first start
// costs nothing.
//
// At the first start the Internet case cannot arise anyway, because the funnel
// cannot be on: CanExposePublicly refuses precisely while there is no password.
// So it is a permanent rule and not a special case of the reset — which is how a
// protection survives whoever touches it without knowing its history.
//
// **It asks the origin, not `Public()`**: the funnel's visitor is recognised by
// tunnel.SourceAddr before the socket's address is looked at, so a request that
// Tailscale delivers over the node's local connection is never mistaken for one
// typed at this keyboard.
//
// **And the socket is not enough either, because a browser on this PC will
// carry somebody else's page to it.** A site whose name the attacker has just
// re-pointed at 127.0.0.1 — DNS rebinding — makes the owner's browser open a
// connection from loopback, and to that browser the page and the monitor are
// then the same origin: `Sec-Fetch-Site` says `same-origin`, `Origin` equals
// `Host`, and every check that compares the two is satisfied by the attacker's
// own name. What that name cannot be is one only this PC answers to, so the
// setup also asks what the request calls us — see namesThisPC.
func setupNotFromThisPC(r *http.Request) bool {
	return requestOrigin(r).Class != originThisPC || !namesThisPC(r.Host)
}

func (s *Server) pageSetup(w http.ResponseWriter, r *http.Request) {
	if s.conf().HasPassword() {
		http.Redirect(w, r, "/login", http.StatusSeeOther)
		return
	}
	if requestOrigin(r).Class == originThisPC && !namesThisPC(r.Host) {
		// At this PC, under a name rather than an address — the machine's
		// own, or one the router hands out. The person is where the setup is
		// done, so they are sent to the same page by the address they
		// arrived on rather than told they are somewhere else; a page that
		// re-pointed a name here gets a navigation to an origin it cannot
		// read.
		if local, ok := r.Context().Value(http.LocalAddrContextKey).(net.Addr); ok {
			http.Redirect(w, r, "http://"+local.String()+"/setup", http.StatusSeeOther)
			return
		}
	}
	if setupNotFromThisPC(r) {
		// **The one place where the server really writes a sentence**, because
		// here there is no page around it to hand the translation to: the
		// refusal happens before the HTML is served, so the browser shows the
		// body as it comes. The language is still chosen by whoever reads — the
		// catalogue is within the server's reach, and `Accept-Language` is their
		// declaration.
		http.Error(w, s.phrase(r, "err.setup-not-this-pc"), http.StatusForbidden)
		return
	}
	s.serveAsset("setup.html", "text/html; charset=utf-8")(w, r)
}

// pageOnboarding serves the first-configuration path.
//
// It never redirects: unlike /setup, which exists only while the password is
// missing, the path can be **resumed**. Whoever answered "at home is enough" at
// the fork and then changed their mind has to be able to come back, and whoever
// reopens it with the configuration finished finds the last step rather than a
// closed door: it is the page itself that jumps to the right point, by looking
// at the state.
func (s *Server) pageOnboarding(w http.ResponseWriter, r *http.Request) {
	s.serveAsset("onboarding.html", "text/html; charset=utf-8")(w, r)
}

// apiOnboardingState says whether a password already exists.
//
// **It is public, and the question it answers is not "am I in?".** The path used
// to infer the existence of the password from `/api/status` answering, that is,
// from "I have a valid session" — which is a different question, and with a
// password set but no session it gave the wrong answer: the first step showed
// the first-configuration form, and submitting it came back with "password
// already set". A guided path running into an error it could have foreseen.
//
// Being public adds nothing to what can already be seen from outside: `GET
// /setup` redirects to `/login` if and only if a password exists, and `GET /`
// redirects to `/setup` if it does not. The fact is already observable by
// anybody who can open a page; here it is said outright rather than inferred
// from a redirect.
func (s *Server) apiOnboardingState(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{
		"hasPassword": s.conf().HasPassword(),
		// `done` serves not to redo the whole path for one thing. After a reset
		// the password is missing but the path has already been done: whoever
		// chooses a new one wants to go back to watching, not to reconfigure
		// Tailscale.
		"done": s.conf().OnboardingDone,
	})
}

// apiQR engraves the address passed in `?u=` into a code.
//
// **It serves so that nothing has to be transcribed by hand.** Whoever has the
// monitor on a computer and the phone in their hand has to be able to open
// Tailscale's authorisation address by scanning it: an address copied wrong is a
// dead end that looks like a fault in the program.
//
// The text is passed by the page rather than taken by the server from the state,
// and that is not laziness: the page shows **one** address at a time — the
// authorisation one at step 4, the public one at step 7 — and it knows which.
// Deciding it here would need a second place repeating the same logic, and the
// two would diverge.
//
// Only `http` and `https` are served: the image is harmless but a code is made
// to be opened without being read, and engraving whatever arrives in the query
// would mean letting whoever crafts a link decide where it leads.
func (s *Server) apiQR(w http.ResponseWriter, r *http.Request) {
	// **It is engraved for whoever is in the house.** A QR code is made to be
	// pointed at with a phone that is in the same room as the screen, so from
	// the Internet this route has no use at all — and left open it was an
	// unauthenticated generator, on the owner's own trusted address, of a code
	// leading wherever the caller wrote. The two pages that ask for one are
	// both walked at home: step 4 shows Tailscale's authorisation address and
	// step 7 the public one, and both are read off a screen that is here.
	if requestOrigin(r).Public() {
		http.Error(w, "the code is engraved for whoever is in the house",
			http.StatusForbidden)
		return
	}
	u := r.URL.Query().Get("u")
	if !strings.HasPrefix(u, "http://") && !strings.HasPrefix(u, "https://") {
		http.Error(w, "invalid address", http.StatusBadRequest)
		return
	}
	m, err := qr.Encode(u)
	if err != nil {
		http.Error(w, err.Error(), http.StatusRequestEntityTooLarge)
		return
	}
	w.Header().Set("Content-Type", "image/svg+xml; charset=utf-8")
	_, _ = io.WriteString(w, m.SVG(240))
}

// apiOnboardingDone marks the path as finished.
//
// Finished means "do not reopen it by itself at the next start", not "it all
// went well": whoever skips the fork has finished the path without having access
// from outside the house. They are two different questions, and this one answers
// only the first.
func (s *Server) apiOnboardingDone(w http.ResponseWriter, r *http.Request) {
	if _, err := s.opts.Config.Set(func(c *config.Config) {
		c.OnboardingDone = true
	}); err != nil {
		s.log.Error("saving the configuration", "error", err)
		writeJSONError(w, http.StatusInternalServerError, ErrSaveFailed)
		return
	}

	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

// apiDetect switches the detection of the two sounds on and off.
//
// **It is not a preference of the viewer's**, and that is why it ends up in the
// monitor's configuration rather than in the browser: "there is a dog in this
// room" is a fact of the installation, and whoever opens the page from a second
// phone has to find the same answer. Behind authentication like everything else:
// whoever can watch the room can also say what is in it.
func (s *Server) apiDetect(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Cry    *bool `json:"cry"`
		Bark   *bool `json:"bark"`
		Motion *bool `json:"motion"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<10)).Decode(&body); err != nil {
		writeJSONError(w, http.StatusBadRequest, ErrBadRequest)
		return
	}

	updated, err := s.opts.Config.Set(func(c *config.Config) {
		// The fields are pointers so that **one** can be changed on its own:
		// with plain booleans, a request that speaks about crying would switch
		// barking off simply by not naming it.
		if body.Cry != nil {
			c.DetectCry = *body.Cry
		}
		if body.Bark != nil {
			c.DetectBark = *body.Bark
		}
		if body.Motion != nil {
			c.DetectMotion = *body.Motion
		}
	})
	if err != nil {
		s.log.Error("saving the configuration", "error", err)
		writeJSONError(w, http.StatusInternalServerError, ErrSaveFailed)
		return
	}

	s.log.Info("detection", "cry", updated.DetectCry,
		"bark", updated.DetectBark, "motion", updated.DetectMotion)
	writeJSON(w, http.StatusOK, map[string]any{
		"cry":    updated.DetectCry,
		"bark":   updated.DetectBark,
		"motion": updated.DetectMotion,
	})
}

// apiRemoteEnable switches on access from outside the house and persists the
// choice.
//
// **The order is check, write, switch on, and it is not arbitrary.**
// CanExposePublicly comes first because the address about to be published is
// reachable by anybody; the write comes before the switching on because a tunnel
// switched on and not persisted would vanish at the next start with nothing to
// say so, that is, the monitor would switch itself off from outside. If the
// write fails it is not switched on: better not done than half done.
func (s *Server) apiRemoteEnable(w http.ResponseWriter, r *http.Request) {
	if s.opts.EnableRemote == nil {
		writeJSONError(w, http.StatusNotImplemented, ErrRemoteUnavailable)
		return
	}

	// **A refusal and a failed write are two answers, and the store keeps them
	// apart.** Exposing with no password is a conflict and carries its own code;
	// a disk that will not take the file is ours and carries the generic one.
	// Both arrive here as an error, so which one is asked rather than assumed.
	if _, err := s.opts.Config.Update(func(c *config.Config) error {
		c.FunnelEnabled = true
		return c.CanExposePublicly()
	}); err != nil {
		if errors.Is(err, config.ErrSave) {
			s.log.Error("saving the configuration", "error", err)
			writeJSONError(w, http.StatusInternalServerError, ErrSaveFailed)
			return
		}
		writeJSONError(w, http.StatusConflict, s.codeFor(err))
		return
	}

	s.opts.EnableRemote()
	s.log.Info("remote access enabled from the guided setup")
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

func (s *Server) serveAsset(name, contentType string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		data, err := fs.ReadFile(s.assets, name)
		if err != nil {
			http.Error(w, "resource not found", http.StatusNotFound)
			return
		}
		w.Header().Set("Content-Type", contentType)
		_, _ = w.Write(data)
	}
}

// ---------- API ----------

type loginRequest struct {
	Password string `json:"password"`
	Confirm  string `json:"confirm"`
	// Current serves only the password change: it is the one in force, and it
	// has to be proved even by whoever already has a valid session. See
	// apiPassword.
	Current string `json:"current"`
}

// credentials extracts the credentials from a JSON request or from a form.
//
// The fallback to the form serves to guarantee that, even if the script were not
// executed, the password travels in a POST and not in the query string.
// isForm tells the two apart in order to decide how to answer: whoever has no
// JavaScript expects a redirect, not JSON.
func credentials(w http.ResponseWriter, r *http.Request) (req loginRequest, isForm bool, err error) {
	const maxBody = 4 << 10
	ct := r.Header.Get("Content-Type")

	// **Credentials are taken only from our own pages.** See fromOurOwnPages:
	// this is the check that stops somebody else's site posting a form here,
	// and the route it protects hardest is /api/setup, which by design accepts
	// a password from anyone at home while none is set.
	if !fromOurOwnPages(r) {
		return req, !strings.HasPrefix(ct, "application/json"), errCrossSite
	}

	if strings.HasPrefix(ct, "application/json") {
		err = json.NewDecoder(http.MaxBytesReader(w, r.Body, maxBody)).Decode(&req)
		return req, false, err
	}

	r.Body = http.MaxBytesReader(w, r.Body, maxBody)
	if err = r.ParseForm(); err != nil {
		return req, true, err
	}
	// A password sent by GET would be in the query string: it is refused rather
	// than accepted, so as not to normalise a credential leak.
	if r.Method != http.MethodPost {
		return req, true, errors.New("method not allowed for sending credentials")
	}
	req.Password = r.PostFormValue("password")
	req.Confirm = r.PostFormValue("confirm")
	req.Current = r.PostFormValue("current")
	return req, true, nil
}

// writeJSONError answers with a **code**, not with a sentence.
//
// The shape of the response does not change — the field is still called `error`
// — but the value is now an identifier the page translates with
// `T('err.' + code)`. The words are chosen by whoever shows, which is also the
// only one that knows what language it is speaking: the same answer is read by
// one browser and another.
func writeJSONError(w http.ResponseWriter, status int, code errCode) {
	writeJSON(w, status, map[string]any{"error": string(code)})
}

// writeJSONRetry adds how long is left before trying again.
//
// **The seconds travel as a number and not inside the sentence.** "try again in
// 1m30s" composed here would belong to one language, and the format of a
// duration changes with the language as much as the words do: the page receives
// an integer and writes the sentence its own grammar wants.
func writeJSONRetry(w http.ResponseWriter, status int, code errCode, after time.Duration) {
	writeJSON(w, status, map[string]any{
		"error":      string(code),
		"retryAfter": retryAfterSeconds(after),
	})
}

// retryAfterSeconds is how long is left, in whole seconds, **rounded up**.
//
// It used to round to the nearest, and the last half second of a lock therefore
// travelled as a zero: the page composed `err.too-many` with n = 0 and said
// "Too many attempts. Try again in 0 seconds." — which is the exact sentence
// `i18n.js` says its three-line check on `retryAfter` exists to prevent. That
// check guards an **absent** number and could not see one rounded away here.
//
// It is reachable by whoever is retrying in a tight loop, which is the caller
// this wait exists for in the first place: the limiter answers
// `time.Until(lockedUntil)`, a continuous duration, so every lock passes through
// that last half second on its way out.
//
// Rounding up is also the honest direction. Saying one second when four hundred
// milliseconds are left costs a wait nobody notices; saying zero invites the
// retry that is refused again, which is the loop the page was about to describe.
func retryAfterSeconds(after time.Duration) int {
	if after <= 0 {
		return 0
	}
	return int(math.Ceil(after.Seconds()))
}

// authError answers an authentication request, in JSON or by sending the caller
// back to the page.
//
// **The fallback without JavaScript carries the code, not the sentence.** It
// used to end up in the address as text already written; now it is an identifier
// the page translates as it would the JSON answer, so the two roads say the same
// thing in the same language rather than diverging.
func authError(w http.ResponseWriter, r *http.Request, isForm bool, page string, status int, code errCode) {
	authErrorRetry(w, r, isForm, page, status, code, 0)
}

func authErrorRetry(w http.ResponseWriter, r *http.Request, isForm bool, page string,
	status int, code errCode, after time.Duration) {

	if isForm {
		q := "?error=" + url.QueryEscape(string(code))
		if after > 0 {
			q += "&retryAfter=" + strconv.Itoa(retryAfterSeconds(after))
		}
		http.Redirect(w, r, page+q, http.StatusSeeOther)
		return
	}
	if after > 0 {
		writeJSONRetry(w, status, code, after)
		return
	}
	writeJSONError(w, status, code)
}

func (s *Server) apiLogin(w http.ResponseWriter, r *http.Request) {
	req, isForm, err := credentials(w, r)
	if err != nil {
		s.refuseCredentials(w, r, isForm, "/login", err)
		return
	}

	if !s.conf().HasPassword() {
		authError(w, r, isForm, "/setup", http.StatusConflict, ErrNoPassword)
		return
	}

	key := clientKey(r)
	done, d := s.limiter.begin(key)
	if d > 0 {
		w.Header().Set("Retry-After", fmt.Sprint(int(d.Seconds())+1))
		authErrorRetry(w, r, isForm, "/login", http.StatusTooManyRequests, ErrTooMany, d)
		return
	}
	defer done()

	release, ok := s.hashSlot(r)
	if !ok {
		return // the caller has gone: there is nobody to answer
	}
	verified := config.VerifyPassword(s.conf().PasswordHash, req.Password)
	release()

	if !verified {
		lock := s.limiter.fail(key)
		// The global slowdown is paid here and not earlier: whoever typed the
		// right password has already gone through, and must not wait because of
		// somebody else.
		wait := s.global.fail(time.Now())
		s.log.Warn("login refused", "from", key, "lock", lock, "global_wait", wait)
		sleepCtx(r.Context(), wait)
		if lock > 0 {
			w.Header().Set("Retry-After", fmt.Sprint(int(lock.Seconds())+1))
			authErrorRetry(w, r, isForm, "/login", http.StatusTooManyRequests, ErrTooMany, lock)
			return
		}
		authError(w, r, isForm, "/login", http.StatusUnauthorized, ErrWrongPassword)
		return
	}

	s.limiter.success(key)
	token, err := s.sessions.create(key, requestOrigin(r))
	if err != nil {
		authError(w, r, isForm, "/login", http.StatusInternalServerError, ErrSessionFailed)
		return
	}
	s.setSessionCookie(w, r, token)
	s.log.Info("login succeeded", "from", key)

	if isForm {
		http.Redirect(w, r, "/", http.StatusSeeOther)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

func (s *Server) apiLogout(w http.ResponseWriter, r *http.Request) {
	if c, err := r.Cookie(sessionCookieName); err == nil {
		s.sessions.revoke(c.Value)
	}
	http.SetCookie(w, &http.Cookie{
		Name: sessionCookieName, Value: "", Path: "/", MaxAge: -1,
		HttpOnly: true, SameSite: http.SameSiteStrictMode, Secure: isTLS(r),
	})
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

// ResetPassword clears the password and closes every session.
//
// **It is not an HTTP route, and it is RevokeAllSessions' reason**: whoever has
// forgotten the password cannot prove ownership with a credential, so the only
// proof left is physical presence in front of the machine. The tray calls it,
// where that presence is there by construction; exposing it as a URL would mean
// that anybody who can reach the monitor can lock out whoever has it at home.
//
// From here on the monitor is reachable by nobody until a new one is set —
// requireAuth imposes it — and the first configuration can only be done at this
// PC: see setupNotFromThisPC.
func (s *Server) ResetPassword() error {
	if _, err := s.opts.Config.Set(func(c *config.Config) {
		c.PasswordHash = ""
	}); err != nil {
		return err
	}

	n := s.sessions.revokeAll()
	s.log.Warn("password reset from the machine: the monitor is unreachable "+
		"until a new one is set", "sessions_closed", n)
	return nil
}

// RevokeAllSessions invalidates every open session and says how many it closed.
//
// **It is not an HTTP route, and that is deliberate.** Throwing every device out
// is an administrative command, and an administrative command is given from in
// front of the machine — where physical presence is already the proof of who you
// are — not from a public URL where the only proof is exactly the credential
// suspected of being compromised. Exposing it would add surface without adding
// anything for whoever has a right to it.
//
// The local interface will call it. In the meantime the remedy already exists
// and it is restarting the monitor: the sessions live in memory and do not
// survive it. This makes it a polite remedy rather than a brutal one, not a
// capability that is missing today.
//
// It also closes the session of whoever invokes it, if they have one: making an
// exception for the device in hand would make the count a half truth.
func (s *Server) RevokeAllSessions() int {
	n := s.sessions.revokeAll()
	s.log.Warn("all sessions revoked", "sessions_closed", n)
	return n
}

// apiPassword changes the password in force.
//
// Three constraints, and they only hold if all three are there.
//
// **The current password is wanted, even though the session is already valid.**
// A session cookie is a bearer token: it says "somebody had got in from this
// browser", not "it is you". Without this demand, anybody who comes to a browser
// left open — or to a stolen cookie — can lock the owner out of their own
// house's monitor, which is the worst fault this endpoint can produce.
//
// **It goes through /api/login's rate limiter.** A password is verified here, so
// without the limiter this route becomes an unwatched oracle for guessing it: a
// back door round all the work in auth.go. The global slowdown applies too, and
// for the same reason it does there — it is paid **only by getting it wrong**:
// whoever types the right one does not wait.
//
// **On the change every session dies, including that of whoever asks.** If the
// password is changed because it has fallen into somebody's hands, leaving the
// devices already in alive cancels the reason for the change. The caller gets
// back in on their own with the new one, so the price is paid by whoever should
// not have been there.
func (s *Server) apiPassword(w http.ResponseWriter, r *http.Request) {
	req, isForm, err := credentials(w, r)
	if err != nil {
		s.refuseCredentials(w, r, isForm, "/onboarding", err)
		return
	}
	if s.opts.Config == nil {
		authError(w, r, isForm, "/onboarding", http.StatusInternalServerError, ErrSaveFailed)
		return
	}
	if req.Password != req.Confirm {
		authError(w, r, isForm, "/onboarding", http.StatusBadRequest, ErrMismatch)
		return
	}

	key := clientKey(r)
	done, d := s.limiter.begin(key)
	if d > 0 {
		w.Header().Set("Retry-After", fmt.Sprint(int(d.Seconds())+1))
		authErrorRetry(w, r, isForm, "/onboarding", http.StatusTooManyRequests, ErrTooMany, d)
		return
	}
	defer done()
	release, ok := s.hashSlot(r)
	if !ok {
		return
	}
	verified := config.VerifyPassword(s.conf().PasswordHash, req.Current)
	release()

	if !verified {
		lock := s.limiter.fail(key)
		wait := s.global.fail(time.Now())
		s.log.Warn("password change refused: wrong current password", "from", key, "lock", lock)
		sleepCtx(r.Context(), wait)
		if lock > 0 {
			w.Header().Set("Retry-After", fmt.Sprint(int(lock.Seconds())+1))
			authErrorRetry(w, r, isForm, "/onboarding", http.StatusTooManyRequests, ErrTooMany, lock)
			return
		}
		authError(w, r, isForm, "/onboarding", http.StatusUnauthorized, ErrWrongCurrentPassword)
		return
	}
	s.limiter.success(key)

	// The hash is computed **before** taking the lock: argon2id costs hundreds
	// of milliseconds on purpose, and holding the rest of the server still for
	// that long would mean that whoever changes the password suspends the status
	// page of whoever is watching.
	release, ok = s.hashSlot(r)
	if !ok {
		return
	}
	hash, err := config.HashPassword(req.Password)
	release()
	if err != nil {
		authError(w, r, isForm, "/onboarding", http.StatusBadRequest, s.codeFor(err))
		return
	}

	if _, err := s.opts.Config.Set(func(c *config.Config) {
		c.PasswordHash = hash
	}); err != nil {
		s.log.Error("saving the configuration", "error", err)
		authError(w, r, isForm, "/onboarding", http.StatusInternalServerError, ErrSaveFailed)
		return
	}

	n := s.sessions.revokeAll()
	s.log.Warn("password changed: all sessions closed",
		"from", key, "sessions_closed", n)

	if isForm {
		http.Redirect(w, r, "/login", http.StatusSeeOther)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "sessionsClosed": n})
}

func (s *Server) apiSetup(w http.ResponseWriter, r *http.Request) {
	if setupNotFromThisPC(r) {
		o := requestOrigin(r)
		s.log.Warn("first-time setup refused: request not from this PC",
			"from", o.Kind, "address", o.Addr, "host", r.Host)
		writeJSONError(w, http.StatusForbidden, ErrSetupNotThisPC)
		return
	}
	req, isForm, err := credentials(w, r)
	if err != nil {
		s.refuseCredentials(w, r, isForm, "/setup", err)
		return
	}
	if req.Password != req.Confirm {
		authError(w, r, isForm, "/setup", http.StatusBadRequest, ErrMismatch)
		return
	}
	if s.opts.Config == nil {
		authError(w, r, isForm, "/setup", http.StatusInternalServerError, ErrSaveFailed)
		return
	}

	// **This route is rate limited like the login, and it used not to be at
	// all.** It is the one endpoint that takes a password from somebody who has
	// proved nothing, and that is by design — at the first start there is
	// nothing to prove with. What is not by design is that it could be called
	// without limit.
	key := clientKey(r)
	done, d := s.limiter.begin(key)
	if d > 0 {
		w.Header().Set("Retry-After", fmt.Sprint(int(d.Seconds())+1))
		authErrorRetry(w, r, isForm, "/setup", http.StatusTooManyRequests, ErrTooMany, d)
		return
	}
	defer done()

	// **The cheap refusal comes before the expensive work, and it used to come
	// after.** The password already being set is read with a lock and a string
	// comparison; hashing costs 64 MiB and a tenth of a second. With the order
	// the other way round every call on a monitor that is already configured —
	// that is, every call for the rest of the machine's life — bought an argon2
	// run before finding out there was nothing to do, from anyone on the home
	// network, with no limit and nothing written down.
	//
	// **It does not replace the check inside the store**, below, and must not
	// be read as doing so: two simultaneous first-time requests both pass this
	// one, and only the check held under the store's own lock stops the second
	// overwriting the first. This is the cheap gate, that is the correct one.
	if s.conf().HasPassword() {
		s.limiter.fail(key)
		s.log.Warn("first-time setup refused: the password is already set", "from", key)
		authError(w, r, isForm, "/login", http.StatusConflict, ErrAlreadySet)
		return
	}

	release, ok := s.hashSlot(r)
	if !ok {
		return
	}
	hash, err := config.HashPassword(req.Password)
	release()
	if err != nil {
		authError(w, r, isForm, "/setup", http.StatusBadRequest, s.codeFor(err))
		return
	}

	// Check and write in one operation: checking first and writing afterwards
	// would leave a window in which two simultaneous requests both pass the
	// check and the second overwrites the first one's password. The store holds
	// its lock across the change and the save, so the test belongs inside it.
	if _, err := s.opts.Config.Update(func(c *config.Config) error {
		if c.HasPassword() {
			return errPasswordAlreadySet
		}
		c.PasswordHash = hash
		return nil
	}); err != nil {
		if errors.Is(err, errPasswordAlreadySet) {
			authError(w, r, isForm, "/login", http.StatusConflict, ErrAlreadySet)
			return
		}
		s.log.Error("saving the configuration", "error", err)
		authError(w, r, isForm, "/setup", http.StatusInternalServerError, ErrSaveFailed)
		return
	}

	s.limiter.success(key)
	s.log.Info("password set during the initial setup", "from", key)
	if isForm {
		http.Redirect(w, r, "/login", http.StatusSeeOther)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

func (s *Server) apiStatus(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, s.Status())
}

// Status is the monitor's state with, inside it, what **only the server** knows:
// how many sessions are open, the version, and the detection toggles, which live
// in the configuration the server holds.
//
// **It is exported because there are two readers**, and for a while this work
// sat inside the HTTP route: the page came through here and saw the fields
// filled, the tray called `StatusFn` for itself and saw them at zero. From
// outside this is what showed — the panel declaring "no device connected" with a
// phone connected, while the same line on the page counted two, **at the same
// instant and from the same state**.
//
// It is the rule this file already states two paragraphs below — **whoever owns
// a value publishes it** — missed in the easiest way: the value was published,
// but by a route instead of by whoever owns it, and a route is called by only
// one of the two. `TestTheRouteComposesNothing` nails the shape: nothing is
// filled in the route.
func (s *Server) Status() Status {
	var st Status
	if s.opts.StatusFn != nil {
		st = s.opts.StatusFn()
	}
	st.Devices = s.sessions.count()
	// The version is put in by the server and not by whoever builds the Status:
	// it is a fact of the binary, and passing it from outside would mean being
	// able to forget it.
	st.Version = version.Full()
	// **The toggles are reported by whoever changes them.** They were left to
	// whoever builds the Status, which is elsewhere: it took no more than not
	// filling them for the status to declare "off" while the configuration had
	// them on. From outside this is what showed — two grey buttons, one of them
	// pressed and both turning green, because the route's answer was the only
	// one telling the truth.
	//
	// The general rule: **whoever owns a value publishes it.** If writing it and
	// reading it are two different places, sooner or later one of them does not.
	// One read for the five: taken separately they could come from two
	// different configurations.
	c := s.opts.Config.Get()
	st.DetectCry, st.DetectBark = c.DetectCry, c.DetectBark
	st.DetectMotion = c.DetectMotion
	// The **chosen** microphone lives in the configuration, which the server
	// holds: whoever builds the Status knows which one is open, not which one
	// was asked for.
	st.MicrophoneChosen = c.MicDeviceID
	// And the same for the camera, for the same reason.
	st.CameraChosen = c.CameraDeviceID
	return st
}

func (s *Server) setSessionCookie(w http.ResponseWriter, r *http.Request, token string) {
	http.SetCookie(w, &http.Cookie{
		Name:  sessionCookieName,
		Value: token,
		Path:  "/",
		// Secure only over TLS: imposing it always would block access on the LAN
		// over plain HTTP, where the browser would never send the cookie.
		Secure:   isTLS(r),
		HttpOnly: true,
		SameSite: http.SameSiteStrictMode,
		MaxAge:   s.conf().SessionTTLHours * 3600,
	})
}

// isTLS says whether the request arrived encrypted.
//
// **It used to believe `X-Forwarded-Proto` as well**, with a comment saying the
// Funnel terminated TLS and forwarded in the clear on loopback. It does not:
// `ListenFunnel` hands back a TLS listener, so a request through the Funnel has
// `r.TLS` like any other HTTPS request. The header was a value the caller
// writes, and the only thing it could change was the caller's own cookie.
func isTLS(r *http.Request) bool {
	return r.TLS != nil
}

// ---------- WebRTC signalling ----------

// The reasons the signalling stops, as codes.
//
// They are constants and not literals scattered about because **three**
// consumers read them — the viewer, the configuration path and `pat-viewer` —
// and a code added without updating them all shows only as an empty line in the
// wrong place. It happened: the first draft changed the field and updated one
// page.
const (
	// reasonNotReady: there is no video stream to offer yet.
	reasonNotReady = "not-ready"
	// reasonSDP: the browser's answer was not accepted.
	reasonSDP = "sdp"
	// reasonTooMany: as many viewer sessions are open as the house's uplink
	// is allowed to carry. See rtc.ErrTooManyViewers.
	reasonTooMany = "too-many-viewers"
)

type signalMessage struct {
	Type      string                     `json:"type"`
	SDP       *webrtc.SessionDescription `json:"sdp,omitempty"`
	Candidate *webrtc.ICECandidateInit   `json:"candidate,omitempty"`
	Message   string                     `json:"message,omitempty"`
	// Reason says **why** the signalling stopped, with a code.
	//
	// `err.Error()` used to travel here, that is, the text of a Go error written
	// for the log — "stream not ready yet: no keyframe received",
	// "SetRemoteDescription: ..." — and the page printed it as it came beside
	// the status dot, in the middle of a vocabulary otherwise made of two small
	// words. They were also the only sixteen strings of `internal/rtc` that
	// anybody read on screen, and keeping them in one language for that would
	// have kept a whole package in it.
	//
	// Codes cross the API, the words live at the edges: the detail stays in the
	// log, where it is wanted, and the sentence is chosen by whoever draws the
	// page — which is also the only one that knows what language it is speaking.
	Reason    string `json:"reason,omitempty"`
	Transport string `json:"transport,omitempty"`
	// ICEServers goes with the offer: without them the browser discovers only
	// its own local addresses and the connection succeeds only on the same
	// network.
	ICEServers []webrtc.ICEServer `json:"iceServers,omitempty"`
	// TalkMid identifies the talk-back's m-line in the offer.
	//
	// **The server says it rather than making the page look for it.** There are
	// two audio m-lines — the room going out, the voice coming in — and telling
	// them apart from outside would mean inferring them from the order or the
	// direction: two clues that change at the first touch of the offer, and that
	// if got wrong would attach the microphone to the wrong track with no error
	// at all.
	TalkMid string `json:"talkMid,omitempty"`
}

func (s *Server) wsSignaling(w http.ResponseWriter, r *http.Request) {
	if err := checkOrigin(r); err != nil {
		s.log.Warn("WebSocket refused", "reason", err, "origin", r.Header.Get("Origin"))
		http.Error(w, "origin not allowed", http.StatusForbidden)
		return
	}

	conn, err := websocket.Accept(w, r, &websocket.AcceptOptions{
		// The origin has already been checked above with a comparison on the
		// host.
		InsecureSkipVerify: true,
	})
	if err != nil {
		s.log.Warn("WebSocket upgrade failed", "error", err)
		return
	}
	defer conn.CloseNow()

	ctx, cancel := context.WithCancel(r.Context())
	defer cancel()

	// The origin is read now, while the HTTP request is still there: the real
	// address of whoever arrives through the funnel is in its context, not in a
	// header.
	from := requestOrigin(r)

	viewer, offer, err := s.opts.Hub.NewViewer()
	if err != nil {
		s.refuseViewer(from, err)
		reason := reasonNotReady
		if errors.Is(err, rtc.ErrTooManyViewers) {
			reason = reasonTooMany
		}
		_ = wsWrite(ctx, conn, signalMessage{Type: "error", Reason: reason})
		return
	}
	s.viewerAdmitted()
	// Signalling that ends does **not** close media that is flowing.
	//
	// After the negotiation the WebSocket is no use until there is something to
	// renegotiate: audio and video travel on their own. Closing the session
	// because the signalling channel fell turns an invisible hiccup into an
	// interruption that shows, and that is exactly what happened — measured, two
	// sessions cut at 2m34s each. Whoever really leaves is announced by ICE,
	// which loses consent in a few seconds and makes the connection's state
	// machine trigger the close.
	defer func() {
		if viewer.CloseUnlessConnected() {
			return
		}
		s.log.Debug("signalling closed but the media continues", "session", viewer.ID())
	}()

	// Periodic ping: a WebSocket with no traffic is closed by intermediaries,
	// and after the negotiation this one has none by definition. Without it, the
	// browser renegotiates from scratch every couple of minutes.
	guard.Go(s.log, "the WebSocket keep-alive", func() { keepAlive(ctx, conn, cancel) })

	// Trickle ICE towards the browser: sending the candidates as soon as they
	// are available noticeably shortens the time to first frame.
	viewer.OnICECandidate(func(c *webrtc.ICECandidate) {
		if c == nil {
			return
		}
		init := c.ToJSON()
		if err := wsWrite(ctx, conn, signalMessage{Type: "candidate", Candidate: &init}); err != nil {
			cancel()
		}
	})

	// The STUN servers travel with the offer rather than being written into the
	// page: they are configurable, and the page is static.
	if err := wsWrite(ctx, conn, signalMessage{
		Type:       "offer",
		SDP:        offer,
		ICEServers: s.opts.Hub.ICEServers(),
		TalkMid:    viewer.TalkMid(),
	}); err != nil {
		return
	}

	// Closes the WebSocket if the WebRTC session dies.
	guard.Go(s.log, "closing the WebSocket with the session", func() {
		select {
		case <-viewer.Done():
			cancel()
		case <-ctx.Done():
		}
	})

	for {
		var msg signalMessage
		if err := wsRead(ctx, conn, &msg); err != nil {
			if ctx.Err() == nil && !isNormalClose(err) {
				s.log.Debug("signalling read finished", "error", err)
			}
			return
		}

		switch msg.Type {
		case "answer":
			if msg.SDP == nil {
				continue
			}
			if err := viewer.SetAnswer(*msg.SDP); err != nil {
				// What the browser declared it supported is logged: with a codec
				// error it is the only way of understanding which
				// profile-level-id it expected, instead of guessing.
				s.log.Warn("SDP answer refused",
					"error", err,
					"h264_we_offered", s.opts.Hub.ProfileLevelID(),
					"codecs_the_browser_accepted", codecsIn(msg.SDP.SDP),
					"user_agent", r.Header.Get("User-Agent"))
				_ = wsWrite(ctx, conn, signalMessage{Type: "error", Reason: reasonSDP})
				return
			}
			// From here on the session has an observer: it announces who
			// connected and from where, tells the UI the chosen path and
			// measures the session to the end. It is the only trace left of a
			// viewing done far from the PC.
			//
			// The context loses cancellation but keeps its values: the observer
			// has to outlive the WebSocket, otherwise the final report would be
			// missing precisely in the sessions where the signalling fell first —
			// that is, the ones to investigate.
			// **The header is read here and not inside the goroutine.** It
			// used to be an argument of a `go` statement, which evaluates its
			// arguments at once; moved into a closure it would be read
			// whenever that goroutine got round to it — and this observer is
			// built with `WithoutCancel` precisely to outlive the WebSocket,
			// that is, to still be running after the handler has returned and
			// the request is no longer ours to read.
			agent := r.Header.Get("User-Agent")
			guard.Go(s.log, "the session observer", func() {
				s.watchSession(context.WithoutCancel(ctx), func(path string) {
					_ = wsWrite(ctx, conn, signalMessage{Type: "transport", Transport: path})
				}, viewer, from, agent)
			})
		case "candidate":
			if msg.Candidate == nil {
				continue
			}
			if err := viewer.AddICECandidate(*msg.Candidate); err != nil {
				s.log.Debug("ICE candidate discarded", "error", err)
			}
		case "bye":
			return
		}
	}
}

// keepAliveInterval is how often a ping is sent on the signalling WebSocket.
//
// Measured in the field: through the funnel and a mobile network, a WebSocket
// with no traffic is closed after about two and a half minutes. Thirty seconds
// sit well below any threshold of that kind and cost one packet every half
// minute.
const keepAliveInterval = 30 * time.Second

// keepAlive keeps the WebSocket alive and notices when the browser is gone.
//
// The ping serves two things at once: it stops intermediaries treating the
// connection as abandoned, and it gives a way of discovering that it has gone —
// without it, a dead WebSocket would stay open on our side until the next
// message, which after the negotiation might never come.
func keepAlive(ctx context.Context, conn *websocket.Conn, onDead func()) {
	t := time.NewTicker(keepAliveInterval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			pingCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
			err := conn.Ping(pingCtx)
			cancel()
			if err != nil {
				onDead()
				return
			}
		}
	}
}

// codecsIn summarises the codecs declared in an SDP, one per a=rtpmap line, with
// the profile-level-id where present.
//
// Listing every codec and not the H.264 profiles alone is deliberate: a "codec
// is not supported by remote" error does not say which track failed, and looking
// only at the video has already led to hunting the problem on the wrong side
// while it was the audio that did not match.
func codecsIn(sdp string) []string {
	// profile-level-id is on an a=fmtp:<pt> line, to be matched with the rtpmap
	// of the same payload type.
	fmtps := map[string]string{}
	for line := range strings.SplitSeq(sdp, "\n") {
		line = strings.TrimSpace(line)
		rest, ok := strings.CutPrefix(line, "a=fmtp:")
		if !ok {
			continue
		}
		pt, params, found := strings.Cut(rest, " ")
		if !found {
			continue
		}
		for param := range strings.SplitSeq(params, ";") {
			if id, ok := strings.CutPrefix(strings.TrimSpace(param), "profile-level-id="); ok {
				fmtps[pt] = id
			}
		}
	}

	var out []string
	for line := range strings.SplitSeq(sdp, "\n") {
		line = strings.TrimSpace(line)
		rest, ok := strings.CutPrefix(line, "a=rtpmap:")
		if !ok {
			continue
		}
		pt, desc, found := strings.Cut(rest, " ")
		if !found {
			continue
		}
		entry := desc
		if id, ok := fmtps[pt]; ok {
			entry += " (" + id + ")"
		}
		out = append(out, entry)
	}
	if len(out) == 0 {
		return []string{"none declared"}
	}
	return out
}

// hashSlots is how many argon2id hashes may be running at the same instant.
//
// **The parameters are chosen to be expensive, which makes them a lever
// somebody else can pull.** Each hash takes 64 MiB and about a tenth of a
// second (`internal/config`), and every wrong password on /api/login costs one
// — so without a ceiling, a hundred requests arriving together ask this process
// for 6.4 GB. Four of them is 256 MiB, which is the same order as the frame
// buffers already in flight, and four concurrent logins is far more than a
// house produces: the tray, a phone and a laptop are three, and they do not
// arrive in the same tenth of a second.
//
// **It bounds the work, and on its own it delayed whoever was right**: the
// queue for a slot is first come first served, so a correct password waited
// behind every wrong one already queued — measured by an audit at minutes, with
// a thousand guesses sent at once. One attempt per address (limiter.begin)
// shortens that queue to one per address, and homeSlots keeps the house out of
// it altogether.
const hashSlots = 4

// homeSlots of the hashSlots are kept for requests that did not come from the
// Internet.
//
// **The queue the Internet can fill is not the queue the house waits in.** A
// caller with many addresses can keep every shared slot busy for as long as it
// likes, and nothing before the hash can tell that caller from the owner on a
// phone far away — that is the price of paying only by getting it wrong. What
// can be told apart is the road: a login from the house, the tailnet or this
// PC takes the kept slot or a shared one, whichever frees first, so an attack
// through the Funnel slows the Funnel and never the parent at home.
const homeSlots = 1

// hashSlot takes one of the hashing slots and hands back the way to give it up.
//
// It reports false when the request went away first: at that point there is
// nobody to answer, and starting a tenth of a second of work for a closed
// connection is exactly what an attacker asking for a hundred of them wants.
func (s *Server) hashSlot(r *http.Request) (release func(), ok bool) {
	var home chan struct{}
	if !requestOrigin(r).Public() {
		home = s.hashingHome
	}
	// A nil channel is never ready, so from the Internet the second case is
	// simply not there.
	select {
	case s.hashing <- struct{}{}:
		return func() { <-s.hashing }, true
	case home <- struct{}{}:
		return func() { <-home }, true
	case <-r.Context().Done():
		return func() {}, false
	}
}

// refuseCredentials answers the two ways a submission can be unusable, and
// tells them apart.
//
// A malformed body is a 400 and nothing more; a submission from somebody else's
// page is a 403 **and a line in the log**, because that one is not a mistake —
// it is a page in the owner's browser posting a password of its own choosing at
// the monitor, and whoever reads the log afterwards has no other way of knowing
// it happened.
func (s *Server) refuseCredentials(w http.ResponseWriter, r *http.Request, isForm bool, page string, err error) {
	if errors.Is(err, errCrossSite) {
		s.log.Warn("credentials refused: the submission did not come from our own pages",
			"path", r.URL.Path, "from", clientKey(r),
			"origin", r.Header.Get("Origin"),
			"sec_fetch_site", r.Header.Get("Sec-Fetch-Site"))
		authError(w, r, isForm, page, http.StatusForbidden, ErrCrossSite)
		return
	}
	authError(w, r, isForm, page, http.StatusBadRequest, ErrBadRequest)
}

// errCrossSite is credentials' refusal of a submission from somebody else's
// page. It is a sentinel because the three routes that take a password all
// answer it the same way and none of them can tell it from a malformed body
// otherwise.
var errCrossSite = errors.New("the credentials did not come from our own pages")

// fromOurOwnPages says whether the browser declared this request as started by
// a page of ours.
//
// **A form POST from any site in the world is a request this server used to
// serve.** /api/login, /api/password and /api/setup accept
// `application/x-www-form-urlencoded`, which is a *simple request*: no
// preflight, no permission asked, and the attacker never needs to read the
// answer — the write has already happened. The one that matters is /api/setup,
// which is open by construction while no password is set: any page the owner's
// browser visits could post a password of its own choosing to the monitor's
// address on the home network, and then walk in through the Funnel, which stays
// up. That window is not hypothetical — it is what the tray's "reset the
// password" opens.
//
// **The cookie's SameSite does not cover these three.** Everything else that
// changes state sits behind requireAuth, which carries a cookie a browser will
// not send cross-site (`SameSite=Strict`) and asks this same function besides,
// because SameSite does not stop a sibling origin of the same site. These three
// carry no cookie at all: they are how one is obtained.
//
// **Nor does the Content-Security-Policy.** `form-action 'self'` is enforced on
// the document that *contains* the form, so it constrains our pages and says
// nothing about somebody else's.
//
// Two headers, in this order:
//
//   - `Sec-Fetch-Site` is the browser's own classification of the request and
//     cannot be set by script. `same-origin` is one of our pages; `none` is the
//     user themselves — a typed address or a bookmark — which no form
//     submission can be. Anything else, `cross-site` and `same-site` alike, is
//     refused: `same-site` is not `same-origin`, and on a `*.ts.net` address a
//     sibling name is exactly the neighbour we are being protected from.
//   - `Origin`, for a browser too old to send the first. It is compared with
//     the host the request arrived on, as checkOrigin already does for the
//     WebSocket.
//
// **A request with neither header is allowed through**, and that is a decision
// rather than an oversight: it is curl, it is the tests, it is any client that
// is not a browser — and none of those is riding somebody's session, which is
// the whole of what this check is about. Every browser that can submit a form
// cross-site sends at least Origin.
func fromOurOwnPages(r *http.Request) bool {
	switch r.Header.Get("Sec-Fetch-Site") {
	case "same-origin", "none":
		return true
	case "":
		// Too old to say. Origin below.
	default:
		return false
	}
	return checkOrigin(r) == nil
}

// checkOrigin compares Origin's host with the request's.
//
// It serves against cross-site WebSocket hijacking: without this check a
// third-party page open in the same browser could open a WebSocket authenticated
// by the cookie and receive the stream.
func checkOrigin(r *http.Request) error {
	origin := r.Header.Get("Origin")
	if origin == "" {
		return nil // non-browser client: no origin semantics
	}
	u, err := url.Parse(origin)
	if err != nil {
		return fmt.Errorf("origin cannot be parsed")
	}
	if !strings.EqualFold(u.Host, r.Host) {
		return fmt.Errorf("origin %q differs from host %q", u.Host, r.Host)
	}
	return nil
}

// ---------- utilities ----------

const wsMessageLimit = 256 << 10 // an SDP sits well below this threshold

func wsWrite(ctx context.Context, conn *websocket.Conn, msg signalMessage) error {
	data, err := json.Marshal(msg)
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	return conn.Write(ctx, websocket.MessageText, data)
}

func wsRead(ctx context.Context, conn *websocket.Conn, msg *signalMessage) error {
	conn.SetReadLimit(wsMessageLimit)
	_, data, err := conn.Read(ctx)
	if err != nil {
		return err
	}
	return json.Unmarshal(data, msg)
}

func isNormalClose(err error) bool {
	if ce, ok := errors.AsType[websocket.CloseError](err); ok {
		return ce.Code == websocket.StatusNormalClosure || ce.Code == websocket.StatusGoingAway
	}
	return errors.Is(err, context.Canceled)
}

func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(v)
}
