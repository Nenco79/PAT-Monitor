//go:build windows

package tray

import (
	"strconv"
	"strings"
)

// The tray's words, and the codes that choose them.
//
// **This package's language is not the pages'.** The pages are read by whoever
// is watching, from a phone that may be in another house; the tray is read by
// whoever is in front of the machine. They are two audiences, and the
// declaration that speaks for the second is the language of the Windows
// interface — not `Accept-Language`, which says nothing about this computer.
//
// Whoever composes the state sends **codes and numbers**; the words are chosen
// here. The direction is the usual one: whoever decides compares a code, whoever
// shows picks the sentence, and only the second knows what language it is
// speaking.

// Fault is what is wrong, when something is wrong.
type Fault string

const (
	FaultNone           Fault = ""
	FaultNoPassword     Fault = "no-password"
	FaultCaptureStopped Fault = "capture-stopped"
	FaultMicMissing     Fault = "mic-missing"
	FaultMicSilent      Fault = "mic-silent"
	// FaultMicMuted: the microphone opens and Windows is muting it.
	//
	// **It is here for the reason the two refusals below are**: the remedy is
	// on this machine and nowhere else. Whoever is watching from a phone can be
	// told the room is muted and can do nothing at all; whoever walks past the
	// computer is one click from the slider that did it — see settingsPage.
	FaultMicMuted        Fault = "mic-muted"
	FaultMicFiltered     Fault = "mic-filtered"
	FaultRemoteNoIngress Fault = "remote-no-ingress"
	// FaultCameraDenied and FaultMicDenied: Windows is refusing the device
	// because the permission is off.
	//
	// **They are in the tray before they are anywhere else, because this is the
	// only place the remedy exists.** Whoever watches from a phone can be told
	// the camera permission is off and can do nothing whatever about it; the
	// switch is in the Settings of this machine, and whoever is in front of it
	// is one click away — see settingsPage, which is what turns this code
	// into that click. It is the argument NoteCameraOther and NoteUpdate
	// already carry.
	FaultCameraDenied Fault = "camera-denied"
	FaultMicDenied    Fault = "mic-denied"
)

// settingsPage is the Windows page where what is wrong can be put right.
//
// **It was called privacySetting while the only two answers were privacy
// switches**, and a mute is not a permission: the name would have gone on
// describing two thirds of the function, which is the shape this repository
// keeps a chapter about. The button's word never said "permission" — it says
// *open Windows settings* — so only the key and the name had to follow.
//
// **The address belongs here and not to whoever composes the state**: it is a
// Windows shell address, this package is the monitor's Windows presence, and
// `cmd/pat-monitor` already hands over a code — which is the direction
// everything else in this file goes.
//
// **The word is one and it is not here**, and that is a correction: the command
// used to carry the device's name, which put *Microphone permission* on a
// button sitting directly under a line that already says *microphone: permission
// is off*. The line names the device and the button says what pressing does —
// the same division the recordings page's lock already makes — so there is one
// entry, `tray.menu.settings`, and it is shorter, which the panel notices.
//
// An empty answer means there is nothing to grant, and then there is no command:
// a panel entry that does nothing is worse than a shorter panel.
func settingsPage(f Fault) string {
	switch f {
	case FaultCameraDenied:
		return "ms-settings:privacy-webcam"
	case FaultMicDenied:
		return "ms-settings:privacy-microphone"
	case FaultMicMuted:
		// **The Sound page and not the default microphone's properties.**
		// `ms-settings:sound-defaultinputproperties` lands on the very slider,
		// which is better every time the muted endpoint is the default one and
		// wrong the rest of the time — the chosen microphone can be another
		// device, and a page that opens the wrong device's volume is worse than
		// one that opens a list: it shows a slider that is not down.
		//
		// There is an address that is always right,
		// `ms-settings:sound-properties?endpointId=`, and it wants the ID of
		// the endpoint that is capturing — which this package does not have and
		// `cmd/pat-monitor` does. It is a field's worth of work and it is not
		// done until somebody has measured that the URI takes the ID in the
		// shape WASAPI hands it over.
		return "ms-settings:sound"
	}
	return ""
}

// todoCommands is the label for the step Tailscale is waiting on, per action
// code, and the empty string where the step is not something to press.
//
// **The sentence and the label are two different things, and one used to do
// both.** The button carried the explanation — ours, or Tailscale's when their
// control server answers, which it composes knowing the tailnet and the
// reader's role — so what appeared on it was a paragraph cut mid-word:
// `Da fare: approve this machin…`, in another language, on the one command the
// reader has to press. The ellipsis was there and it did not help; a cut
// sentence does not read as a short one.
//
// So the explanation goes to the status lines, which wrap into as many rows as
// they need, and the button says what pressing does, in the reader's language
// and short enough to fit. It is the division the recordings page's lock and
// the permission's own notice already make.
//
// **Three of the six actions never had a URL**, so they never produced a button
// and produce none now: `wait-certificate` says in as many words that there is
// nothing to do, `other-user` and `failed` describe a state nobody can press
// their way out of. An empty label is how that is said, and it is the shape
// `settingsPage` already uses.
var todoCommands = map[string]string{
	"authorise":        "tray.menu.todo.authorise",
	"approve":          "tray.menu.todo.approve",
	"enable-funnel":    "tray.menu.todo.enable-funnel",
	"wait-certificate": "",
	"other-user":       "",
	"failed":           "",
}

// todoCommand is the label key for an action, or "" when there is nothing to
// press. An action nobody listed answers "" as well, and the guard in
// words_windows_test.go is what stops that being a silent hole: it walks
// tunnel.AllActions and fails on one this map has never met.
func todoCommand(action string) string { return todoCommands[action] }

// Note is a condition worth saying that is not a fault.
type Note string

const (
	NoteNone       Note = ""
	NoteNoPassword Note = "no-password"
	NoteStarting   Note = "starting"
	NoteRemoteDown Note = "remote-down"
	// NoteCameraOther: the chosen camera is not connected, so another one is
	// being watched.
	//
	// **It is here and not only in the page because the remedy is physical.**
	// Whoever is watching from a phone can be told there is another room on
	// screen and can do nothing about it; whoever is in front of the machine is
	// standing next to the socket the webcam goes into.
	NoteCameraOther Note = "camera-other"
	// NoteVerifying: remote access is open and it is still being proved that
	// one can actually get in from outside.
	//
	// **It also drives the icon's pulse**, and it is meant to be the same
	// value: the sentence under the pointer and the breathing of the colour say
	// the same thing, so they cannot say two different ones. A second "pulsing"
	// field would be the usual pair that diverges at the first touch.
	NoteVerifying Note = "verifying"
	// NoteUpdate: a newer release exists.
	//
	// **It is a Note and not a Fault**, and the boundary is the level's own
	// definition: nothing is wrong, the monitor is watching the room, and there
	// is something worth knowing about it. A Fault would also turn the icon
	// brick, that is, teach whoever walks past the machine to read a working
	// monitor as a broken one — for a version number.
	//
	// **And it is in the tray because that is where the remedy is.** Whoever
	// watches from a phone can be told a newer version exists and can do
	// nothing about it; whoever is in front of the machine is the one who can
	// download it. It is the argument NoteCameraOther already carries, and the
	// rule that keeps the password reset and the session revocation off the
	// HTTP routes.
	NoteUpdate Note = "update"
)

// AllFaults and AllNotes are the authoritative lists.
//
// They serve the test that every code has a word in every catalogue: without
// it, a code added here and forgotten over there would appear in the menu **as
// a code**, and no compiler would say so. It is the same shape as the alert
// codes and the tunnel phases.
func AllFaults() []Fault {
	return []Fault{
		FaultNoPassword, FaultCaptureStopped, FaultMicMissing,
		FaultMicSilent, FaultMicMuted, FaultMicFiltered, FaultRemoteNoIngress,
		FaultCameraDenied, FaultMicDenied,
	}
}

func AllNotes() []Note {
	return []Note{
		NoteNoPassword, NoteStarting, NoteRemoteDown, NoteVerifying,
		NoteCameraOther, NoteUpdate,
	}
}

// t is a key's sentence, with the placeholders filled in.
//
// The placeholders are `{name}` as in the pages' catalogues: the same shape on
// both sides, so that whoever translates does not have to remember which half
// they are looking at.
func (t *Tray) t(key string, pairs ...string) string {
	s := t.dictionary.T(key)
	if len(pairs) == 0 {
		return s
	}
	replacements := make([]string, 0, len(pairs))
	for i := 0; i+1 < len(pairs); i += 2 {
		replacements = append(replacements, "{"+pairs[i]+"}", pairs[i+1])
	}
	return strings.NewReplacer(replacements...).Replace(s)
}

// verdict is the line the onboarding illustration shows in bold: how the
// monitor is, in two words.
func (t *Tray) verdict(p Phase) string {
	switch p {
	case PhaseStarting:
		return t.t("tray.verdict.starting")
	case PhaseCheck:
		return t.t("tray.verdict.attention")
	case PhaseFault:
		return t.t("tray.verdict.fault")
	default:
		return t.t("tray.verdict.ok")
	}
}

// lines are the menu's disabled entries: the state, not commands.
//
// **A fault goes at the top**, because it is the reason the menu was opened.
// With no password the counters are not shown at all: before configuration
// "nobody is watching" is true and is not news, and it would take the place of
// the one line that matters.
func (t *Tray) lines(st Status) []string {
	if st.Fault == FaultNoPassword {
		return []string{t.t("tray.fault." + string(st.Fault))}
	}

	// Two lines, not five. The video size, the cadence and the microphone's
	// name change every second and nobody opens the menu to read them: the
	// status page shows them all, and there there is room for what they mean
	// beside them. What stays here is what a two-second glance can use — who is
	// watching, and how long it has been running.
	lines := []string{
		t.t("tray.line.watching",
			"viewers", strconv.FormatInt(st.Viewers, 10),
			"devices", strconv.FormatInt(st.Devices, 10)),
		t.t("tray.line.uptime", "since", st.Uptime),
	}
	// **The step Tailscale is waiting on is a line, not a label.** It is the
	// answer to the question the panel was opened with, it can be a paragraph,
	// and these rows wrap into as many as `maxStatusRows` allows — while a
	// button has one line and cuts. See todoCommands.
	if st.Todo != "" {
		lines = append([]string{st.Todo}, lines...)
	}
	if st.Fault != FaultNone {
		return append([]string{t.t("tray.fault." + string(st.Fault))}, lines...)
	}
	return lines
}

// summary is **the** line of the tooltip, the one that appears on hovering
// without clicking anything.
//
// The menu and the tooltip answer two different questions: the first is opened
// in order to do something, and there the numbers are wanted; the second is
// brushed to find out whether one can go to bed, and there a sentence is wanted.
func (t *Tray) summary(st Status) string {
	switch {
	case st.Note != NoteNone:
		return t.t("tray.note." + string(st.Note))
	case st.Fault != FaultNone:
		return t.t("tray.fault." + string(st.Fault))
	}

	// **When there is nothing wrong, it says where it can be seen and who is
	// watching**, which is the line drawn in the tray illustration at the end
	// of the configuration path. The drawing promised "at home and from
	// outside · 1 viewer" and the product wrote "viewers: 0 · devices
	// connected: 1": the same family as the fake QR code, a drawing showing a
	// thing that does not exist.
	where := t.t("tray.where.home")
	if st.PublicURL != "" {
		where = t.t("tray.where.home-and-away")
	}
	return where + " · " + t.viewersText(st.Viewers)
}

// viewersText says who is watching now.
//
// **Zero is not written as a number**: "0 viewers" is a digit that reads like a
// broken counter, and the question the line answers — "is anybody in front of
// the monitor?" — has an answer in words.
//
// The three forms are separate keys and not a computed plural: Italian and
// English would make do with two, but **the "nobody" form is not a plural**, it
// is another sentence. If one day a language with a dual arrives, the place to
// add it is here.
func (t *Tray) viewersText(n int64) string {
	switch n {
	case 0:
		return t.t("tray.viewers.none")
	case 1:
		return t.t("tray.viewers.one")
	default:
		return t.t("tray.viewers.other", "n", strconv.FormatInt(n, 10))
	}
}

// sessionsText says how many have been closed, by the same rule.
func (t *Tray) sessionsText(n int) string {
	switch n {
	case 0:
		return t.t("tray.sessions.none")
	case 1:
		return t.t("tray.sessions.one")
	default:
		return t.t("tray.sessions.other", "n", strconv.Itoa(n))
	}
}
