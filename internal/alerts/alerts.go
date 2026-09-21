// Package alerts keeps the list of what the monitor has to say right now.
//
// **It exists because a fault is as silent as a quiet room.** The status page
// shows everything, but it gets looked at for half a minute now and then: if
// the capture stops at two in the morning, what is there in the morning is a
// frozen picture and no trace of when it stopped.
//
// **It is not a log of events, it is the set of the ones in progress.** The
// difference matters: a list of transitions grows, has to be scrolled and has
// to be forgotten, while whoever is watching has one question, "is anything
// wrong right now?". Whoever consumes this list works out for themselves what
// appeared and what recovered, by comparing it with the previous one: the
// **identifiers are stable** for as long as the alert lasts, so a new id is a
// new alert and a vanished id is a recovery. Those two facts are enough, and
// nothing has to be kept anywhere.
//
// **Codes, not sentences.** Whoever has to decide something compares a code,
// and the words are chosen by whoever draws the page, who is also the only one
// who knows what language they are speaking.
package alerts

import (
	"sort"
	"sync"
	"time"
)

// Code identifies what is wrong. The values travel in JSON and the page
// compares them: they get added, not renamed.
type Code string

const (
	// CaptureStopped: no frame for half a minute. The camera opens in a few
	// seconds, so this is no longer slowness.
	CaptureStopped Code = "capture-stopped"
	// MicSilent: the microphone is delivering zeros. The worst fault of the
	// product, because from the outside it looks like a quiet room.
	MicSilent Code = "mic-silent"
	// MicMissing: there is no microphone at all. It happens on laptops with the
	// lid closed.
	MicMissing Code = "mic-missing"
	// CameraDenied and MicDenied: Windows is refusing the device because the
	// permission is off.
	//
	// **They are a state of the machine and not a failure of the monitor**, and
	// without them both fell into CaptureStopped and MicMissing — that is, into
	// *no images from the camera*, which is true, says nothing about why, and
	// sends whoever reads it to look at the cable. Camera and microphone are
	// consented to on Windows 11 and the consent can be withdrawn at any
	// moment: from Settings, by whoever administers the machine, or by an
	// update putting *"let desktop apps access your camera"* back to off. The
	// remedy is two clicks, and the whole value of these two codes is that
	// something can name it.
	//
	// They are two and not one because the two permissions are two switches: a
	// machine can refuse the microphone and grant the camera, which is a
	// monitor showing a room it cannot hear.
	//
	// **They are Faults and not Notices**, unlike CameraOther: there is no
	// picture at all, or no sound at all. The boundary is the level's own
	// definition — the monitor is not doing its job — and that it is somebody's
	// decision rather than a breakage does not change what the room gets.
	CameraDenied Code = "camera-denied"
	MicDenied    Code = "mic-denied"
	// CameraOther: the chosen camera is not connected, so another one is being
	// watched.
	//
	// **The picture is fine, and that is the danger.** The chosen camera watched
	// the cot and the one that is there may be looking at the kitchen: at three
	// in the morning, on a phone, a dark room looks like a dark room. It is the
	// one thing the viewer cannot work out by looking.
	//
	// It exists because opening another camera is the right answer — see
	// devices.Pick — and an answer taken in silence would be worse than the
	// refusal it replaced.
	CameraOther Code = "camera-other"
	// RemoteDown: access from outside the house has stopped working. Whoever is
	// out would only find out by trying to open it.
	RemoteDown Code = "remote-down"
	// Motion: something is moving in the room. **It is not a fault**: it is the
	// thing the monitor exists for.
	Motion Code = "motion"
	// Cry and Bark: the two sounds this product exists to hear. What decides is
	// the recogniser in internal/ced, downstream of the gate in internal/detect
	// — so these say a baby is crying and a dog is barking, not that something
	// shaped like one might be.
	Cry  Code = "cry"
	Bark Code = "bark"
)

// Level is how serious it is. There are two and not five: they pick a tint and
// a sound, and a finer scale is one nobody would know how to use.
type Level string

const (
	// Fault: the monitor is not doing its job.
	Fault Level = "fault"
	// Notice: it works, but there is something to know.
	Notice Level = "notice"
	// Event: something happened in the room. It is not a lesser severity, it is
	// a different thing — it deserves its own tint and its own sound, not the
	// brick red of a fault. In the ordering it sits beside the notices: a fault
	// still comes first, because a broken monitor is watching nothing.
	Event Level = "event"
)

// levels maps each code to its severity. It lives here and nowhere else: two
// lists of the same thing always diverge.
var levels = map[Code]Level{
	CaptureStopped: Fault,
	MicSilent:      Fault,
	MicMissing:     Fault,
	CameraDenied:   Fault,
	MicDenied:      Fault,
	// **A Notice and not a Fault**, and the boundary is the level's own
	// definition: the monitor is doing its job — there is a live picture — and
	// there is something to know about it. A Fault would also turn the tray icon
	// brick, that is, teach whoever walks past the machine to read a working
	// monitor as a broken one. It sits beside RemoteDown, which is the same
	// shape: half of what was asked for is missing and the rest works.
	CameraOther: Notice,
	RemoteDown:  Notice,
	Motion:      Event,
	Cry:         Event,
	Bark:        Event,
}

// AllCodes lists every code, in a stable order.
//
// It is derived from levels, which is already the complete list and declares
// itself the only one: deriving instead of rewriting is what keeps that promise
// true. It is for whoever has to check that **every** code is covered — today
// the translations, which need a word for each one in every language.
func AllCodes() []Code {
	out := make([]Code, 0, len(levels))
	for c := range levels {
		out = append(out, c)
	}
	sort.Slice(out, func(i, j int) bool { return out[i] < out[j] })
	return out
}

// LevelOf says how serious a code is. An unknown one counts as a fault: better
// one alert too many than a new code slipping by unnoticed because somebody
// forgot to list it.
func LevelOf(c Code) Level {
	if l, ok := levels[c]; ok {
		return l
	}
	return Fault
}

// Alert is one thing that is wrong, in progress now.
//
// The JSON names are English like the rest of the API, and they hold no word
// meant to be shown: the page composes the sentence from the code.
type Alert struct {
	// ID stays the same for as long as the alert lasts. That is how the viewer
	// tells "still the one from before" from "another one has arrived" without
	// us having to keep any history.
	ID int64 `json:"id"`
	// Code says what is wrong.
	Code Code `json:"code"`
	// Level is the severity, resolved here so the page does not have to keep a
	// second table.
	Level Level `json:"level"`
	// Since is when it appeared, in Unix milliseconds. It is what makes "for
	// twenty minutes" sayable, which is the information whoever comes home is
	// missing.
	Since int64 `json:"since"`
}

// Registry keeps the alerts in progress and hands out the identifiers.
//
// It is queried by the loop that builds the status, once a second from one
// goroutine, and updated from another: the lock is not stylistic caution.
type Registry struct {
	mu     sync.Mutex
	next   int64
	active map[Code]*entry
}

type entry struct {
	id      int64
	since   time.Time
	expires time.Time // zero: does not expire by itself
}

func NewRegistry() *Registry {
	return &Registry{active: map[Code]*entry{}}
}

// Update declares **the complete set** of faults in progress right now.
//
// It takes the whole set instead of an on/off pair because the caller
// recomputes everything from scratch on every turn: handing it the snapshot
// makes impossible the class of fault where an alert stays lit because somebody
// forgot to switch it off along one branch.
//
// Alerts with an expiry — the ones added with Add — are left alone: they do not
// come from an observed condition, so no observation can contradict them.
//
// **It returns what changed**, and that is for the log on file: the page redoes
// the comparison on its own — it has to, because whoever reloads remembers
// nothing — while the log needs the line at the instant the fault appears. It
// is the only witness of what happened at three in the morning, when nobody was
// looking at the page.
func (r *Registry) Update(now time.Time, present []Code) (appeared []Alert, recovered []Code) {
	r.mu.Lock()
	defer r.mu.Unlock()

	seen := make(map[Code]bool, len(present))
	for _, c := range present {
		seen[c] = true
		if _, already := r.active[c]; already {
			continue // same fault as before: the id does not change
		}
		r.next++
		r.active[c] = &entry{id: r.next, since: now}
		appeared = append(appeared, Alert{ID: r.next, Code: c, Level: LevelOf(c), Since: now.UnixMilli()})
	}
	for c, v := range r.active {
		if v.expires.IsZero() && !seen[c] {
			delete(r.active, c)
			recovered = append(recovered, c)
		}
	}
	r.expire(now)
	return appeared, recovered
}

// Active is the list as it stands, most serious first and then most recent.
//
// The order is part of the contract: the page has room for one banner, and **a
// colour can say one thing only, so it has to say the worst one.** It is the
// same rule that governs the icon in the notification area.
func (r *Registry) Active(now time.Time) []Alert {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.expire(now)

	out := make([]Alert, 0, len(r.active))
	for c, v := range r.active {
		out = append(out, Alert{ID: v.id, Code: c, Level: LevelOf(c), Since: v.since.UnixMilli()})
	}
	sort.Slice(out, func(i, j int) bool {
		if (out[i].Level == Fault) != (out[j].Level == Fault) {
			return out[i].Level == Fault
		}
		return out[i].ID > out[j].ID
	})
	return out
}

// expire drops the timed alerts that have run out. It is called with the lock
// already held.
func (r *Registry) expire(now time.Time) {
	for c, v := range r.active {
		if !v.expires.IsZero() && now.After(v.expires) {
			delete(r.active, c)
		}
	}
}
