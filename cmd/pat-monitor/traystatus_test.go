package main

import (
	"strings"
	"testing"
	"time"

	"patmonitor/internal/config"
	"patmonitor/internal/detect"
	"patmonitor/internal/i18n"
	"patmonitor/internal/server"
	"patmonitor/internal/tray"
	"patmonitor/internal/tunnel"
	"patmonitor/internal/update"
)

// **These tests exist for a fault that had not gone off yet.** The branch that
// warns about digital silence compared an Italian sentence, that is, the text
// `MicHealth.String()` produces for the log: translating the interface, or even
// just rewriting that sentence, would have turned the warning off **with no
// error anywhere**. A microphone that delivers zeros is the worst fault a baby
// monitor can have — green page, still meter, crying child — and the tray is the
// only place that declares it when nobody is watching the page.
//
// The code is now a code, and these tests keep it hooked: they build the status
// **the way the program builds it**, that is, through `detect.MicHealth.Code()`,
// so a day on which the two sides came apart would not pass.

// healthyStatus is a monitor with nothing wrong with it, and from which each
// test changes only the one thing it cares about.
//
// **The last frame is part of being healthy**, and it was not here before: the
// only thing said about the picture was `Ready`, a latch that a monitor whose
// camera stopped an hour ago still satisfies. Every test in this package that
// starts from "healthy" starts from a picture that is arriving now, which is
// what the word means.
func healthyStatus() server.Status {
	return server.Status{
		Ready:            true,
		LastFrameUnix:    time.Now().Unix(),
		Camera:           "Integrated Camera",
		Microphone:       "Microphone Array",
		MicrophoneActive: true,
		RawAudio:         true,
		MicHealth:        detect.MicOK.Code(),
		Uptime:           "1m0s",
	}
}

func withPassword() config.Config {
	c := config.Default()
	c.PasswordHash = "$2a$10$doesnotmatter"
	return c
}

// **Faults are compared by code**, which is the whole point: a comparison
// against a sentence dies the day the sentence moves, and the tray's language is
// chosen by Windows.

func TestDigitalSilenceRaisesATrayWarning(t *testing.T) {
	s := healthyStatus()
	s.MicHealth = detect.MicDigitalSilence.Code()

	got := trayStatus(s, withPassword(), time.Now(), trayDict(t), update.State{})

	if got.Phase != tray.PhaseCheck {
		t.Errorf("phase %v, expected PhaseCheck: the microphone delivers zeros and the tray does not say so", got.Phase)
	}
	if got.Fault != tray.FaultMicSilent {
		t.Errorf("fault = %q, expected the digital-silence one", got.Fault)
	}
}

// **The test that catches the translation.** The digital-silence code is not the
// sentence shown: if one day somebody merged them, or translated the code, this
// one fails.
func TestTheMicCodeIsNotASentence(t *testing.T) {
	c := detect.MicDigitalSilence.Code()
	if c != "digital-silence" {
		t.Errorf("code = %q: changing it breaks the page and the tray together", c)
	}
	if c == detect.MicDigitalSilence.String() {
		t.Error("code and sentence coincide: the sentence is for the log and will change with the language")
	}
	// Every judgement has its own code, otherwise two different states would
	// reach whoever has to decide indistinguishable from each other.
	seen := map[string]bool{}
	for _, h := range []detect.MicHealth{detect.MicOK, detect.MicDigitalSilence, detect.MicSuspiciouslyQuiet} {
		if seen[h.Code()] {
			t.Errorf("repeated code: %q", h.Code())
		}
		seen[h.Code()] = true
	}
}

func TestAMissingMicRaisesATrayWarning(t *testing.T) {
	s := healthyStatus()
	s.MicrophoneActive = false

	got := trayStatus(s, withPassword(), time.Now(), trayDict(t), update.State{})

	if got.Phase != tray.PhaseCheck {
		t.Errorf("phase %v, expected PhaseCheck", got.Phase)
	}
	if got.Fault != tray.FaultMicMissing {
		t.Errorf("fault = %q, expected the missing-microphone one", got.Fault)
	}
}

// **The device's name is no longer a sentinel value.** A microphone called
// "missing" must not be able to simulate a fault, nor a microphone that is off
// hide behind any name at all.
func TestTheMicNameDecidesNothing(t *testing.T) {
	s := healthyStatus()
	s.Microphone = "missing"

	if got := trayStatus(s, withPassword(), time.Now(), trayDict(t), update.State{}); got.Phase == tray.PhaseCheck {
		t.Error("a microphone called \"missing\" raised a warning: the name is deciding instead of the state")
	}
}

func TestAHealthyMonitorRaisesNoWarning(t *testing.T) {
	got := trayStatus(healthyStatus(), withPassword(), time.Now(), trayDict(t), update.State{})

	if got.Phase != tray.PhaseHome {
		t.Errorf("phase %v, expected PhaseHome", got.Phase)
	}
	if got.Fault != tray.FaultNone {
		t.Errorf("unexpected fault: %q — a menu that always warns no longer warns", got.Fault)
	}
}

// The order of the cases **is** the scale of severity: with the capture stalled
// and the microphone missing together, the greater fault is declared. A colour
// can say only one thing.
func TestTheWorseFaultWins(t *testing.T) {
	s := healthyStatus()
	s.Ready = false
	s.MicrophoneActive = false

	got := trayStatus(s, withPassword(), time.Now().Add(-time.Minute), trayDict(t), update.State{})

	if got.Phase != tray.PhaseFault {
		t.Errorf("phase %v, expected PhaseFault: the capture is not starting and the microphone is being reported", got.Phase)
	}
}

// **Every state worth telling says something**, as a code.
//
// The tray's tooltip shows a single line, and if the status carries neither a
// fault nor a note that line falls back on the counters: whoever brushes the
// icon with the camera stalled would read "nobody is watching". The test lists
// the states that have to have an answer and checks that they do — and for the
// two healthy ones it checks the opposite, because a monitor that is well has
// nothing to declare and the right line is precisely the counters'.
//
// **The length is no longer tested here.** The cap is `szTip`'s, and the
// sentences now live in the catalogues: the measurement is made in
// `internal/tray`, over **every** language, which is the only place they can all
// be seen from.
func TestEveryStateWorthTellingHasACode(t *testing.T) {
	remote := healthyStatus()
	remote.Remote.Phase = tunnel.PhaseRunning
	remote.Remote.PublicURL = "https://patmon.quercia-lieve.ts.net/"

	silent := healthyStatus()
	silent.MicHealth = detect.MicDigitalSilence.Code()

	noMic := healthyStatus()
	noMic.MicrophoneActive = false

	// **The case that actually happens**, and the one the pair of fields makes
	// easy to miss: a machine with no microphone has never opened one, so
	// `RawAudio` is its zero value and reads exactly like raw refused. Above,
	// `noMic` leaves it true, which is the state after a microphone that was
	// working is unplugged — the two roads into the same absence, and the case
	// order has to send both to `mic-missing`. The viewer had the same pair and
	// answered "audio filtered by the system" over a microphone that was not
	// there.
	noMicEver := healthyStatus()
	noMicEver.MicrophoneActive = false
	noMicEver.RawAudio = false

	filtered := healthyStatus()
	filtered.RawAudio = false

	starting := healthyStatus()
	starting.Ready = false

	cases := map[string]struct {
		st    server.Status
		cfg   config.Config
		since time.Duration // how long it has been running: decides between "starting" and "capture stalled"
		fault tray.Fault
		note  tray.Note
	}{
		"healthy":            {healthyStatus(), withPassword(), time.Hour, tray.FaultNone, tray.NoteNone},
		"away from home":     {remote, withPassword(), time.Hour, tray.FaultNone, tray.NoteNone},
		"no password":        {healthyStatus(), config.Default(), time.Hour, tray.FaultNoPassword, tray.NoteNoPassword},
		"silence":            {silent, withPassword(), time.Hour, tray.FaultMicSilent, tray.NoteNone},
		"microphone gone":    {noMic, withPassword(), time.Hour, tray.FaultMicMissing, tray.NoteNone},
		"never a microphone": {noMicEver, withPassword(), time.Hour, tray.FaultMicMissing, tray.NoteNone},
		"filtered audio":     {filtered, withPassword(), time.Hour, tray.FaultMicFiltered, tray.NoteNone},
		"starting up":        {starting, withPassword(), time.Second, tray.FaultNone, tray.NoteStarting},
		"capture stalled":    {starting, withPassword(), time.Hour, tray.FaultCaptureStopped, tray.NoteNone},
	}
	for name, c := range cases {
		out := trayStatus(c.st, c.cfg, time.Now().Add(-c.since), trayDict(t), update.State{})
		if out.Fault != c.fault {
			t.Errorf("%s: fault = %q, expected %q", name, out.Fault, c.fault)
		}
		if out.Note != c.note {
			t.Errorf("%s: note = %q, expected %q", name, out.Note, c.note)
		}
	}
}

// **The address handed to another device is never localhost.**
//
// `homeAddress` asks the system which interface it would use, and the result
// sits in `server.Status.LocalURL`. `trayStatus` used to **recompute** it with
// `localURL`, which on a listen across all interfaces answers `localhost`: the
// page received the real address and the tray panel localhost, from the same
// status and at the same instant. Two lists of the same thing always diverge.
//
// It showed only while the tunnel was not up yet, because afterwards the public
// address takes this one's place — that is, in the first seconds after startup,
// and for as long as it takes to look at the panel. What one saw was an address
// that holds only for this computer **and a QR code leading to it**: a false
// affordance costs more than a missing one.
//
// The test looks at the two fields together, because the defect was that they
// were one: the address to hand out has to come from the status, the one the
// click opens has to stay here.
func TestTheAddressHandedToOtherDevicesIsNotLocalhost(t *testing.T) {
	const home = "http://192.168.1.85:8080/"

	for name, cfg := range map[string]config.Config{
		"with a password": withPassword(),
		"with none":       config.Default(),
	} {
		s := healthyStatus()
		s.LocalURL = home

		out := trayStatus(s, cfg, time.Now(), trayDict(t), update.State{})

		// With no password none is handed out at all: see below.
		if cfg.HasPassword() && out.HomeURL != home {
			t.Errorf("%s: the home address is %q instead of %q: it was recomputed "+
				"instead of being taken from the status", name, out.HomeURL, home)
		}
		if strings.Contains(out.HomeURL, "localhost") {
			t.Errorf("%s: the panel would show %q and engrave its QR code: "+
				"only this computer sees it", name, out.HomeURL)
		}
		// The other half: the click on the icon opens **here**, and there
		// localhost is the right answer — it works with no network and it is the
		// same origin as SetupURL, that is, the same session.
		if !strings.Contains(out.OpenURL, "localhost") {
			t.Errorf("%s: the click would open %q instead of an address of this "+
				"machine", name, out.OpenURL)
		}
	}

	// With no password the click leads to choosing one, here, and the home
	// address is not handed out: the password is set only at this PC, so a QR
	// code would lead a phone to a refusal. The defect was put back (the line
	// clearing it removed) and this test failed with it.
	s := healthyStatus()
	s.LocalURL = home
	out := trayStatus(s, config.Default(), time.Now(), trayDict(t), update.State{})
	if !strings.HasSuffix(out.OpenURL, "/setup") {
		t.Errorf("with no password the click opens %q instead of the page that asks for one", out.OpenURL)
	}
	if out.HomeURL != "" {
		t.Errorf("with no password the panel hands out %q: the setup is refused "+
			"from any device but this PC", out.HomeURL)
	}
}

// **When the only address is this machine's, none is handed out.**
//
// `homeAddress` never comes back empty: with no IPv4 route its last fallback is
// `localhost`, which for the page running here is true and for the panel is
// false — there that address is **shown** and **engraved in a QR code**, that
// is, it invites a gesture that cannot succeed. The panel already knows how to
// treat an empty one: no line and no code.
//
// **There are two roads to close**, the name and the number: looking only at the
// word lets `127.0.0.1` through, looking only at the number lets `localhost`
// through. And it is the case in which a test on a single value absolves the
// defect it is meant to catch.
func TestAnAddressOnlyThisMachineCanSeeIsNotHandedOut(t *testing.T) {
	onlyHere := []string{
		"http://localhost:8080/",
		"http://LOCALHOST:8080/",
		"http://127.0.0.1:8080/",
		"http://127.0.1.1:8080/",
		"http://[::1]:8080/",
		"http://0.0.0.0:8080/",
		"",
	}
	for _, raw := range onlyHere {
		s := healthyStatus()
		s.LocalURL = raw

		out := trayStatus(s, withPassword(), time.Now(), trayDict(t), update.State{})

		if out.HomeURL != "" {
			t.Errorf("%q ended up in the panel as %q: only this computer sees it, "+
				"and underneath there would be its QR code", raw, out.HomeURL)
		}
		// **The click stays**, and it is the half that must not follow the
		// other: with no network the monitor still works at home, and whoever is
		// in front of the machine has to be able to open it.
		if out.OpenURL == "" {
			t.Errorf("%q: with no address to hand out, the one to open here has "+
				"gone too", raw)
		}
	}

	// The other way round, otherwise the test is passed by always emptying the
	// field.
	for _, raw := range []string{
		"http://192.168.1.85:8080/",
		"http://10.0.0.7:8080/",
		"http://[fd00::1]:8080/",
	} {
		s := healthyStatus()
		s.LocalURL = raw
		if got := trayStatus(s, withPassword(), time.Now(), trayDict(t), update.State{}).HomeURL; got != raw {
			t.Errorf("%q is an address another device can reach, and it came out "+
				"as %q", raw, got)
		}
	}
}

// words is the dictionary the tray would use. It serves the one field that
// arrives already written — the step to take — because that one can carry
// Tailscale's sentence and cannot be reduced to a code.
func trayDict(t *testing.T) *i18n.Dictionary {
	t.Helper()
	return i18n.Open([]string{"it"})
}

// **The chosen camera that is gone reaches the tray**, because the remedy is
// physical: whoever watches from a phone can do nothing about it, whoever is in
// front of the machine is beside the socket the webcam goes into.
//
// It is a note and not a fault — there is a live picture — so the icon says
// "check this" and not "broken".
func TestTheChosenCameraGoneIsSaidInTheTray(t *testing.T) {
	s := healthyStatus()
	s.CameraFallback = true

	got := trayStatus(s, withPassword(), time.Now(), trayDict(t), update.State{})

	if got.Note != tray.NoteCameraOther {
		t.Errorf("note %q: the tray says nothing about a camera watching another room", got.Note)
	}
	if got.Fault != tray.FaultNone {
		t.Errorf("fault %q: a monitor with a live picture was declared broken", got.Fault)
	}
	if got.Phase != tray.PhaseCheck {
		t.Errorf("phase %v, expected PhaseCheck", got.Phase)
	}
}

// newer is the answer the check gives when a release exists.
func newer() update.State {
	return update.State{Code: update.Available, Version: "9.9.9", URL: "https://example.invalid/r"}
}

// **The command is there whatever else is wrong, and the sentence is not.**
//
// The two halves go to two different places and one field would have answered
// the wrong question. `summary` gives a Note precedence over a Fault in the
// tooltip, so a note set unconditionally would write "a newer version is
// available" over "the camera has stopped" — and the tooltip is brushed to find
// out whether one can go to bed, where a version number is not the answer.
//
// The panel's row is the opposite case, and the one that settles it: a monitor
// whose camera keeps stopping is exactly the one whose owner wants the release
// with the fix, so hiding the row behind the fault would take it away in the
// hour it is worth having.
func TestANewerVersionNeverCoversAFault(t *testing.T) {
	s := healthyStatus()
	s.Ready = false // no stream; past the grace below, that is capture-stopped

	out := trayStatus(s, withPassword(), time.Now().Add(time.Duration(-1)*time.Hour), trayDict(t), newer())

	if out.Fault != tray.FaultCaptureStopped {
		t.Fatalf("fault %q: this test is not exercising a fault at all", out.Fault)
	}
	if out.Note == tray.NoteUpdate {
		t.Error("the tooltip says there is a new version while the camera has stopped")
	}
	if out.UpdateVersion != "9.9.9" || out.UpdateURL == "" {
		t.Errorf("the panel's row is gone: %q %q", out.UpdateVersion, out.UpdateURL)
	}
}

// With nothing else to say, the tooltip says it.
func TestAHealthyMonitorSaysThereIsANewerVersion(t *testing.T) {
	out := trayStatus(healthyStatus(), withPassword(), time.Now(), trayDict(t), newer())

	if out.Note != tray.NoteUpdate {
		t.Errorf("note %q instead of %q", out.Note, tray.NoteUpdate)
	}
	if out.UpdateVersion != "9.9.9" {
		t.Errorf("version %q", out.UpdateVersion)
	}
	// **The colour does not change.** A new version is not a thing that is
	// wrong, and an amber icon would teach whoever walks past the machine to
	// read a working monitor as a broken one — for a version number.
	if out.Phase != tray.PhaseHome {
		t.Errorf("phase %q: a newer version must not move the colour", out.Phase)
	}
}

// And with no answer, nothing is said and no row appears.
//
// This is the ordinary case for a monitor with no network, and the one where
// saying anything at all would be a claim built on nothing.
func TestNoAnswerAddsNothing(t *testing.T) {
	out := trayStatus(healthyStatus(), withPassword(), time.Now(), trayDict(t), update.State{})

	if out.Note == tray.NoteUpdate {
		t.Error("the tooltip announces a version nobody found")
	}
	if out.UpdateVersion != "" || out.UpdateURL != "" {
		t.Errorf("a row appeared out of nothing: %q %q", out.UpdateVersion, out.UpdateURL)
	}
}
