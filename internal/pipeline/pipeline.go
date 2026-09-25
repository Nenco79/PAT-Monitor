// Package pipeline orchestrates the capture: video and H.264 encoding with
// Media Foundation, audio with a WASAPI capture in raw mode and an Opus
// encoder, all inside this process.
//
// It all happens inside this process, and the reason is not elegance but two
// commands given to the encoder **while it works**: changing the bitrate when
// the bandwidth drops, and producing a keyframe when the browser reports lost
// packets. Those two are what the engine is shaped this way for.
//
// The audio does not come from the camera because on many OEM machines a
// noise-suppression Audio Processing Object zeroes the signal before
// applications see it (see internal/audio). So it is captured raw.
package pipeline

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"math"
	"runtime"
	"sync"
	"sync/atomic"
	"time"

	"patmonitor/internal/audio"
	"patmonitor/internal/audiocodec"
	"patmonitor/internal/devices"
	"patmonitor/internal/diag"
	"patmonitor/internal/guard"
	"patmonitor/internal/media"
	"patmonitor/internal/mf"
	"patmonitor/internal/resample"
	"patmonitor/internal/wincom"
)

// Parameters of the analysis streams.
const (
	// MotionWidth and MotionHeight are the reduced resolution used for motion
	// detection: more than enough and almost free, because the luma plane of an
	// NV12 frame is already the greyscale image and only needs subsampling.
	MotionWidth  = 160
	MotionHeight = 90
	MotionFPS    = 5

	// AnalysisSampleRate is 16 kHz because that is the rate the sound-event
	// classifiers of the YAMNet family want, and they are grafted onto this same
	// stream. It feeds the energy detector just as well.
	AnalysisSampleRate = 16000

	motionFrameSize = MotionWidth * MotionHeight // 1 byte per pixel, gray

	// levelBlockDuration is the length of the block handed to the detector: 100
	// ms, a granularity that suits both the energy thresholds and a classifier's
	// windows. In samples it depends on the analysis rate, which in turn depends
	// on the microphone: see AnalysisRate.
	levelBlockDuration = 100 * time.Millisecond
)

// DefaultAudioBitrateKbps is the Opus bitrate: more than the minimum for speech,
// because what matters here is the sound of the room and not only the talking.
//
// The congestion control reads it too, to know how much bandwidth is not
// available to the video: it is the only definition, so the two cannot diverge.
const DefaultAudioBitrateKbps = 64

// Config describes what to capture and how to encode it.
type Config struct {
	// PanicIn raises a deliberate panic inside the named capture — "video" or
	// "audio" — once per process, a few seconds into a session.
	//
	// **A fault does not happen on command**, which is the same reason
	// -simulate-fault exists: without it, the claim that a panic in the capture
	// costs a restart and not the night could only be checked on the night it
	// mattered. It fires deep in the session, with the camera open and the
	// encoder built, because what has to be seen is the unwinding running the
	// defers that release those — not a clean return from the top of a
	// function.
	PanicIn string

	// CameraLink is the symbolic link of the **chosen** camera; empty means the
	// first usable one.
	//
	// It is a wish and not a fact: the camera it names may be unplugged, and
	// then the first usable one is opened and that is declared. Whoever wants
	// to know what is really being captured has Camera().
	CameraLink string

	// KeepRequestedSize asks the camera for Width x Height at FPS **without
	// lowering them to what it declares**.
	//
	// It is for measuring instruments, and it is false by omission on purpose:
	// see cameraSize. pat-capture sets it, because those numbers are the
	// operator's and it declares the upscale in a line of its own; the monitor
	// must not, because an upscale is silent and has already cost a machine
	// every frame for twenty minutes.
	KeepRequestedSize bool

	Width, Height, FPS int
	BitrateKbps        int
	// GOPSeconds is the distance between keyframes. Zero lets the encoder
	// choose.
	GOPSeconds int
	// PreferEncoder narrows the choice to encoders whose name contains it.
	PreferEncoder string
	// PinNativeFormat fixes the camera's own format at open, so that a change
	// of output size has to be served by a converter instead of by
	// renegotiating the device. It is an experiment and it is off by default:
	// see SourceReader.PinNativeFormat for what it is trying to settle.
	PinNativeFormat bool
	// KeepFrameRateConversion leaves the video processor free to invent frames,
	// which is what Media Foundation does by default and what this program did
	// until it was measured. **It exists to be able to measure it again**, and
	// nothing in the monitor sets it: see the call site for the numbers.
	KeepFrameRateConversion bool
	// RateControl is the criterion by which the encoder spends its bits; empty
	// means CBR. Quality is the level to hold when constant quality is chosen,
	// MinQP the quantiser floor when capped CRF is chosen: they are two
	// different scales and each one belongs to its own mode.
	RateControl mf.RateControl
	Quality     int
	MinQP       int
	// MaxQP is how far the encoder may degrade in order to stay inside the
	// bitrate. Zero leaves it free, and free means it may also refuse to degrade
	// and overshoot the cap — measured.
	MaxQP int
	// NoDirect3D gives up the Direct3D device. It serves diagnosis and the
	// machines where creating one fails; hardware encoders do not work without.
	NoDirect3D bool

	// MicDeviceID is the WASAPI endpoint ID; empty means the default.
	//
	// It is **where capture starts from**, not where it captures: a microphone
	// is also chosen while the monitor runs, and from then on SetMicDevice is in
	// command. Reading this again later would mean reopening the microphone the
	// user has just discarded.
	MicDeviceID string
	// MicGainDB applies a gain to the captured audio. It serves very quiet
	// microphones; 0 leaves the signal untouched.
	MicGainDB float64

	// AudioTestTone replaces the microphone with a generated tone. It serves
	// where there is no microphone — a laptop with the lid closed — and to
	// separate the capture from the rest of the chain when nothing can be heard.
	// Whoever switches it on has to tell whoever is watching: a tone is not the
	// room.
	AudioTestTone bool

	AudioBitrateKbps int

	// Trace, when set, times the steps that cannot be observed from outside. It
	// serves diagnostics only: in service it stays nil.
	Trace *Trace

	Log *slog.Logger
}

// Trace measures the cadence of the steps inside the capture.
//
// They have to be measured with the pipeline under load: on their own they are
// regular, but here they compete with the video capture and with the encoder.
type Trace struct {
	// MicCallback is the cadence at which WASAPI hands us the audio.
	MicCallback *diag.Delivery
	// AudioEncode is the cadence at which an Opus packet comes out of it.
	AudioEncode *diag.Delivery
}

func (c *Config) applyDefaults() {
	if c.AudioBitrateKbps == 0 {
		c.AudioBitrateKbps = DefaultAudioBitrateKbps
	}
	if c.Width == 0 {
		c.Width = 1280
	}
	if c.Height == 0 {
		c.Height = 720
	}
	if c.FPS == 0 {
		c.FPS = 30
	}
	if c.BitrateKbps == 0 {
		c.BitrateKbps = 2500
	}
	if c.Log == nil {
		c.Log = slog.Default()
	}
}

// Sinks gathers the consumers of the four streams. Every callback is invoked
// from its producer's goroutine and must not block for long: anyone who needs
// slow processing has to queue internally.
type Sinks struct {
	// Video receives an Access Unit (one complete encoded frame).
	Video func(media.AccessUnit)
	// Audio receives an Opus packet ready to send.
	Audio func(packet []byte)
	// Motion receives a gray frame of MotionWidth x MotionHeight, **with the
	// size it was derived from**: the analysis frame is always the same size, so
	// on its own it never says the source has changed, and a change of source
	// changes every cell. Whoever consumes it has to be able to know that
	// without asking anyone else — a rule somebody has to remember breaks at the
	// first new caller.
	Motion func(frame []byte, srcW, srcH int)
	// Level receives s16le mono PCM at AnalysisSampleRate.
	Level func(pcm []byte)
}

// Stats exposes counters for diagnostics and for the status page.
type Stats struct {
	Restarts     atomic.Int64
	VideoFrames  atomic.Int64
	Keyframes    atomic.Int64
	AudioPackets atomic.Int64
	MotionFrames atomic.Int64
	AudioBytesIn atomic.Int64
	AudioDropped atomic.Int64
	// VideoBytes are the video bytes produced by the encoder. Cumulative:
	// whoever wants a throughput subtracts two readings, and so contends with
	// nobody over a window — the quantiser take has a single consumer precisely
	// because it empties.
	VideoBytes atomic.Int64
	// VideoDropped are the frames read from the camera and not handed to the
	// encoder because the cadence was lowered. It is not a loss: it is the scale
	// at work, and it is the only number that separates "the picture updates
	// rarely because we decided so" from "the camera has wedged" — two things
	// that look identical from the status page.
	VideoDropped atomic.Int64
	// LastVideoUnix and LastAudioUnix make it possible to notice that one stream
	// has stopped while everything else stays alive.
	//
	// **That sentence was true and nobody was reading it.** Both were written
	// on every frame and every packet from the day they were added, and no
	// consumer in the tree loaded either: what the status page, the
	// notification area and the alerts all watched instead was `Ready`, a latch
	// that closes on the first keyframe. A monitor whose picture stops after an
	// hour therefore goes on declaring itself ready, for minutes together, with
	// the evidence sitting unread. `LastVideoUnix` now reaches
	// `server.Status.LastFrameUnix` and is what `captureStopped` decides on.
	LastVideoUnix atomic.Int64
	LastAudioUnix atomic.Int64
}

// Pipeline manages the life of the capture, restarting it on error.
type Pipeline struct {
	// panicSimulated keeps the deliberate panic to one per process: see
	// simulatePanic.
	panicSimulated atomic.Bool

	cfg   Config
	Stats Stats
	// rawMode records whether the last microphone open obtained raw mode.
	rawMode atomic.Bool
	// micMuted records whether the endpoint the last open found was muted in
	// Windows. See MicrophoneMuted.
	micMuted atomic.Bool
	// audioOK says whether there is a microphone capturing right now. It serves
	// the status page: a monitor without audio has to say so, not leave it to be
	// inferred from the silence.
	audioOK atomic.Bool

	// camDenied and micDenied say Windows refused the device on the last
	// attempt, which is a different thing from the device not working.
	//
	// **They exist because a withdrawn permission is a normal state and not a
	// fault**, and nothing in this file could tell the two apart: a camera the
	// user has revoked and a camera that will not open both arrived as "the
	// capture is not starting", retried every thirty seconds for ever with the
	// log naming an HRESULT nobody reads. Camera and microphone are consented
	// to on Windows 11, the consent can be taken away while the monitor runs —
	// from Settings, by whoever administers the machine, or by an update
	// putting *"let desktop apps access your camera"* back to off — and the
	// remedy is two clicks on a page this program can open.
	//
	// They are two flags and not one because the two permissions are separate
	// switches: a machine can refuse the microphone and grant the camera, which
	// is a monitor that shows a room it cannot hear, and saying "permission
	// denied" without saying which would leave whoever reads it to go and look.
	//
	// **They follow the evidence and hold no history.** Each is set from the
	// error the last attempt ended with and cleared the moment the device
	// opens, so the state cannot outlive the refusal — the way a latch would,
	// leaving an alert lit all night over a permission granted back in a
	// second.
	camDenied, micDenied atomic.Bool

	// camOpening and micOpening say a call into the device is in flight right
	// now and has not come back.
	//
	// **They exist because "not yet" and "not there" had one name**, and
	// packaged they are not the same length. Windows asks for the camera and
	// the microphone one consent per package, at the first use, and the open
	// call waits for as long as the person takes. Unpackaged it answers in
	// about half a second, inside the start-up grace, so the window is
	// invisible; packaged it outlasts the grace, and what went out was
	// `capture-stopped` and `mic-missing` — the second of which asserts there
	// is no microphone while the person is being asked whether this program may
	// use it.
	//
	// **They are not the denied flags with another name.** A refusal comes back
	// as `E_ACCESSDENIED`, under a package too, and the pair above names it
	// correctly. This is the state before any answer exists, and the
	// distinction is the one this program already makes in `internal/update`:
	// "I could not ask" must never be rendered as an answer.
	//
	// They hold no history and are not a grace: set before the call, cleared
	// the moment it returns or the device opens. Nothing here invents a
	// deadline for somebody reading a dialogue — while the answer is pending
	// the monitor says it is starting, which is true, and Windows' own window
	// is on the screen saying why.
	camOpening, micOpening atomic.Bool

	// camOpenedAt is when the camera last came open, in Unix seconds, zero
	// before the first time.
	//
	// **It exists because the start-up grace was anchored to the wrong
	// instant.** Thirty seconds from the process starting is right when the
	// camera opens half a second in; it is wrong the moment the open waits for
	// a person, because by the time the device answers the grace is long gone
	// and the second the first keyframe takes to arrive is announced as a
	// capture that stopped.
	//
	// The remedy is not a second grace with a number in it: it is the same
	// thirty seconds counted from when trying actually began.
	camOpenedAt atomic.Int64

	// camRetrying says the last attempt failed without the camera coming open,
	// so the open's own warnings on the next one are a repeat and go to Debug.
	// Set and cleared by Run. See retryLevel.
	camRetrying atomic.Bool

	// micGraceUntil is until when a closed microphone must **not** be reported
	// as absent: the window of a reopen we decided on. See micRecheckGrace.
	micGraceUntil atomic.Int64
	// micMode is the road the microphone is open by ("raw", "exclusive",
	// "shared"). It serves to avoid repeating in the log an open identical to
	// the previous one, now that the path gets re-examined.
	micMode atomic.Value
	// micWanted is the ID of the endpoint to open: empty means the Windows
	// default.
	//
	// **It is not read from the configuration on every open**, and that is not a
	// detail: the configuration is read once at start-up, while this changes
	// while the capture runs. Whoever changes it goes through SetMicDevice,
	// which is also the only way to make the microphone reopen.
	micWanted atomic.Value
	// micOpen is the endpoint capturing right now, with the name Windows gives
	// it.
	//
	// **It is not micWanted**, and the difference shows in the case that
	// matters: the chosen microphone may have been unplugged, and then whatever
	// is there gets opened. What is shown is this one — the same rule as the
	// encoder's, where the governor is not the authority on what is coming out.
	micOpen atomic.Pointer[MicDevice]
	// micCancel closes the audio capture in progress. Choosing another
	// microphone is applied by reopening, which is the road the periodic path
	// recheck already walks: no second way of opening a microphone.
	micCancel atomic.Value
	// micWake wakes the audio supervisor while it sleeps in its backoff.
	//
	// **Closing the capture is not enough to make it reopen.** If the open is
	// failing — no active endpoint, a driver that refuses — the supervisor waits
	// up to thirty seconds between one attempt and the next, and there is no
	// capture there to cancel: the choice would sit written and without effect
	// for half a minute, while the page has already taken it as done.
	micWake chan struct{}
	// micReopen says the close was our own decision, and micChosen that behind
	// it there is a choice by whoever is watching.
	//
	// They are two flags and not one because they answer two different
	// questions: the first the supervisor's, which has to reopen at once and in
	// silence instead of announcing a fault; the second the open's, which on the
	// contrary has to **announce** — a microphone chosen just now is news, and if
	// it is filtered that has to be said straight away instead of ending up
	// inside the grace that silences the recheck.
	micReopen, micChosen atomic.Bool
	// camOpen is the camera capturing right now, with the name Windows gives it.
	//
	// **It is not the chosen one**, and that difference is the whole reason it
	// exists: the chosen camera may have been unplugged, and then the first
	// usable one is opened in its place. What gets shown is this one — the same
	// rule as the microphone's, and as the encoder's, where the governor is not
	// the authority on what is going out.
	camOpen atomic.Pointer[Camera]
	// camFellBack says the camera that is open is not the one that was chosen.
	//
	// **It is a fact and not a comparison to be redone**, and that is why it is
	// published instead of leaving whoever shows it to compare two links: the
	// chosen one comes out of a file and the open one out of the enumeration,
	// and Windows gives the same symbolic link back in different cases
	// depending on who is asked — so the obvious comparison says "another
	// camera" about the right one. It is decided once, where the two are
	// matched.
	camFellBack atomic.Bool
	// camWanted is the link of the camera to open: empty means the first usable
	// one.
	//
	// **It is not read from the configuration at every open**, for the same
	// reason as micWanted: the configuration is read once at start-up, this
	// changes while the capture runs. Whoever changes it goes through
	// SetCamera, which is also the only way to make the camera reopen.
	camWanted atomic.Value
	// camCancel closes the video capture in progress. Choosing another camera is
	// applied by **reopening**, which is the road the restart loop already
	// walks: no second way of opening a camera.
	camCancel atomic.Value
	// camWake wakes the video supervisor while it sleeps in its backoff.
	//
	// **Closing the capture is not enough to make it reopen.** If the open is
	// failing — no camera at all, one held by another process — the supervisor
	// waits up to thirty seconds, and there is no capture there to cancel: the
	// choice would sit written and without effect for half a minute, while the
	// page has already taken it as done.
	camWake chan struct{}
	// camReopen says the close was our own decision, and camChosen that behind
	// it there is a choice by whoever is watching.
	//
	// They are two flags and not one because they answer two questions: the
	// first the supervisor's, which has to reopen at once and without announcing
	// a fault; the second the open's, which has to **announce** — a camera
	// chosen just now and not connected is news, and it is the one moment when
	// saying so is any use, because another one can still be chosen. Guarded by
	// "the picture has not changed" alone, that line would be silent in exactly
	// that case: falling back on the camera that is already open changes
	// nothing.
	// camRecheck says which of the two closures we decide on this was: the
	// periodic look for the chosen camera coming back, rather than a choice.
	//
	// **They must be told apart in the log**, which is the microphone's rule:
	// whoever reads has to find out **why** the picture was interrupted, and
	// "somebody chose" in place of "the camera came back" sends them looking
	// for a command where there was a precaution.
	camReopen, camChosen, camRecheck atomic.Bool
	// analysisRate is the rate of the stream handed to the Level sink. It is
	// AnalysisSampleRate for any microphone, and zero until the audio is open.
	analysisRate atomic.Int64
	// encoderName and hardware describe the chosen encoder, which is only known
	// after asking the system.
	encoderName atomic.Value
	hardware    atomic.Bool

	// Verifies that keyframe requests really take effect.
	//
	// The encoder answers S_OK regardless, and on hardware other than the
	// development machine "accept and ignore" is a concrete outcome: we have
	// already seen it here, with the wrong VARIANT type. The only proof is
	// looking at whether the keyframe arrived.
	kfMu sync.Mutex
	// kfPendingAt is the frame count at the moment of the request; zero means no
	// request outstanding.
	kfPendingAt   int64
	kfAsked       int
	kfServed      int
	kfWarned      bool
	kfDeclaredBad atomic.Bool

	// The same for the bitrate: what it was asked for, how much came out, and
	// whether on this machine the simple command has shown itself inert.
	brMu    sync.Mutex
	brWant  int
	brSince time.Time
	brBytes int64
	// brWantMax is the **highest** request in force during the verification
	// window: that is the one to judge against, because if we never asked for
	// more than so much and a great deal more comes out, the command did not
	// take. See bitrateAsked for the direction, which is easy to get backwards.
	brWantMax int
	// The request and the throughput from when the proof began: they separate an
	// encoder that does not follow from one that follows with a wide
	// calibration, which the ratio alone cannot tell apart. See bitrateSeen.
	brWantRef, brMeasRef int
	brHard               bool
	// brProbed says the probe has already been run. It holds for the process and
	// not for the capture: an encoder does not change character between one
	// restart and the next, and redoing it on every backoff would mean half the
	// bitrate on every fault.
	brProbed bool
	// lastReconfig and reconfigBroken serve to give up on reconfiguration if it
	// takes the capture with it. See reconfigIsBroken.
	lastReconfig   time.Time
	reconfigBroken bool
	// brStreak counts the verifications in a row where the throughput stayed
	// above what was asked, even slightly: that is how a stubborn refusal is
	// recognised, which no single measurement tells apart from a hard scene.
	brStreak int

	// The quantiser distribution, that is how much the encoder had to
	// approximate. The histogram is over the whole H.264 scale, which has 51
	// values: keeping it whole costs 51 integers and makes it possible to ask
	// for any percentile afterwards, instead of having to decide beforehand.
	qpMu    sync.Mutex
	qpCount int64
	qpSum   int64
	qpMax   int
	qpLast  int
	qpHist  [52]int64
	// The recent window: the resolution scale has to know how much the encoder
	// is approximating **now**, not on the night's average. Here too the
	// histogram is kept and not the sum, for the same reason as above: the
	// number the scale needs is a percentile, and a percentile cannot be derived
	// from a sum.
	qpWinHist  [52]int64
	qpWinCount int64
	// qpDivergences counts the times the sample attribute and the stream said
	// two different things. Zero is the good news; a number that grows means one
	// of the two readings is wrong, and the one to believe is the stream — it is
	// what the decoder reads.
	qpDivergences atomic.Int64
	// How many times the quantiser attribute was readable and how many not. They
	// separate "this encoder does not declare it" from "we cannot read it",
	// which from outside are the same thing.
	qpAttrOK, qpAttrKO atomic.Int64
	// qpAttrDeviation is the sum of the absolute deviations between the two
	// readings. It serves to tell **by how much** they diverge: one point is the
	// difference between the slice quantiser and the macroblock average, and is
	// expected; ten would mean one of the two readings is broken.
	qpAttrDeviation atomic.Int64
	// qpAttrBias is the same sum **with the sign** (attribute minus stream), and
	// it is not a duplicate: a deviation that sits now on one side and now on the
	// other is adaptive quantisation moving away from the slice quantiser, that
	// is a phenomenon; a deviation always in the same direction is one of the two
	// readings failing systematically, that is a fault. The absolute value
	// confuses them, and that is how a measurement made expressly to check stops
	// checking.
	qpAttrBias atomic.Int64
	// qpFromAttr says which of the two readings the quantiser governing the loop
	// and the scale comes from. It serves to say it instead of leaving it to be
	// inferred: two machines measuring the same scene from two different sources
	// give different numbers, and without this line it would look like a
	// difference of scene.
	qpFromAttr atomic.Bool
	// The range of the quantiser **read from the stream**, kept apart from the
	// range of the reading in use. They serve one question, which nonetheless
	// decides everything: does that number vary? A minimum equal to the maximum
	// says the slice quantiser on this encoder is a constant, that is, that the
	// divergence with the attribute is not a broken parser but a choice by
	// whoever wrote the transform.
	qpStreamMin, qpStreamMax atomic.Int64
	// The range of the reading **in use**, the one chosen between stream and
	// attribute. While they coincide that number has not yet shown it measures
	// anything and does not enter the statistics. See qpMeasured.
	qpUsedMin, qpUsedMax atomic.Int64
	// An inventory of the attributes the encoder puts on its samples,
	// photographed **while the capture runs**. It serves to separate "this
	// encoder does not declare it" from "we do not know how to read it", which
	// from outside are the same thing, and it belongs here for the reason given
	// in SampleAttributes.
	sampleAttrs atomic.Pointer[attrInventory]

	// curKbps is the last bitrate commanded, which is not the preset's as soon
	// as the network has asked to come down. It serves to rebuild the encoder
	// with the right value: restarting from the preset would mean going back up
	// to the maximum at the very moment the picture is being made smaller
	// because the bandwidth is short — that is, doing the opposite of what is
	// being attempted.
	curKbps atomic.Int64

	// wantFormat is the format requested by the governor. The three numbers sit
	// in one struct because they have to be read together: taken from separate
	// fields, the new width could be read with the old cadence, and the encoder
	// reconfigured with a pair nobody ever asked for.
	// Nil means nothing to do.
	wantFormat atomic.Pointer[videoFormat]
	// The format in force, for whoever is looking at the status page.
	videoWidth, videoHeight, videoFPS atomic.Int64
	// startSize is the size the capture **starts from**: the preset, lowered to
	// what this camera really declares.
	//
	// **It is a value with a life, not a constant.** It used to be computed once
	// in main and handed to everyone, which is right for as long as the camera
	// cannot change; the moment it can, whoever built something on top of the old
	// number is reasoning about a camera that is no longer there. The resolution
	// scale is that somebody: its steps are fractions of this size, so a scale
	// built on a size the camera cannot deliver has no step matching what
	// arrives, and resync — which deliberately does not invent a size that is not
	// a step — would stay out of alignment for the rest of the session.
	//
	// **The three numbers travel in one struct, for the reason wantFormat above
	// already carries**: read from separate fields they can be caught mid-write,
	// and out comes a size nobody ever had — a width from the new camera with the
	// old one's height. The scale is *built* on this, so a phantom would become a
	// governor whose steps match nothing that arrives, plus a rebuild announced
	// with a size that never existed. Nil until the first open.
	startFormat atomic.Pointer[videoFormat]
	// videoDelivered is the **delivered** cadence, that is how many frames the
	// gate lets through: the scale commands it, and it is the only one of the
	// three that says whether we are taking pictures away on purpose.
	//
	// It is not videoFPS. That one is what is **declared** to the encoder, and
	// it chases the camera: in the dark it drops to 10 without anyone dropping
	// anything. Showing the declared one in its place turns the reduced-cadence
	// warning on every evening, and would leave it off in the one case it exists
	// for.
	videoDelivered atomic.Int64
	// curMinQP is the quantiser floor, taken from the configuration and applied
	// by building the encoder. It is not changed live: MinQP is the twin of
	// MaxQP, which the documentation declares settable **only before** the
	// session starts, and about MinQP it says nothing — silence is not
	// permission.
	curMinQP atomic.Int64
	// wantReconfig is the bitrate to impose by reconfiguring the transform. Zero
	// means nothing to do. Like everything that touches the encoder, the video
	// loop applies it: see ReconfigureBitrate.
	wantReconfig atomic.Int64
	// curMode is the bit-spending criterion in force, decided at construction.
	curMode atomic.Value

	// bitrate and keyframe are the commands that can be given to the encoder
	// while it works. They live here and not in the engine because whoever
	// invokes them — the congestion control, a viewer's PLI — knows nothing
	// about sessions.
	control atomic.Pointer[mf.VideoEncoder]
	// encMu protects the encoder for **the whole time it is used**, not only
	// while the pointer is read.
	//
	// An atomic pointer was enough while the encoder stayed the same for the
	// whole capture. Since the resolution scale **replaces** it, it is not:
	// whoever had just loaded it finds an already released COM object in their
	// hands, and the first method called on it takes the process out. On a baby
	// monitor that is the camera going dark in the middle of the night, so no
	// expense is spared here: whoever commands holds the lock for reading,
	// whoever replaces takes it for writing.
	encMu sync.RWMutex
}

// MicDevice identifies a capture endpoint: the stable ID it is reopened by and
// the readable name that is shown.
//
// **They are two fields and not one** for the usual reason: the name is not an
// identifier — two microphones of the same model share it — and the ID is not a
// word to show anybody.
type MicDevice struct {
	ID   string
	Name string
}

// Camera identifies a webcam: the symbolic link it is reopened by and the
// readable name that is shown.
//
// It is MicDevice's twin and for the same reason: two cameras of the same model
// share the name, so the name cannot identify one, and the link is ninety-four
// characters of device path, so it cannot be the word shown to anybody.
type Camera struct {
	Link string
	Name string
}

func New(cfg Config) *Pipeline {
	cfg.applyDefaults()
	p := &Pipeline{cfg: cfg}
	p.encoderName.Store("")
	p.curMinQP.Store(int64(cfg.MinQP))
	// The configuration says which microphone capture starts from, and from
	// here on micWanted is in command: it is the only point where the two touch.
	p.micWanted.Store(cfg.MicDeviceID)
	// And the same for the camera, for the same reason.
	p.camWanted.Store(cfg.CameraLink)
	// Capacity of one: the wake-up is a fact, not a queue. Whoever sends it must
	// never wait, and two choices close together are worth one reopen.
	p.micWake = make(chan struct{}, 1)
	p.camWake = make(chan struct{}, 1)
	return p
}

// RawAudioMode says whether the audio is bypassing the OEM's effects.
//
// **It describes the last open, not this instant**, the same way Microphone
// does and for the same reason: it is written where the capture opens and is
// not cleared when the capture stops, because the road obtained is the road
// that will be obtained again. A boolean has two values and the question has
// three — raw, filtered, no capture at all — so `false` on a machine that has
// never opened a microphone is indistinguishable from raw being refused, and
// `true` outlives the device being unplugged.
//
// **Whoever shows this to somebody must ask whether there is audio first**,
// which is the different question. Both directions have been shown on the
// page: "audio filtered by the system" over a microphone that was not there,
// and "unfiltered audio: yes" over one that had gone. The guard lives at the
// four edges that say it in words — the tray's case order, the guided path's
// `micOk`, the viewer's `rawAudioState`, and `pat-capture`, which asks its own
// measurement because by the time it reports, `Run` has returned and
// `AudioActive` is already false. **Only the two in the browser are held by a
// test**; the two in Go are a rule written down, and the chapter says why no
// guard was added for them.
func (p *Pipeline) RawAudioMode() bool { return p.rawMode.Load() }

// MicrophoneMuted says the endpoint is muted in Windows.
//
// **It is read from the endpoint and not deduced from the silence**, which is
// the whole of it: deduced, a mute looks exactly like a microphone that has
// stopped working, and the two have nothing in common — one is undone by a
// click on this machine and the other is a fault. It is the argument
// `micDenied` already makes about a withdrawn permission.
//
// **It describes the last open, like RawAudioMode**, and for a reason of its
// own: reading it again means asking the endpoint from a thread that is not the
// one holding it, and the capture loop is not a place to put a call that crosses
// into the audio service.
//
// **What makes that enough is the re-examination**, which a muted open arms like
// any other worse path: the monitor comes back to look every two minutes and
// disarms the timer when it finds the endpoint unmuted. Without that arming this
// field would be a latch, and the command the tray panel offers under it — open
// the Windows Sound page — would be a remedy that never reaches the monitor. See
// micRecheckWanted.
//
// The price is that the news is late: up to two minutes between somebody
// unmuting and the room being heard again, which is the figure the chosen
// microphone's return already carries.
func (p *Pipeline) MicrophoneMuted() bool { return p.micMuted.Load() }

// AudioActive says whether the microphone is capturing right now.
func (p *Pipeline) AudioActive() bool {
	if p.audioOK.Load() {
		return true
	}
	// **A planned reopen is not an absence.** See micRecheckGrace.
	return time.Now().UnixNano() < p.micGraceUntil.Load()
}

// inMicGrace says whether we are inside a planned reopen.
//
// It serves the two warnings the open writes: inside the grace they are repeats
// of news nobody has seen go away, and the log would collect a thousand and a
// half of them a night.
func (p *Pipeline) inMicGrace() bool {
	return time.Now().UnixNano() < p.micGraceUntil.Load()
}

// Microphone is the capture endpoint open right now, empty until one has been
// opened.
//
// It is not cleared when the capture stops, and that is deliberate: it is the
// last microphone opened, that is the one that will be reopened. Whoever wants
// to know whether it is capturing **now** has AudioActive, which is the
// different question.
func (p *Pipeline) Microphone() MicDevice {
	if d := p.micOpen.Load(); d != nil {
		return *d
	}
	return MicDevice{}
}

// Camera is the webcam capturing right now, empty until one has been opened.
//
// Like Microphone it is not cleared when the capture stops: it is the last one
// opened, that is the one the next attempt starts from. "Is anything arriving
// now?" is a different question, and the frame counters answer it.
func (p *Pipeline) Camera() Camera {
	if c := p.camOpen.Load(); c != nil {
		return *c
	}
	return Camera{}
}

// CameraIsFallback says the camera being captured is not the one that was
// chosen, that is, the chosen one is not connected.
func (p *Pipeline) CameraIsFallback() bool { return p.camFellBack.Load() }

// CameraDenied and MicrophoneDenied say Windows is refusing the device because
// the permission is off.
//
// **They are asked separately from "is it capturing?" and they have to be**: a
// refused camera delivers no frames and a refused microphone reports no level,
// so from those two numbers alone a revoked permission is indistinguishable
// from a broken cable. These answer the question the other two cannot, and they
// are what lets the interface say *the camera permission is off* instead of
// *no images from the camera*, which is true and leads nowhere.
func (p *Pipeline) CameraDenied() bool { return p.camDenied.Load() }

// MicrophoneDenied says Windows is refusing the microphone. See CameraDenied.
func (p *Pipeline) MicrophoneDenied() bool { return p.micDenied.Load() }

// CameraOpening and MicrophoneOpening say the device is being opened right now
// and the call has not answered. See camOpening.
func (p *Pipeline) CameraOpening() bool { return p.camOpening.Load() }

// MicrophoneOpening says the microphone is being opened. See CameraOpening.
func (p *Pipeline) MicrophoneOpening() bool { return p.micOpening.Load() }

// CameraOpenedUnix is when the camera last came open, zero before the first
// time. See camOpenedAt.
func (p *Pipeline) CameraOpenedUnix() int64 { return p.camOpenedAt.Load() }

// camWantedLink is the link of the camera we want to open.
func (p *Pipeline) camWantedLink() string {
	link, _ := p.camWanted.Load().(string)
	return link
}

// SetCamera chooses which camera to capture from, without restarting anything.
//
// The choice is applied by **reopening**, which is the microphone's road and
// the same one the restart loop already walks: the capture in progress is
// closed and the supervisor opens the new camera at once. A second way of
// opening a camera would be a second open, and the new one would be the less
// tested of the two.
//
// **It reopens even when the link is the one from before**, and that is not
// waste: if the chosen camera had been unplugged the capture is coming from
// another one, and repeating the choice is the only way whoever is watching has
// of saying "try again now".
//
// **There is no grace here, unlike the microphone's**, and the reason is a
// measurement rather than a difference of principle: what a closed microphone
// makes false is `AudioActive`, read every second, while what a closed camera
// makes false is `capture-stopped`, which asks for half a minute of no frames.
// A reopen lasting a second cannot reach it.
func (p *Pipeline) SetCamera(link string) {
	// **The order is choose, mark, close.** Closing first would reopen reading
	// the old choice, and the new one would sit there with nobody applying it.
	p.camWanted.Store(link)
	p.camChosen.Store(true)
	p.camReopen.Store(true)
	if cancel, ok := p.camCancel.Load().(context.CancelFunc); ok {
		cancel()
	}
	// And if there was nothing to close, whoever is waiting to try again gets
	// woken: see camWake.
	select {
	case p.camWake <- struct{}{}:
	default:
	}
}

// micWantedID is the ID of the endpoint we want to open.
func (p *Pipeline) micWantedID() string {
	id, _ := p.micWanted.Load().(string)
	return id
}

// SetMicDevice chooses which microphone to capture from, without restarting
// anything.
//
// The choice is applied by **reopening**: the capture in progress is closed and
// the supervisor reopens it at once on the new endpoint. There is no second road
// for opening a microphone, and it is the same one as the periodic path recheck
// — grace included, so the fraction of a second of closure is not announced as a
// missing microphone.
//
// **It reopens even when the ID is the one from before**, and that is not waste:
// if the chosen microphone was unplugged, capture is coming from another one,
// and repeating the choice is the only way whoever is watching has of saying
// "try again now".
func (p *Pipeline) SetMicDevice(id string) {
	// **The order is choose, mark, close.** Closing first would reopen reading
	// the old choice, and the new one would sit there with nobody applying it: a
	// command that moves nothing and does not say so.
	p.micWanted.Store(id)
	p.micChosen.Store(true)
	p.micReopen.Store(true)
	if cancel, ok := p.micCancel.Load().(context.CancelFunc); ok {
		cancel()
	}
	// And if there was nothing to close, whoever is waiting to try again gets
	// woken: see micWake.
	select {
	case p.micWake <- struct{}{}:
	default:
	}
}

// EncoderName is the name of the video encoder in use, empty until one has been
// chosen.
func (p *Pipeline) EncoderName() string {
	s, _ := p.encoderName.Load().(string)
	return s
}

// HardwareEncoder says whether the encoding is happening on the GPU.
func (p *Pipeline) HardwareEncoder() bool { return p.hardware.Load() }

// SetBitrate changes the video bitrate while the capture is running.
//
// It is one of the two reasons this engine exists: on 5G the bandwidth swings
// constantly, and a fixed bitrate either wastes or loses packets.
func (p *Pipeline) SetBitrate(kbps int) error {
	p.encMu.RLock()
	defer p.encMu.RUnlock()

	enc := p.control.Load()
	if enc == nil {
		return fmt.Errorf("no capture running")
	}
	p.curKbps.Store(int64(kbps))
	// On the machine where the simple command has already shown itself inert it
	// is not repeated: that would be a wasted round exactly when the network is
	// asking for help.
	//
	// **It is deposited, not executed here.** This function runs on the
	// congestion control's thread, and reconfiguring means stopping and
	// restarting the transform while the video loop is calling Feed and
	// ProcessOutput on it. That was the real cause of the capture restarts on
	// AMD, and it stayed hidden for a while: the safe road had already been
	// built, but **this line was not using it** and called the encoder straight.
	// Correcting the wrong path changes nothing, and the log said so — the
	// restarts went on and the giving-up never fired.
	if p.BitrateNeedsReconfig() {
		return p.ReconfigureBitrate(kbps)
	}
	p.bitrateAsked(kbps)
	return enc.SetBitrate(kbps)
}

// ReconfigureBitrate changes the bitrate by restarting the transform.
//
// It is the road for encoders that accept SetBitrate and do not execute it —
// Quick Sync, that is the integrated GPU of almost every laptop. It is not the
// first choice because it stops and restarts the transform, and it is taken when
// the other one has been **measured** inert.
// **The reconfiguration is deposited and the video loop applies it**, like the
// size change and the quantiser floor.
//
// It is the only thing that modified the transform **from outside**, and the
// only one that broke. Stopping the encoder, reassigning its format and
// restarting it while the video loop is calling Feed and ProcessOutput on it
// means pulling the object out from under it: the prescribed answer to whoever
// calls ProcessOutput without the right to is E_UNEXPECTED, and the loop reads
// that as a fault. Measured on AMD: three capture restarts in half a minute,
// with `ProcessOutput: HRESULT 0x8000FFFF`, which went away as soon as the watch
// stopped reconfiguring.
//
// The lock was not enough and could not be: `encMu` is taken **for reading** and
// protects the replacement of the encoder, not its modification in place — for
// reading it excludes nobody, and the video loop does not take it at all. It is
// the same fault one floor further on: before it was "whoever commands can find
// an already closed encoder in their hands", now "an encoder somebody else is
// stopping".
//
// Zero cancels a request not yet applied.
func (p *Pipeline) ReconfigureBitrate(kbps int) error {
	if kbps < 0 {
		return nil
	}
	if p.control.Load() == nil {
		return fmt.Errorf("no capture running")
	}
	// The value is noted here **and** in SetBitrate: they are two doors into the
	// same room, and one that forgets is enough to lose the command. Found this
	// way: a size change arriving straight afterwards rebuilt the encoder at the
	// preset's bitrate, cancelling the 700 kbit/s just asked for — that is,
	// sending the maximum back onto the network at the very moment the picture
	// was being made smaller because the bandwidth was short.
	p.curKbps.Store(int64(kbps))
	p.wantReconfig.Store(int64(kbps))
	return nil
}

// RateMode is the criterion in force now.
func (p *Pipeline) RateMode() mf.RateControl {
	s, _ := p.curMode.Load().(string)
	if s == "" {
		return mf.RateCBR
	}
	return mf.RateControl(s)
}

// videoFormat is size and cadence together, which is the way the scale decides
// them and therefore the way they have to be applied.
//
// **The cadences are two, and confusing them closes a loop on itself.** `FPS` is
// how many frames are **delivered**, that is how many the gate lets through;
// `Declared` is how many are **declared** to the encoder so it divides the
// budget by the right number. They are different quantities: in the dark the
// gate is wide open (30) and the camera delivers 9.8, so 10 is declared without
// dropping anything.
//
// Keeping them as one number makes the declaration drive the gate, and the
// declaration is computed from the **measured** cadence — which with the gate
// shut is ours, not the camera's. Measured on wifi: the scale had come down to
// the floor of 2 fps, the gate delivered 2, the measurement read 2.1 and the
// declaration came back to 2, commanding the gate again. Six minutes at 2 fps
// with the estimate at 2696 kbit/s, and the resolution climbing back to 1280x720
// in the meantime — that is, a large sharp picture updating twice a second, a
// state the specification does not even allow, because the cadence steps live
// only on the last size.
type videoFormat struct{ W, H, FPS, Declared int }

// cadenceGate decides which frames reach the encoder when the cadence has been
// lowered.
//
// **Incoming frames are dropped, the camera is not slowed down.** A cadence
// asked of a device can be refused or rounded, and it remains to be discovered
// to what value; counting them here we know the real cadence by construction.
// It holds the other way round too: the camera slows down by itself in the dark,
// and a gate anchored to time still delivers the wanted cadence as long as there
// is enough coming in.
//
// The wait is anchored to the last frame **passed** and not to an absolute grid:
// if the camera delivers irregularly, a grid would accumulate the delay and
// bring out two frames in a row to catch up.
type cadenceGate struct {
	interval time.Duration
	last     time.Time
}

// setFPS sets the wanted cadence. Equal to or above the incoming one means drop
// nothing: a gate that lets everything through is the ordinary case, and it is
// as well that it costs nothing.
func (c *cadenceGate) setFPS(want, in int) {
	if want <= 0 || in <= 0 || want >= in {
		c.interval = 0
		return
	}
	c.interval = time.Second / time.Duration(want)
}

// due says whether this frame is to be delivered to the encoder.
//
// The margin of a tenth of the interval avoids losing a frame over a few
// milliseconds of earliness and ending up with half the wanted cadence: with the
// camera at 30 and the gate at 15, a frame arriving an instant early would be
// dropped and the next would fall a whole interval later.
func (c *cadenceGate) due(now time.Time) bool {
	if c.interval <= 0 {
		return true
	}
	if c.last.IsZero() || now.Sub(c.last) >= c.interval-c.interval/10 {
		c.last = now
		return true
	}
	return false
}

// newMotionGate builds the gate that decides which frames reach detection.
//
// It is a function so that the rule can be checked without a camera: what it
// has to hold is that the analyses come out at MotionFPS whatever cadence the
// camera really delivers, which is the property a divisor of the declared one
// does not have.
func newMotionGate(cameraFPS int) *cadenceGate {
	g := &cadenceGate{}
	g.setFPS(MotionFPS, cameraFPS)
	return g
}

// SetVideoFormat asks for frames of another size or cadence to be delivered.
//
// The request is asynchronous on purpose: the change touches the Source Reader,
// the encoder and the reducer for motion detection, which have to move together,
// and the place where that is true is the video loop. Here only the intention is
// deposited, and the loop picks it up on the next turn.
//
// A request that replaces another one not yet applied is the right outcome: what
// counts is the last format wanted, not the queue of the ones passed through.
// `delivered` is the frames that have to reach the encoder, `declared` how many
// are announced to it: see videoFormat for why they are two. A non-positive
// declared means "same as delivered", which is the case for whoever has no
// cadence governor — pat-capture, and the tests.
func (p *Pipeline) SetVideoFormat(w, h, delivered, declared int) {
	if w <= 0 || h <= 0 || delivered <= 0 {
		return
	}
	if declared <= 0 {
		declared = delivered
	}
	p.wantFormat.Store(&videoFormat{W: w, H: h, FPS: delivered, Declared: declared})
}

// qpMeasured records a sample of the reading in use, but lets it into the
// statistics **only after having seen it change at least once**.
//
// **A reading that never varies is not a measurement, and it is dangerous
// precisely because it has every appearance of one.** On Quick Sync the slice
// quantiser is 26 on every frame: with a target of 30 the loop would conclude
// "quality exceeds" on every turn and go down to the floor for ever.
//
// The defence is here and not in the loop, where a first attempt produced a
// false positive on AMD in under a minute: there the question was "did the
// number react to the drop in bits?", which during a successful saving has **the
// same answer** as the fault. Here it is "does this number ever change?", which
// does have a clean answer. The delay of a few frames costs the saving, never
// the picture: with no quantiser the loop sits at the cap.
//
// The judgement is not withdrawn: once the number has shown it moves, standing
// still is news about the scene, no longer about the reading.
func (p *Pipeline) qpMeasured(qp int) {
	if qp <= 0 || qp > 51 {
		return
	}
	v := int64(qp)
	if p.qpUsedMin.Load() == 0 || v < p.qpUsedMin.Load() {
		p.qpUsedMin.Store(v)
	}
	if v > p.qpUsedMax.Load() {
		p.qpUsedMax.Store(v)
	}
	if p.qpUsedMax.Load() > p.qpUsedMin.Load() {
		p.qpSeen(qp)
	}
}

// qpSeen keeps the quantiser's **distribution**, not its mean: the mean of a
// night hides the few seconds in which something moved, which are the only ones
// where whoever is watching sees the blocks.
func (p *Pipeline) qpSeen(qp int) {
	if qp <= 0 || qp > 51 {
		return
	}
	p.qpMu.Lock()
	p.qpLast = qp
	p.qpWinHist[qp]++
	p.qpWinCount++
	p.qpCount++
	p.qpSum += int64(qp)
	if qp > p.qpMax {
		p.qpMax = qp
	}
	p.qpHist[qp]++
	p.qpMu.Unlock()
}

// QPStats summarises how the encoder is working: how much it approximates, and
// how much it approximates at the worst point.
type QPStats struct {
	Known   bool
	Samples int64
	Mean    float64
	P95     int
	Max     int
}

// qpWinPercentile is the percentile the recent window hands to the resolution
// scale.
//
// **It is not the mean, and that is the point.** The blocks show in the worst
// frames, not in the mean: measured in CBR on the same scene, mean 29.9 and
// maximum 43. A mean sitting comfortably below the break threshold can hide one
// frame in ten that sits well above it, and that is exactly the frame whoever is
// watching notices.
//
// **Ninety and not ninety-five**, which is the diagnostics one instead. The
// window is one second, that is twenty to thirty samples: over twenty-five
// samples the p95 is effectively the maximum, and the maximum of each second is
// almost always the keyframe — which is encoded with a different quantiser from
// the others by construction, so it would put the scale under the command of a
// frame that says nothing about how hard the scene is. The p90 over the same
// second is the third worst frame: it still catches the tail, without chasing a
// single sample.
const qpWinPercentile = 90

// TakeRecentQP returns the quantiser percentile since it was last asked for, and
// clears the window.
//
// It takes rather than looks, on purpose: whoever decides the resolution scale
// calls it once a second, and so always gets the last second of encoding instead
// of a statistic that keeps lengthening. The single frame would be too noisy,
// and the cumulative one from the start would say what the room was like half an
// hour ago — which is the mistake already made with the cadence. For the same
// reason **the p95 of `QP()` is not used**: that one is over the whole run and
// ages.
//
// The value serves two governors at once, and they want two different things
// from it: the scale wants the instant, because one bad second has to be caught
// straight away, while the quality loop wants the stretch, because it decides
// how many bits to buy for the scene. **It is damped where it is decided, not
// where it is measured** — see scaleQPWindow and the loop in internal/rtc.
//
// One consumer only: if two were needed, the second would find the window
// already emptied.
func (p *Pipeline) TakeRecentQP() (int, bool) {
	p.qpMu.Lock()
	defer p.qpMu.Unlock()
	if p.qpWinCount == 0 {
		return 0, false
	}
	// The threshold rounds **up** and never falls below one: with few samples
	// the truncation would give zero, and a cumulative count starting from zero
	// is already >= 0 at the first value of the histogram — that is, it would
	// answer with the lowest quantiser in the window exactly when the worst one
	// is being asked for.
	threshold := max((p.qpWinCount*qpWinPercentile+99)/100, 1)
	qp := 0
	var cum int64
	for q := 1; q <= 51; q++ {
		cum += p.qpWinHist[q]
		if cum >= threshold {
			qp = q
			break
		}
	}
	p.qpWinHist = [52]int64{}
	p.qpWinCount = 0
	return qp, qp > 0
}

// QPDivergences counts how many times the sample attribute said something
// different from the stream.
//
// Zero is the good news. A number that grows says one of the two readings is
// wrong — and the one to believe is the stream, which is what the decoder reads.
// On an encoder that does not set the attribute it stays zero without saying
// anything, and it has to be read together with the number of samples.
func (p *Pipeline) QPDivergences() int64 { return p.qpDivergences.Load() }

// QP returns the quantiser distribution observed so far.
func (p *Pipeline) QP() QPStats {
	p.qpMu.Lock()
	defer p.qpMu.Unlock()
	if p.qpCount == 0 {
		return QPStats{}
	}
	s := QPStats{
		Known:   true,
		Samples: p.qpCount,
		Mean:    float64(p.qpSum) / float64(p.qpCount),
		Max:     p.qpMax,
	}
	threshold := p.qpCount * 95 / 100
	var cum int64
	for q := 1; q <= 51; q++ {
		cum += p.qpHist[q]
		if cum >= threshold {
			s.P95 = q
			break
		}
	}
	return s
}

// currentBitrate is the last bitrate commanded, or the preset's if the
// congestion control has not spoken yet.
func (p *Pipeline) currentBitrate() int {
	if v := p.curKbps.Load(); v > 0 {
		return int(v)
	}
	return p.cfg.BitrateKbps
}

// VideoFormat is the size and cadence in force now.
//
// The cadence is declared together with the size because below the last step it
// is **the cadence** that changes, and a picture that updates every two seconds
// with nothing saying so reads as a camera that has wedged.
func (p *Pipeline) VideoFormat() (w, h, fps int) {
	return int(p.videoWidth.Load()), int(p.videoHeight.Load()), int(p.videoFPS.Load())
}

// pickSize is mf.PickCameraSize's shape, taken as a parameter because that
// function interrogates the system — and a function that interrogates the system
// cannot be tested, only run.
type pickSize func(w, h, fps int) (int, int, int, bool, error)

// cameraSize resolves the size to ask the camera for.
//
// **Whether the request is lowered to what the camera declares belongs to the
// caller, and the safe answer is the zero value.** The monitor wants it lowered:
// with the advanced video processing on, a request larger than the camera does
// not fail — the Source Reader puts a scaler in the middle and enlarges, so the
// log reports the preset while the picture carries not one extra detail, and on
// a C210 that meant no frame delivered in twenty minutes. A measuring instrument
// wants the opposite, because the numbers are whoever ran it: lowering them
// underneath reports a test that is not the one asked for, and pat-capture
// declares the upscale in a line of its own precisely so it can measure it.
//
// Hence the polarity: whoever writes nothing gets the protection, and whoever
// measures has to say so.
func cameraSize(w, h, fps int, keep bool, pick pickSize, log *slog.Logger) (int, int, int) {
	if keep {
		return w, h, fps
	}
	pw, ph, pf, clamped, err := pick(w, h, fps)
	switch {
	case err != nil:
		log.Warn("cannot read the formats the camera declares, asking for the preset as it is",
			"error", err)
	case clamped:
		return pw, ph, pf
	}
	return w, h, fps
}

// StartSize is the size the capture starts from: the preset lowered to what
// this camera really has. Zeros until the first open, which is the honest answer
// — before the camera is opened nobody knows what it declares.
//
// The three come out of one pointer, so they are always a size somebody really
// asked for: see startFormat.
func (p *Pipeline) StartSize() (w, h, fps int) {
	f := p.startFormat.Load()
	if f == nil {
		return 0, 0, 0
	}
	return f.W, f.H, f.FPS
}

// setStartSize records that size, and says so **only when it changes**: this
// runs at every open, that is, at every capture restart and every backoff, and a
// line repeated all night hides the one time it meant something.
func (p *Pipeline) setStartSize(w, h, fps int) {
	if f := p.startFormat.Load(); f != nil && f.W == w && f.H == h && f.FPS == fps {
		return
	}
	p.startFormat.Store(&videoFormat{W: w, H: h, FPS: fps, Declared: fps})
	if w != p.cfg.Width || h != p.cfg.Height || fps != p.cfg.FPS {
		p.cfg.Log.Warn("the camera has fewer pixels than the preset: capturing at its own size",
			"preset", fmt.Sprintf("%dx%d@%d", p.cfg.Width, p.cfg.Height, p.cfg.FPS),
			"camera", fmt.Sprintf("%dx%d@%d", w, h, fps))
	}
}

// DeliveredFPS is how many frames per second the gate lets through.
//
// It sits beside `VideoFormat` and not inside it because it answers a different
// question: that one says what format we are encoding with, this one whether we
// are **taking pictures away on purpose**. They are equal almost always, and
// when they are not is the only moment the difference has to be told to whoever
// is watching — a picture that updates every two seconds with nothing declaring
// it is indistinguishable from a wedged camera.
func (p *Pipeline) DeliveredFPS() int { return int(p.videoDelivered.Load()) }

// The two roads for changing the bitrate, and why both are needed.
//
// Neither works everywhere, and that is measured on this very machine with
// `pat-capture -br 2500 -br2 800`, which weighs the two halves of the proof:
//
//	                    SetValue        reconfiguration
//	Quick Sync          2418 -> 2375    2431 -> 746
//	software encoder    2413 ->  829    2403 -> 2407
//
// They are exactly complementary. Keeping one road for the sake of consistency
// would mean choosing which half of the users to leave without congestion
// control — and the cost of not having it is measured: from a cellular network,
// 300 kbit/s asked for over twenty seconds, 2500 produced, 33% of the packets
// lost.
//
// The consistency is therefore in the **rule**, which is one for everybody: try
// the road that interrupts nothing, weigh what comes out, and go over to the
// other only if the first did not take. No list of manufacturers: an "Intel yes,
// AMD no" list ages in silence, and the next user has a chip we have never seen.
const (
	// bitrateVerifyAfter is how long to wait before judging a command. More than
	// one GOP: the frames already in flight come out with the old value, and the
	// encoder's internal control has to fill its own window.
	bitrateVerifyAfter = 4 * time.Second
	// bitrateProofScale: by how much the request has to have dropped before
	// anything can be judged, in tenths.
	//
	// **It is the proof, and without it nobody is accused.** If we never asked
	// for appreciably less, an encoder producing more than it was asked for may
	// be deaf or may just have a wide calibration, and the two cases are
	// indistinguishable: a zero means "I do not know", never "zero".
	//
	// **Half, and a fifth was not enough.** The throughput is measured over four
	// seconds of a scene that changes, and between one window and the next they
	// easily swing by 20-30%: observed on AMD, reference 816 asked with 724
	// produced, then 620 asked with 766 produced — that is, 24% less asked for
	// and 6% more produced, which formally is "it does not follow" and was
	// noise, with the ratio going from 0.89 to 1.24 between two windows.
	//
	// Asking for half puts the proof well above the noise, and costs nothing in
	// sensitivity: the phenomenon to be recognised is enormous — a deaf encoder
	// stays at 2400 while being asked for 800 — so it goes past anyway, and soon.
	bitrateProofScale = 5

	// bitrateStubbornStreak: how many windows in a row are needed to conclude.
	//
	// Two windows of four seconds. A single one could fall on a transient — a
	// keyframe, a scene changing at that very instant — and the cost of being
	// wrong is moving the machine for good onto the road that stops and restarts
	// the encoder.
	bitrateStubbornStreak = 2

	// --- the probe --------------------------------------------------------
	//
	// **The watch waits for a big step, and in service it does not come.** On
	// Quick Sync the quantiser sits against the target, so the quality loop asks
	// for between 77% and 100% of the cap and never halves: the proof
	// `bitrateSeen` needs does not present itself, and the saving never switches
	// on for that chip. Measured: two minutes of requests between 1932 and 2500
	// with the throughput stuck at 2300-2500.
	//
	// **And a ratio does not tell them apart.** The previous criterion accused
	// whoever produced more than they were asked for, and caught Quick Sync
	// (1655 against 1322, that is 1.25) — but it also caught AMD, which obeys
	// with a calibration a third wide (662 against 551, that is 1.20). The two
	// signatures overlap, which is why that criterion was replaced.
	//
	// What separates the two is whether the throughput **moves when the request
	// moves**. Seeing that needs a big step: instead of waiting for one, one is
	// made. Once per process, as soon as the capture starts and before anyone is
	// watching.

	// bitrateProbeFraction: how much is asked for during the probe, in tenths of
	// the starting bitrate.
	//
	// Half and not a third: the step has to sit well above the noise of the
	// measurement, which between nearby windows reaches 20-30%, and a halving is
	// the largest that can be asked for without the picture becoming
	// unrecognisable for the seconds it lasts.
	bitrateProbeFraction = 5

	// bitrateProbeWait: how long to wait before reading the effect.
	//
	// More than one GOP, like bitrateSettle: the frames already in flight come
	// out with the old bitrate, and judging inside the transient would accuse
	// whoever is answering.
	bitrateProbeWait = 4 * time.Second

	// bitrateProbeDelay: how long the capture is left to settle before starting.
	// The first frames of an encoder starting up do not describe the scene, they
	// describe the encoder starting up.
	bitrateProbeDelay = 3 * time.Second

	// bitrateProbeDrop: by how much the throughput has to have dropped for the
	// command to count as having taken, in tenths of the halving asked for.
	//
	// Asking for half, half is expected; far less than that is accepted, because
	// an encoder may have a wide calibration and because the scene changes on its
	// own. It is enough that it moved **in the right direction and
	// unmistakably**: a fifth of the step asked for is well above the noise and
	// well below real obedience.
	bitrateProbeDrop = 2
)

// probeBitrate asks for half the bitrate once only and looks at what happens.
//
// **It is an experiment, not a heuristic.** The continuous watch stays where it
// is and goes on doing its job; this gives it the answer it would otherwise wait
// for ever for on an encoder where the quality loop never asks for little
// enough.
//
// Three things make it harmless, and all three have to be kept:
//
//   - **once per process**, not per capture restart. An encoder does not change
//     character between one restart and the next, and repeating the probe on
//     every backoff would mean half the bitrate on every fault;
//   - **before anyone is watching.** The capture starts with the program and the
//     viewers arrive later: nobody sees the four seconds at half bitrate. If
//     somebody is already there — which happens only by reopening the page at
//     the right instant — they see a few seconds of softer picture, once in the
//     life of the process;
//   - **the bitrate is put back as it was** by the road that has been chosen in
//     the meantime, otherwise the probe would leave the monitor at half
//     bandwidth.
func (p *Pipeline) probeBitrate(ctx context.Context, base int) {
	if base <= 0 {
		return
	}
	// **An interrupted probe is not a probe, and must not count as one.**
	//
	// `brProbed` is set by the caller **before** this goroutine starts — once
	// per process, so that two overlapping sessions cannot both probe — and an
	// abort therefore has to give the flag back. Otherwise the one question that
	// tells a deaf encoder from an obedient one is never asked again for the
	// whole process, and what is left is `bitrateSeen`, which on Quick Sync
	// stays silent by construction: that is the whole reason this probe exists.
	//
	// **It became reachable when the capture's context became per-session**, so
	// that choosing a camera can close it. Before that this goroutine held Run's
	// context and outlived every restart — which was not right either, because
	// it would then measure bytes across a gap, but it did produce a verdict.
	// Aborting is the correct half; re-arming is the half that was missing.
	//
	// And what it lowered goes back on the way out as well: the value in force
	// is remembered by the pipeline and a rebuilt encoder starts from it, so an
	// abort between the two measurements would leave the next encoder at half
	// the preset with nobody having asked for it.
	var asked, before int
	lowered, completed := false, false
	putBack := func() {
		if kbps, restore := probeRestore(p.currentBitrate(), asked, before); restore {
			if err := p.SetBitrate(kbps); err != nil {
				p.cfg.Log.Warn("bitrate not restored after the probe", "error", err, "kbps", kbps)
			}
			return
		}
		p.cfg.Log.Info("bitrate left as it is after the probe",
			"in_force_kbps", p.currentBitrate(),
			"why", "someone else commanded while the probe was measuring")
	}
	defer func() {
		if completed {
			return
		}
		p.brMu.Lock()
		p.brProbed = false
		p.brMu.Unlock()
		if lowered {
			putBack()
		}
		p.cfg.Log.Info("the bitrate probe did not finish, it will be asked again",
			"why", "the capture closed while it was measuring", "lowered", lowered)
	}()
	sleep := func(d time.Duration) bool {
		select {
		case <-ctx.Done():
			return false
		case <-time.After(d):
			return true
		}
	}
	if !sleep(bitrateProbeDelay) {
		return
	}

	// The starting throughput is measured on the bytes the video loop has
	// already counted: no new counter is needed, and this is the same number the
	// watch looks at.
	measure := func(d time.Duration) int {
		before := p.Stats.VideoBytes.Load()
		start := time.Now()
		if !sleep(d) {
			return -1
		}
		delta := p.Stats.VideoBytes.Load() - before
		sec := time.Since(start).Seconds()
		if sec <= 0 {
			return -1
		}
		return int(float64(delta) * 8 / 1000 / sec)
	}

	start := measure(bitrateProbeWait)
	if start <= 0 {
		return
	}

	// **The bitrate in force is not necessarily `base`.** The probe waits some
	// ten seconds from the start of the capture, and in that window somebody may
	// have connected: from then on the loop is in command, and what has to go
	// back is its number, not the preset. See the restore at the bottom.
	before = p.currentBitrate()

	asked = base * bitrateProbeFraction / 10
	if err := p.SetBitrate(asked); err != nil {
		return
	}
	lowered = true
	after := measure(bitrateProbeWait)
	if after < 0 {
		return
	}

	took, judgeable := probeVerdict(start, asked, after)
	switch {
	case !judgeable:
		// It is not a fault and it is not a verdict: it is a question that could
		// not be asked here. It is said, because without this line the log would
		// not tell "the probe cleared the encoder" from "the probe measured
		// nothing", and the watch carries on by itself.
		p.cfg.Log.Info("the bitrate probe proves nothing on this scene",
			"asked_kbps", asked, "before_kbps", start, "after_kbps", after,
			"why", "the encoder was already producing less than the probe asked for",
			"note", "the verdict is left to bitrateSeen")
	case !took:
		p.brMu.Lock()
		already := p.brHard
		p.brHard = true
		p.brStreak = 0
		p.brMu.Unlock()
		if !already {
			p.cfg.Log.Warn("the bitrate command has no effect on this encoder",
				"asked_kbps", asked, "before_kbps", start, "after_kbps", after,
				"encoder", p.EncoderName(),
				"how", "asked for half the bitrate and the output did not drop",
				"note", "from now on the bitrate is changed by rebuilding the encoder")
		}
	default:
		p.cfg.Log.Info("the bitrate command takes effect on this encoder",
			"asked_kbps", asked, "before_kbps", start, "after_kbps", after)
	}

	// It goes back as it was. It goes through SetBitrate, so it uses the road
	// just chosen: on a deaf encoder the first thing the reconfiguration does is
	// put back the bitrate the probe lowered.
	putBack()
	completed = true
}

// probeVerdict weighs the experiment: `took` says whether the command had an
// effect, `judgeable` whether the question was a legitimate one.
//
// **An encoder is only judged if the request was the constraint**, and that is
// the rule this function exists in order not to forget. `bitrateSeen` has had it
// for a long time — either the new cap cuts what was coming out, or it is not a
// cap — and the probe did not: it halved the **preset** and judged the drop,
// even when the encoder was already producing less than that. On a still scene
// that happens easily, because in CBR the encoder under-produces; and then
// nothing was taken away from it, the scene is what decides the throughput, and
// a negative verdict puts **the whole process** onto the road that rebuilds the
// encoder — the one that on AMD has already interrupted the capture.
//
// The log of a test machine shows it unintentionally: `probe: asked 1250, before
// 597, after 228`. There 1250 constrained nothing, and the probe concluded "the
// command takes" only because the scene had got easier in the meantime. With a
// still scene it would have concluded the opposite, and it would have been a
// permanent verdict taken on a measurement that was not measuring.
//
// **And the expected drop is `start - asked`**, not "half the start": they are
// the same number only when the encoder produces what it is asked for, which is
// precisely what the probe does not take for granted.
func probeVerdict(start, asked, after int) (took, judgeable bool) {
	if start <= 0 || asked <= 0 || after < 0 {
		return false, false
	}
	if asked >= start {
		return false, false
	}
	wanted := start - asked
	dropped := start - after
	return dropped*10 >= wanted*bitrateProbeDrop, true
}

// probeRestore says what to put back after the probe, and whether to put it
// back.
//
// **Nothing goes back if somebody else has commanded in the meantime.** The
// probe measures for eight seconds, and in that time a viewer may have
// connected: putting the preset back on a link the congestion control has just
// measured at 600 kbit/s means sending four times what it holds onto the
// network. And it does not correct itself, because the loop commands **only when
// its own state changes**: it would believe it was at 600 while the encoder sat
// at the preset, for as long as the network did not move.
//
// That somebody else has commanded shows in one thing only: the bitrate in force
// is no longer the one the probe asked for.
func probeRestore(inForce, asked, before int) (int, bool) {
	if inForce != asked {
		return 0, false
	}
	return before, true
}

// bitrateAsked records a bitrate command, so its effect can be weighed.
//
// **It does not clear the measurement in progress.** Commands arrive every two
// or three seconds and the verification wants four: clearing it, the window
// would never close. What is kept instead is the **highest request** in force
// during the window.
//
// **The direction matters, and it is easy to get backwards: keeping the lowest.**
// That looks like the strict choice and it is the one that condemns an obedient
// encoder, because in a descent the window's bytes were produced by the previous
// request:
//
//	18:46:16  asked 803
//	18:46:21  asked 523
//	18:46:22  window closed, produced 743  →  compared with 523  →  1.42
//
// 743 against 803 is obedience. The fault presents itself **by construction** in
// every session where the quality loop does its job, because that one commands
// monotonic descents. With the highest, a really deaf encoder is still caught
// (2400 against 803 makes 3.0): the only thing that changes is whoever is not
// deaf.
func (p *Pipeline) bitrateAsked(kbps int) {
	p.brMu.Lock()
	defer p.brMu.Unlock()
	p.brWant = kbps
	if p.brSince.IsZero() {
		p.brSince = time.Now()
		p.brBytes = 0
		p.brWantMax = kbps
		return
	}
	if kbps > p.brWantMax {
		p.brWantMax = kbps
	}
}

// bitrateSeen weighs the frames that come out and, if the command did not take,
// goes over to the road that reconfigures.
//
// It is keyframeSeen's twin, and for the same reason: of an encoder one verifies
// the effect, not the answer. `SetValue` answers S_OK even when it does not
// execute, and the only way of noticing is to weigh what comes out.
func (p *Pipeline) bitrateSeen(n int) {
	p.brMu.Lock()
	if p.brWant == 0 {
		p.brMu.Unlock()
		return
	}
	p.brBytes += int64(n)
	elapsed := time.Since(p.brSince)
	if elapsed < bitrateVerifyAfter {
		p.brMu.Unlock()
		return
	}

	want := p.brWantMax
	measured := int(float64(p.brBytes) * 8 / 1000 / elapsed.Seconds())
	// The window restarts at once and does not wait for a new command: the
	// question "is the encoder obeying?" makes sense at every instant, not only
	// after somebody has asked for something.
	p.brSince = time.Now()
	p.brBytes = 0
	p.brWantMax = p.brWant

	// **Deafness is a throughput that does not follow, and that is the only
	// criterion.**
	//
	// Two thresholds on the **ratio** between produced and asked, 1.4 and 1.15,
	// cannot do it. AMD obeys with a gain of 1.3-1.5 — halve the request and the
	// throughput halves — that is, it falls right on top of those thresholds: no
	// fixed value of the ratio can separate a gain error from deafness, and the
	// quality loop absorbs the first on its own.
	//
	// The right question is not "by how much does it overshoot" but "does it
	// follow?". A reference is kept and judgement passed **only after asking for
	// appreciably less**, which is the proof. It covers the blatant case too
	// without an extra rule. And if we never asked for less nobody is accused: a
	// zero means "I do not know".
	switch {
	case p.brWantRef == 0 || want > p.brWantRef:
		// First measurement, or we asked for more: the proof restarts.
		p.brWantRef, p.brMeasRef, p.brStreak = want, measured, 0
	case want >= p.brMeasRef:
		// **The request was not the constraint, so it proves nothing.**
		//
		// "I asked for less and it did not drop" only accuses if the less asked
		// for sits **below** what was already coming out. If the encoder was
		// producing 114 and 309 are granted to it, nothing was taken away: what
		// decides the throughput is the scene, and the scene is still.
		//
		// Observed on a test machine, which for this reason ended up on the
		// expensive road for the whole session: reference 775 asked / 114
		// produced, then 309 asked / 235 produced, and the watch read "the
		// output went up while I was asking for less". Those were two numbers
		// dictated by the scene, neither of them by the order. And thirty-four
		// seconds earlier the probe had established the opposite **with an
		// experiment**, that is by really halving and weighing: a controlled
		// proof is not overturned by an observation taken where the quantity is
		// not observable.
		//
		// The comparison is exact and has no threshold to tune: either the new
		// cap cuts what was coming out, or it is not a cap.
	case want*10 <= p.brWantRef*bitrateProofScale:
		if measured*10 >= p.brMeasRef*9 {
			p.brStreak++
		} else {
			p.brWantRef, p.brMeasRef, p.brStreak = want, measured, 0
		}
	}
	deaf := p.brStreak >= bitrateStubbornStreak
	// The reference goes into the log: without it, "asked 936, produced 1286"
	// does not say whether the encoder is deaf or merely calibrated wide, which
	// is the distinction this watch exists for.
	refWant, refMeas := p.brWantRef, p.brMeasRef

	first := !p.brHard
	p.brMu.Unlock()

	// The throughput follows: nothing to do, and above all no line of log. A log
	// that confirms every time everything is fine hides what did happen once.
	if !deaf {
		return
	}
	if !first {
		// We are already on the expensive road and even that is not enough.
		// There is no third remedy: it is said, once, so that the reason for a
		// stuttering picture stays written instead of being inferred. The final
		// judgement is given by the watch in rtc, which sees all the sessions.
		return
	}

	p.brMu.Lock()
	p.brHard = true
	p.brStreak = 0
	p.brMu.Unlock()
	p.cfg.Log.Warn("the encoder accepts the bitrate change and does not apply it",
		"asked_kbps", want, "produced_kbps", measured, "encoder", p.EncoderName(),
		"how", fmt.Sprintf("asked for %d%% less and the output did not drop, over %d checks",
			100-bitrateProofScale*10, bitrateStubbornStreak),
		"reference_kbps", refWant, "reference_produced_kbps", refMeas,
		"note", "from now on the bitrate is changed by rebuilding the encoder")

	// The dropped command is re-executed at once by the other road: without
	// this, the first lowering — that is the one arriving when the network is
	// already suffering — would stay without effect until the next.
	if err := p.ReconfigureBitrate(want); err != nil {
		p.cfg.Log.Warn("encoder reconfiguration failed", "error", err)
	}
}

// BitrateNeedsReconfig says whether on this machine the bitrate can only be
// changed by reconfiguring the encoder. False until that has been measured, and
// false again if that road has proved worse than the disease.
func (p *Pipeline) BitrateNeedsReconfig() bool {
	p.brMu.Lock()
	defer p.brMu.Unlock()
	return p.brHard && !p.reconfigBroken
}

// reconfigIsBroken declares that reconfiguring the encoder breaks the capture on
// this machine, and stops doing it for the rest of the process's life.
//
// **It is the half the rule was missing.** The project already had "try the road
// that interrupts nothing, weigh the frames, and go over to the other only if the
// first did not take"; what was missing was "and if the other does damage, go
// back". Measured on AMD: as soon as the watch went over to reconfiguration, the
// capture died with `ProcessOutput: HRESULT 0x8000FFFF` and restarted in a loop.
// A baby monitor that goes dark when the child moves is far worse than a bitrate
// that will not be commanded precisely.
//
// And it can afford to, because the loop on quality does not need a precise
// encoder: it measures the bytes coming out and asks for less until they drop.
// Measured in the same session: 496 asked, 701 produced, quantiser stuck at 24 —
// the command had little grip and the quality was the wanted one anyway.
func (p *Pipeline) reconfigIsBroken() bool {
	p.brMu.Lock()
	defer p.brMu.Unlock()
	if p.reconfigBroken || p.lastReconfig.IsZero() {
		return false
	}
	if time.Since(p.lastReconfig) > reconfigSuspect {
		return false
	}
	p.reconfigBroken = true
	return true
}

// reconfigSuspect is how soon after a reconfiguration a capture fault is put
// down to it.
//
// Generous on purpose: several frames can pass between the reconfiguration and
// the error, and attributing to it a fault that is not its own costs only giving
// up a road that is not the only one anyway.
const reconfigSuspect = 10 * time.Second

// AnalysisRate is the rate of the stream handed to the Level sink.
//
// **It is always AnalysisSampleRate**, and it stays a method for one reason: it
// is **zero until the audio is open**, which is what separates "there is nothing
// there yet" from "it arrives at 16 kHz". How it gets there is decided by
// `analysisPlan`.
func (p *Pipeline) AnalysisRate() int { return int(p.analysisRate.Load()) }

// ForceKeyFrame asks for the next frame to be a keyframe.
//
// It is the other reason this engine exists: when the browser reports a loss
// with a PLI, without this command the picture stays frozen until the next
// keyframe.
func (p *Pipeline) ForceKeyFrame() error {
	p.encMu.RLock()
	defer p.encMu.RUnlock()

	enc := p.control.Load()
	if enc == nil {
		return fmt.Errorf("no capture running")
	}
	if p.kfDeclaredBad.Load() {
		// Going on asking does not cost much, but it would hide the reason the
		// picture stays frozen: here it is said to whoever asked.
		return fmt.Errorf("this encoder does not answer keyframe requests")
	}
	if err := enc.ForceKeyFrame(); err != nil {
		return err
	}
	p.keyframeAsked()
	return nil
}

// keyframeGraceFrames is how many frames a request is given before it counts as
// ignored.
//
// Wide enough to cover the frames already in flight inside a pipelined encoder,
// narrow enough not to mistake the periodic keyframe for an answer, which with
// the GOP at two seconds is much further away.
const keyframeGraceFrames = 5

func (p *Pipeline) keyframeAsked() {
	p.kfMu.Lock()
	defer p.kfMu.Unlock()
	p.kfAsked++
	p.kfPendingAt = p.Stats.VideoFrames.Load()
}

// keyframeSeen records the outcome of a request by looking at the frames coming
// out.
//
// It has to be called for every Access Unit, keyframe or not: it is the frames
// going past without a keyframe that show the request fell on deaf ears.
func (p *Pipeline) keyframeSeen(frames int64, keyframe bool) {
	p.kfMu.Lock()
	defer p.kfMu.Unlock()

	if p.kfPendingAt == 0 {
		return
	}
	switch {
	case keyframe:
		p.kfServed++
		p.kfPendingAt = 0
	case frames-p.kfPendingAt > keyframeGraceFrames:
		p.kfPendingAt = 0
	default:
		return
	}

	// The verdict is given over several requests: a single dropped one may be a
	// capture restart or a lost frame.
	const enoughToJudge = 5
	if p.kfWarned || p.kfAsked < enoughToJudge {
		return
	}
	if p.kfServed == 0 {
		p.kfWarned = true
		p.kfDeclaredBad.Store(true)
		p.cfg.Log.Warn("the encoder ignores keyframe requests",
			"requests", p.kfAsked, "encoder", p.EncoderName(),
			"note", "anyone who loses the reference waits for the periodic keyframe")
	}
}

// QPAttributeCounts says how many readings of the attribute succeeded and how
// many did not, and by how far they differed: it separates "the encoder does not
// declare it" from "we cannot read it", which from outside are the same thing.
func (p *Pipeline) QPAttributeCounts() (readable, unreadable, totalDeviation, bias int64) {
	return p.qpAttrOK.Load(), p.qpAttrKO.Load(), p.qpAttrDeviation.Load(), p.qpAttrBias.Load()
}

// QPStreamRange gives the minimum and the maximum of the quantiser read from the
// stream. Equal means that encoder writes a constant slice quantiser and does
// its control per macroblock: the reading is correct and says nothing.
func (p *Pipeline) QPStreamRange() (min, max int) {
	return int(p.qpStreamMin.Load()), int(p.qpStreamMax.Load())
}

// QPSource says where the quantiser in use comes from: the sample attribute or
// the stream. It is not curiosity: the first is the average of the macroblocks,
// the second the slice quantiser, and on Quick Sync they differ by six and a
// half points — enough to make the same room look like two different scenes.
func (p *Pipeline) QPSource() string {
	if p.qpFromAttr.Load() {
		return "sample attribute"
	}
	return "stream"
}

// attrInventory is the list of attributes seen on an outgoing sample, with a
// note of whether the quantiser was among them.
type attrInventory struct {
	keys   []string
	withQP bool
}

// SampleAttributes lists the attributes the encoder puts on the frames it
// delivers, and whether the quantiser is among them.
//
// It answers the question left open when the quantiser is missing: is it the
// encoder that does not declare it, or are we asking for it wrongly? From
// outside the two are identical, and this project has already spent months with
// a GUID written wrong that never complained.
//
// **An empty inventory is not proof that the attribute is absent.** The
// enumeration answers zero even where the attribute reads perfectly well, so a
// read attempt that succeeds is evidence and an empty list may be either
// evidence or a defect of the enumeration — from outside the two cannot be told
// apart. Whether the quantiser is really there is answered by reading it.
func (p *Pipeline) SampleAttributes() (keys []string, withQP bool) {
	// **The photograph taken while the capture was running comes first.**
	// Questioning the encoder here is the same trap already paid for with the
	// quantiser counters: the report is printed after shutdown, and there
	// `p.control` is nil. The answer would have been "the encoder sets no
	// attributes" — that is, exactly the wrong conclusion we have already had to
	// retract once, and on this branch it would have been **the main evidence**
	// instead of a detail.
	if inv := p.sampleAttrs.Load(); inv != nil {
		return inv.keys, inv.withQP
	}
	p.encMu.RLock()
	defer p.encMu.RUnlock()
	enc := p.control.Load()
	if enc == nil {
		return nil, false
	}
	return enc.SampleAttributes()
}

// Run supervises the capture until the context is cancelled, restarting it with
// exponential backoff if it ends on its own.
//
// Audio and video have two separate lives, and neither can switch off the other.
// It costs one goroutine and is worth one fault per side:
//
//   - **a missing microphone must not switch off the video**: it happens on a
//     laptop with the lid closed, where the microphone array disappears while
//     the camera stays;
//   - **a camera that will not open must not switch off the audio**: it happens
//     on a machine whose webcam offers no usable format, and it used to leave
//     the monitor blind **and** mute, because the video restart cycle dragged
//     the microphone along with it and kept it closed for the whole backoff, up
//     to half a minute.
//
// The second half was found on hardware other than the development machine, and
// that is no accident: here the webcam always works.
func (p *Pipeline) Run(ctx context.Context, sinks Sinks) error {
	const (
		minBackoff = time.Second
		maxBackoff = 30 * time.Second
		// A session that lasted at least this long counts as healthy, so the
		// backoff restarts from zero: an isolated fault after hours of running
		// must not inherit the wait of a crash loop.
		healthyRun = 60 * time.Second
	)

	audioDone := make(chan struct{})
	guard.Go(p.cfg.Log, "the audio supervisor", func() {
		defer close(audioDone)
		p.superviseAudio(ctx, sinks)
	})
	defer func() {
		<-audioDone
		p.audioOK.Store(false)
	}()

	backoff := minBackoff
	// lastRetry is the failure already written, empty after an attempt in which
	// the camera came open. See retryLevel.
	var lastRetry string
	for {
		if err := ctx.Err(); err != nil {
			return nil
		}

		start := time.Now()
		err := p.runOnce(ctx, sinks)
		ran := time.Since(start)

		if ctx.Err() != nil {
			return nil // shutdown requested
		}

		p.Stats.Restarts.Add(1)
		// A camera chosen by whoever is watching: reopen at once, and say why.
		// **The line is worth writing** — this is the one closure of the video
		// capture that is not a fault, and without it whoever reads the log
		// finds an interruption with no cause, which sends them looking for a
		// fault where there was a command. It is the microphone's rule, where
		// two decided closures had to be told apart from each other; here there
		// is one, and it has to be told apart from the faults.
		if errors.Is(err, errCamSwitch) || errors.Is(err, errCamRecheck) {
			if errors.Is(err, errCamRecheck) {
				p.cfg.Log.Info("reopening on the camera that was chosen",
					"ran_for", ran.Round(time.Millisecond))
			} else {
				p.cfg.Log.Info("camera changed on request, reopening",
					"ran_for", ran.Round(time.Millisecond))
			}
			backoff = minBackoff
			// **A command is news**, so what fails after it is written in full:
			// a failure remembered from before the choice would otherwise
			// quieten the open of the camera somebody has just picked.
			lastRetry = ""
			p.camRetrying.Store(false)
			continue
		}
		// **What Windows refused is read off the attempt that just failed**, and
		// it is stored even when it is false: the flag says what the last try
		// met, so a permission granted back while the camera would not open for
		// some other reason does not leave the alert behind it.
		//
		// **The line comes out when the state changes, not on every retry.** A
		// refused camera is retried every thirty seconds for as long as the
		// monitor runs, and a sentence repeated all night is a log in which
		// nothing that happened once can be found — the rule the camera's own
		// open already follows a few hundred lines below.
		if denied := wincom.Denied(err); p.camDenied.Swap(denied) != denied && denied {
			p.cfg.Log.Error("the camera permission is off: Windows is refusing the "+
				"device, and there will be no picture until it is granted",
				"where", "Settings > Privacy & security > Camera",
				"note", "a desktop program also needs \"let desktop apps access your camera\"",
				"error", err)
		}
		// **What is new is written, and a repeat is not.** A camera that is
		// missing or refused is retried every thirty seconds for as long as the
		// monitor runs, and this line and the open's two warnings used to come
		// out every time: measured with the webcam disabled, three lines every
		// thirty seconds, ~360 an hour for ever, in a log of four rotating files
		// where the lines that matter get pushed out. It is the audio
		// supervisor's rule, "audio still unavailable", brought to the video.
		// **The wait is settled before it is written.** A healthy run resets it,
		// and doing that after the lines below made them announce the old wait:
		// measured, `waiting=30s` after a session of four minutes, with the next
		// attempt coming one second later.
		if ran >= healthyRun {
			backoff = minBackoff
		}
		opened := p.camOpenedAt.Load() >= start.Unix()
		lvl, sig := retryLevel(lastRetry, err, opened)
		lastRetry = sig
		p.camRetrying.Store(err != nil && !opened)
		if err != nil {
			p.cfg.Log.Log(ctx, lvl, "capture interrupted, restarting",
				"ran_for", ran.Round(time.Millisecond), "waiting", backoff, "error", err)
			// If the capture died right after a reconfiguration, that road is
			// abandoned: it is worse than the disease it cures. It is said once
			// only, because it is a final decision for this process and whoever
			// reads the log afterwards has to be able to find it.
			if p.reconfigIsBroken() {
				p.cfg.Log.Warn("rebuilding the encoder breaks capture on this machine: "+
					"it will not be done again",
					"encoder", p.EncoderName(),
					"note", "the bitrate stays the one commanded live, which barely bites here; "+
						"quality is held by the loop on the quantiser")
			}
		} else {
			p.cfg.Log.Warn("capture ended on its own, restarting",
				"ran_for", ran.Round(time.Millisecond), "waiting", backoff)
		}

		select {
		case <-time.After(backoff):
		case <-p.camWake:
			// A choice does not wait for the backoff: whoever made it has
			// already had their answer, and thirty seconds of a still picture
			// after a command reads as a command that did not work.
			backoff = minBackoff
		case <-ctx.Done():
			return nil
		}
		if backoff < maxBackoff {
			backoff *= 2
			if backoff > maxBackoff {
				backoff = maxBackoff
			}
		}
	}
}

// runOnce runs a single video capture session.
//
// The audio is not here, and its absence from this function is the point: the
// two branches must not be able to switch each other off. See Run.
func (p *Pipeline) runOnce(ctx context.Context, sinks Sinks) error {
	// **A panic in the capture is a fault of the capture, and this is where it
	// becomes one.** Under this call are COM, Media Foundation and an encoder
	// rebuilt while the monitor runs — that is, the code most likely to
	// dereference something that has just been released, and the place where
	// this project has already paid for exactly that (see the encoder held
	// across a replacement). Left as a panic it ends the process, so an
	// accessory's bug switches the camera off for the night with nobody there
	// to start it again.
	//
	// **No restart is invented for it.** The supervisor above already knows how
	// to come back from a fault — the backoff, the healthy-run reset, the line
	// saying what happened — and every one of those applies unchanged to an
	// error that happens to have been a panic.
	//
	// It sits here and not around Run's loop for the same reason the camera's
	// decision does: a catch around the loop is caught once, and then the loop
	// is gone.
	err := guard.Run(p.cfg.Log, "the video capture", func() error {
		return p.runVideo(ctx, sinks)
	})
	// **A close we decided on is not a fault and must not look like one**: the
	// supervisor recognises it by this error and reopens at once, with no wait
	// and no line accusing the camera.
	//
	// It is decided here and not inside runVideo because that function leaves by
	// several roads — a cancelled context comes back as a plain nil from two of
	// them — and a decision taken at each exit is a decision one of them will
	// end up not taking.
	if p.camReopen.Load() {
		// **Two closures we decide on, and they stay two errors**, as the
		// microphone's do: they end in the same branch of the supervisor and the
		// log line is not the same, because "somebody chose" and "the camera
		// came back" send whoever reads to two different places.
		if p.camRecheck.Swap(false) {
			return errCamRecheck
		}
		return errCamSwitch
	}
	return err
}

// simulatePanicAfter is how far into a session the deliberate panic falls.
//
// A hundred video frames are some three seconds at thirty a second, and a
// hundred audio packets two: long enough that the camera is open, the encoder
// is built and the first keyframe has gone out, which is the state whose
// unwinding is worth watching.
const simulatePanicAfter = 100

// simulatePanic raises the panic asked for by -simulate-panic, once.
//
// **Once per process and not once per session**: the supervisor restarts the
// capture, and a trigger that fired every time would give a crash loop instead
// of the one thing to be seen — a panic, a restart, and then a monitor that
// goes on working.
func (p *Pipeline) simulatePanic(where string, count int64) {
	if p.cfg.PanicIn != where || count != simulatePanicAfter {
		return
	}
	if !p.panicSimulated.CompareAndSwap(false, true) {
		return
	}
	panic("-simulate-panic " + where + ": a deliberate panic")
}

// errCamRecheck signals that the capture was closed because the chosen camera
// came back, so that the supervisor neither reports a fault nor waits.
var errCamRecheck = errors.New("the chosen camera is connected again")

// errCamSwitch signals that the capture was closed to open another camera, so
// that the supervisor does not report a fault and does not wait.
//
// **It is a sentinel and not a boolean** for the same reason as the
// microphone's: it travels with the error the loop already reads, and a second
// channel of "why did it end" would have to be checked by whoever added the
// third case.
var errCamSwitch = errors.New("camera changed on request")

// micRecheckInterval is how often the unprocessed signal is tried for again
// after falling back on the shared path.
//
// The fallback may depend on another application holding the microphone, and
// those come to an end: a video call lasts an hour, not a night. Trying again
// costs the fraction of a second of a reopen, and it is only paid while we are
// already on the worse path — when the good road is open this timer does not
// exist and the audio is never interrupted.
const micRecheckInterval = 2 * time.Minute

// micRecheckWanted says whether the audio path just opened is a fallback to be
// re-examined.
//
// **There are three fallbacks, and it is easy for the timer to watch one.** Raw
// mode not obtained was the only case foreseen; since an absent endpoint makes
// the default open instead of failing, there is a second one — the capture is on
// a microphone nobody asked for — and without this it would **never** be
// re-examined: on a machine that does obtain raw mode, that is where everything
// is fine, the timer would not arm at all and whoever plugged the USB stick back
// in would stay on the lid microphone until the program restarts.
//
// **The third is the mute, and it is the one that makes a button lie.** A muted
// endpoint delivers silence and the gain is held at zero for the life of the
// open, so on a machine that obtains raw mode with nothing to fall back to, the
// timer was not armed at all: whoever read *microphone: muted in Windows*,
// pressed the command under it and unmuted, got a room that stayed silent and a
// fault that stayed red until the program restarted. **The remedy the panel
// offers has to reach the monitor**, and the road already existed — it is the
// same worse path, re-examined on the same cadence, disarming itself the moment
// the endpoint opens unmuted.
//
// **It lives in a function because it was a condition inside three hundred lines
// of opening**, where it can only be tested by running WASAPI. Here the answers
// read as a table.
//
// A wanted ID that is **empty** is never a fallback: it means "follow the role
// Windows calls default", and whatever the role opens is by definition the right
// choice. The test tone does not go through WASAPI, so it has neither a mode to
// obtain nor an endpoint to compare.
func micRecheckWanted(rawObtained, muted bool, wantedID, openedID string, testTone bool) bool {
	if testTone {
		return false
	}
	fellBack := wantedID != "" && openedID != wantedID
	return !rawObtained || fellBack || muted
}

// micRecheckGrace is how long a planned reopen may last without anyone calling
// it an absence.
//
// **The reopen is a precaution, and a precaution must not be able to sound like
// an alarm.** The recheck arms itself on every open that does not obtain raw
// mode — exclusive included, which is where a machine whose APO filters the
// microphone ends up — that is, every two minutes all night long. With the
// status read every second, a microphone closed for a fraction of a second is
// enough to make the `mic-missing` alert appear and go: banner, chime and two
// lines, hundreds of times. **An alarm that sounds every night for no reason is
// one nobody reads any more**, and this is the only alarm the program has.
//
// Two seconds is far more than needed — a reopen costs a fraction of a second —
// and far less than would be needed to hide a fault: if the device does not come
// back, the absence is declared on the next turn. The grace **expires by
// itself** rather than being switched off by somebody, so an open that wedges
// inside the driver does not keep it on for ever.
const micRecheckGrace = 2 * time.Second

// errMicRecheck signals that the capture was closed by us to try a better path,
// not because something went wrong.
var errMicRecheck = errors.New("microphone path re-examination")

// errMicSwitch signals that the capture was closed to open another microphone,
// chosen by whoever is watching.
//
// It is an error distinct from errMicRecheck and not the same one, even though
// the supervisor treats them alike: the log has to say **why** the audio was
// interrupted for an instant, and "re-examination" in place of "another
// microphone was chosen" would send someone looking for a fault where there was
// a command.
var errMicSwitch = errors.New("microphone changed on request")

// superviseAudio retries opening the microphone without being able to interrupt
// the video.
//
// The fault is reported once only: repeating it every few seconds would fill the
// log with identical lines and hide everything else. The audio's return, on the
// other hand, is always announced, because that is news.
func (p *Pipeline) superviseAudio(ctx context.Context, sinks Sinks) {
	const maxBackoff = 30 * time.Second
	backoff := time.Second
	var lastErr string

	for ctx.Err() == nil {
		// The microphone's half of what runOnce does for the camera, and it is
		// needed on its own: the two lives are separate by construction, and a
		// panic is the one fault that would have joined them back together.
		err := guard.Run(p.cfg.Log, "the audio capture", func() error {
			return p.runAudio(ctx, sinks)
		})
		if ctx.Err() != nil {
			return
		}

		// **The flag is cleared by the supervisor, not by `runAudio`.** With a
		// second writer, whichever runs first wins and the
		// `CompareAndSwap(true, false)` here always finds `false`: the line
		// "audio interrupted, retrying" never comes out and `lastErr` never
		// clears — so after a recovery, a second interruption carrying the same
		// message goes to `Debug`, which in the binary that ships means nowhere.
		wasActive := p.audioOK.Swap(false)

		// Planned recheck: it reopens at once and in silence. Coming through
		// here by way of the errors would announce an audio interruption every
		// two minutes, that is, it would turn a precaution into an alarm.
		//
		// **And the silence has to reach whoever is watching too.**
		// `AudioActive` feeds `MicrophoneActive`, which feeds the `mic-missing`
		// alert: a status round falling inside the reopen produces a banner, a
		// chime and two transitions in the log, every two minutes all night long
		// on a machine that does not obtain raw mode. The grace covers the
		// reopen and **expires by itself**: if the device does not come back
		// within its time, the absence is declared as always.
		//
		// **Choosing another microphone comes through here along with the
		// recheck**, and for the same reason: the close was our own decision, so
		// it is not a fault and must not sound like one. What changes is on the
		// other side, in the open, which does **announce** a choice.
		if errors.Is(err, errMicRecheck) || errors.Is(err, errMicSwitch) {
			p.micGraceUntil.Store(time.Now().Add(micRecheckGrace).UnixNano())
			backoff = time.Second
			continue
		}
		// The microphone's half of what Run reads for the camera, and by the
		// same two rules: the flag says what the last attempt met, and the line
		// comes out on the change rather than on every retry. See camDenied.
		if denied := wincom.Denied(err); p.micDenied.Swap(denied) != denied && denied {
			p.cfg.Log.Error("the microphone permission is off: Windows is refusing "+
				"the device, and the room cannot be heard until it is granted",
				"where", "Settings > Privacy & security > Microphone",
				"note", "a desktop program also needs \"let desktop apps access your microphone\"",
				"error", err)
		}
		if wasActive {
			p.cfg.Log.Warn("audio interrupted, retrying", "error", err)
			lastErr = ""
		}
		if err != nil {
			if msg := err.Error(); msg != lastErr {
				lastErr = msg
				p.cfg.Log.Error("audio unavailable, carrying on without it", "error", err)
			} else {
				p.cfg.Log.Debug("audio still unavailable", "error", err)
			}
		}

		select {
		case <-time.After(backoff):
		case <-p.micWake:
			// A choice does not wait for the backoff: whoever made it has
			// already had their answer, and thirty seconds of silence after a
			// command reads as a command that did not work.
			backoff = time.Second
		case <-ctx.Done():
			return
		}
		if backoff < maxBackoff {
			backoff *= 2
			if backoff > maxBackoff {
				backoff = maxBackoff
			}
		}
	}
}

// ---------- video ----------

// camRecheckInterval is how often the chosen camera is looked for while another
// one is being captured.
//
// **It is the microphone's number and the microphone's reason.** There a
// fallback can end because another application let the device go; here it ends
// because somebody plugs the webcam back in, and neither is announced to us.
// Without a look of some kind the monitor would show the wrong room until the
// next restart — and worse, the `camera-other` alert would go on asserting that
// the chosen camera is not connected **after it is**, which is an alarm that
// rings all night for no reason, in a program that has one alarm.
//
// The check is cheap and the cure is not: enumerating costs a round through
// Media Foundation, reopening costs about a second and a third of picture. So
// the reopen happens **only** when the chosen camera is really back, which is
// what makes a two-minute cadence free in the normal case.
const camRecheckInterval = 2 * time.Minute

// watchForTheChosenCamera reopens the capture when the camera that was chosen
// comes back.
//
// **It only runs while we are on another camera**, which is the microphone's
// "only if it is the worse path": with the chosen camera open there is nothing
// to look for, so there is no enumeration and no interruption to risk.
//
// The close goes through the same road as a choice — mark, then cancel — and is
// distinguished from it by `camRecheck`, so the log says which of the two it
// was. It does **not** set `camChosen`: nobody chose anything, and that flag
// exists to make the open announce a choice that went nowhere.
//
// **The enumeration and the cadence are handed in**, because a function that
// interrogates the system can only be run and not tested — and the branch that
// matters here is the one that decides *not* to interrupt anything.
func (p *Pipeline) watchForTheChosenCamera(ctx context.Context, list func() ([]devices.Device, error), every time.Duration) {
	t := time.NewTicker(every)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			wanted := p.camWantedLink()
			if wanted == "" || !p.camFellBack.Load() {
				continue
			}
			all, err := list()
			if err != nil {
				// At Debug on purpose: this runs every two minutes for as long
				// as the fallback lasts, and a warning repeated all night is the
				// volume of the log handed to whoever unplugged a webcam.
				p.cfg.Log.Debug("cannot look for the chosen camera", "error", err)
				continue
			}
			cam, fellBack, err := devices.Pick(all, wanted)
			if err != nil || fellBack {
				continue
			}
			p.cfg.Log.Info("the camera that was chosen is connected again",
				"camera", cam.Name)
			p.camRecheck.Store(true)
			p.camReopen.Store(true)
			if cancel, ok := p.camCancel.Load().(context.CancelFunc); ok {
				cancel()
			}
			return
		}
	}
}

// slowRebuild is how long the encoder's replacement may take before the log
// says so.
//
// **It is a threshold above the measurements and not near them**: on this
// machine the pair build-and-close is 107 ms to 330 ms across every size change
// in a day's log, so two seconds is six times the worst of them. A threshold
// close to the normal value would write a line on an ordinary night, and a line
// that appears on ordinary nights is one nobody reads on the night it means
// something.
const slowRebuild = 2 * time.Second

// rebuildWasSlow weighs the three intervals a replacement can spend inside
// Media Foundation against that threshold.
//
// **It takes the sum and not the build alone**, which is the whole of it: the
// frame loop is stopped for every one of them, a refused cadence included, so
// an attempt turned down after 1.8 s followed by a build of 1.0 and a close of
// 0.1 is a 2.9 s stall — and weighing the 1.0 on its own left it under the
// threshold and the log silent about exactly the case the timing was added for.
// The fields stay three because they accuse different people; the threshold is
// about the picture, which cannot tell them apart.
func rebuildWasSlow(refused, built, closed time.Duration) bool {
	return refused+built+closed >= slowRebuild
}

// resolveCamera decides which camera to open, and reports whether the chosen
// one was there.
//
// **The question is asked at every open**, which is what makes the answer
// follow the camera instead of describing the one that was plugged in at
// start-up. It is the same move already made for the size, one step earlier:
// there the wrong answer was an upscale in silence, here it is a monitor that
// refuses to switch on because a webcam was unplugged.
//
// **A failure in the enumeration stops nothing**: the chosen link is handed to
// Media Foundation as it stands, which is exactly how it was opened before this
// function existed. Worse than opening a camera badly there is only not opening
// it — and the same holds for the enumeration answering nothing usable, where
// the open's own error says more than a guess made here.
//
// Whoever calls it is left with two things to do and they are not the same one:
// **open** what comes back, and **declare** the fallback. See devices.Pick.
func resolveCamera(want string, list func() ([]devices.Device, error), log *slog.Logger) (cam devices.Device, fellBack bool) {
	all, err := list()
	if err != nil {
		log.Warn("cannot list the cameras, opening the chosen one as it is", "error", err)
		return devices.New("", want), false
	}
	cam, fellBack, err = devices.Pick(all, want)
	if err != nil {
		log.Warn("no usable webcam in the list, asking for the chosen one anyway",
			"error", err)
		return devices.New("", want), false
	}
	return cam, fellBack
}

// runVideo holds camera and encoder together on a single thread.
//
// COM objects belong to the thread that created them and Go goroutines migrate
// between threads: without LockOSThread the calls would end up where
// CoInitializeEx was never run.
func (p *Pipeline) runVideo(ctx context.Context, sinks Sinks) error {
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()

	// **The flag is given back before anything that can fail**, which is
	// where the microphone's twin already clears its own: `runAudio` does it
	// on its sixth line, with nothing above it that can return.
	//
	// Here it used to be cleared thirty lines down, after `wincom.Init` and
	// `mf.Startup` — two calls that return an error. Left set through one of
	// those, `runOnce` reads it and reports `errCamSwitch`, and the
	// supervisor's branch for a closure we decided on **does not wait at
	// all**: it logs a line and goes straight round again. A failure that
	// repeats — Media Foundation refusing to start — therefore becomes a loop
	// at full speed writing `camera changed on request, reopening` as fast as
	// the machine can, instead of a fault reported once and retried with the
	// backoff that exists for it.
	//
	// Clearing it here loses nothing: **the wish lives in `camWanted`**, which
	// this does not touch, so the next open honours the choice either way.
	// What this flag carries is only whether the closure just past was ours,
	// and about a session that never started the honest answer is no.
	p.camReopen.Store(false)

	// Multithreaded and not apartment, unlike the audio capture: asynchronous
	// transforms deliver their events from an internal work queue, and in an STA
	// apartment that queue wants a message loop which is not here. The result
	// would not be an error but a silent block.
	//
	// **The outcome is read by `wincom`.** Treating any non-zero HRESULT as a
	// fault gets `S_FALSE` wrong, which means succeeded: it would exit from here
	// before opening the camera, that is a blind monitor with a diagnosis that
	// accuses the webcam.
	//
	// **`Required` is the other half.** Finding somebody else's apartment here
	// means finding ourselves in STA, which is precisely the case the paragraph
	// above forbids: not an error, a silent block. A tolerant reading would let
	// it carry on. A refusal here costs a line of log and a restart with
	// backoff, which the video loop already has.
	release, err := wincom.Init(wincom.MTA, wincom.Required)
	if err != nil {
		return err
	}
	defer release()

	if err := mf.Startup(); err != nil {
		return err
	}
	defer mf.Shutdown()

	// The Direct3D device serves the encoder, not the camera: the frames arrive
	// in system memory because motion detection needs to read their luma plane,
	// and bringing a texture back down from the GPU costs far more than the copy
	// the encoder makes on its own.
	var dev *mf.D3DDevice
	if !p.cfg.NoDirect3D {
		var err error
		if dev, err = mf.NewD3DDevice(); err != nil {
			p.cfg.Log.Warn("no Direct3D device: only software encoders remain", "error", err)
			dev = nil
		} else {
			defer dev.Release()
		}
	}

	// **The preset is lowered to what this camera really has, at every open.**
	// With the advanced video processing on, the Source Reader does not refuse a
	// size larger than the camera declares: it puts a scaler in the middle and
	// enlarges, so nothing fails and the log reports the preset while the picture
	// carries not one extra detail — measured on a C210, which declares 640x480
	// and delivered no frame in twenty minutes at 720p.
	//
	// It is asked here and not once in main because that is what makes the answer
	// follow the camera: with the question asked at start-up, a camera changed
	// while the monitor runs would be opened at the previous one's size.
	// **Which camera is opened is decided here too**, and the reason is the
	// same as the size's: asked once in main, the answer describes the webcam
	// that was there at start-up. A camera chosen and then unplugged would stop
	// the monitor from starting at all, which for a baby monitor is the worst
	// of the outcomes available.
	//
	// The link handed on is the **enumerated** one and not the one from the
	// file: Windows gives the same symbolic link back in different cases
	// depending on who is asked, and findDevice compares it byte for byte.
	// Matching case-insensitively once here removes a whole class of "camera
	// %q not found" on a link that is there.
	// **The capture's context derives from the general one**, because we can
	// close it too: choosing another camera is applied by closing this one and
	// letting the supervisor reopen. The parameter is reassigned deliberately —
	// every use below wants the derived one, and a second name would be a second
	// thing to remember at each of them — the probe and the frame loop
	// included, both of which have to stop when the camera changes.
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	// The close can be asked for by whoever changes camera, who is not here: the
	// way to reach it is this pointer. The flag is cleared **before** opening,
	// otherwise a choice arriving while we were closed would be consumed by this
	// open instead of by the next one.
	p.camCancel.Store(cancel)
	wanted := p.camWantedLink()

	// **From here to the open the device is being touched**, and under a
	// package that is where Windows stops everything to ask. Enumerating,
	// reading the formats and opening all go through the same consent, so the
	// flag covers the three of them and not only the last.
	p.camOpening.Store(true)
	// **And cleared again on the way out, because this stretch can panic.**
	// `runVideo` runs under `guard.Run` precisely because enumerating, reading
	// the formats and opening go into COM and Media Foundation; a recovered
	// panic between here and the store below would leave the flag standing, and
	// a flag standing is `capture-stopped` waived for ever. The straight-line
	// clear is what marks the right instant; this one is what makes it true on
	// every road out.
	defer p.camOpening.Store(false)
	// **On a retry after a failed open, the open's warnings are a repeat.**
	// They are written before the attempt's outcome is known, so the only thing
	// that can tell a repeat is the attempt before: when it failed without the
	// camera coming open, these go to Debug, and the error line in Run carries
	// whatever is new. See retryLevel.
	//
	// **And they are held, because on the attempt that finally opens they were
	// news.** Written at Debug they would be lost exactly then: a camera back
	// whose formats cannot be read would be asked for the preset with nothing
	// saying why. So what the open quietened is written again, at its own
	// level, the moment the camera comes open.
	openLog := p.cfg.Log
	var held []slog.Record
	if p.camRetrying.Load() {
		openLog = slog.New(demoted{Handler: p.cfg.Log.Handler(), held: &held})
	}
	cam, fellBack := resolveCamera(wanted, devices.ListCameras, openLog)
	link := cam.Link()

	wantW, wantH, wantF := cameraSize(p.cfg.Width, p.cfg.Height, p.cfg.FPS,
		p.cfg.KeepRequestedSize,
		func(w, h, fps int) (int, int, int, bool, error) {
			return mf.PickCameraSize(link, w, h, fps)
		}, openLog)
	p.setStartSize(wantW, wantH, wantF)
	// **A size the previous camera's scale asked for is thrown away.**
	//
	// `wantFormat` is a request deposited by the governor and consumed by the
	// loop below, and it outlives the session that was meant to consume it. That
	// was harmless while a restart always came back to the same starting size —
	// every step of the old scale is a step of the identical new one — and it
	// stopped being harmless the moment the camera can change: from a small
	// webcam to a large one, a pending step of the small scale is smaller than
	// anything the rebuilt scale knows, `applyFormat`'s cap does not catch it
	// because the cap only looks upwards, and `resync` deliberately invents no
	// size that is not a step. What is left is a governor that believes it is at
	// full size while a shrunken picture goes out.
	//
	// Dropping a request that was legitimate costs one turn: `resync` realigns
	// the governor to what is really being sent, which is what it is for.
	p.wantFormat.Store(nil)

	reader, err := mf.OpenCamera(link, wantW, wantH, wantF, nil)
	// **Cleared on both roads, and before the error is wrapped**: an attempt
	// that came back with a refusal is answered, not pending, and it is the
	// denied flag's business from here on.
	p.camOpening.Store(false)
	if err != nil {
		return fmt.Errorf("camera open: %w", err)
	}
	// **The instant the attempt succeeded**, which is where the start-up grace
	// is counted from. Written after the error is ruled out: a refusal is not
	// an open, and anchoring the grace to it would hand a fresh thirty seconds
	// of silence to every retry.
	p.camOpenedAt.Store(time.Now().Unix())
	replay(ctx, p.cfg.Log.Handler(), held)
	// **The refusal is cleared here and not when the session ends**, which is
	// the whole difference between a state and a latch: a permission granted
	// back at two in the morning must take the banner off the phone at the next
	// open, not at the next fault. Whoever set it is the supervisor, on the
	// attempt that failed — see Run.
	p.camDenied.Store(false)
	defer reader.Release()

	// **The pinning is asked for and its outcome is written, never assumed.**
	// A failure here changes nothing and must not: the capture that follows is
	// the one this program has always done, and the experiment is about whether
	// the scale's steps arrive afterwards — which is read from the frames, not
	// from this line.
	if p.cfg.PinNativeFormat {
		if err := reader.PinNativeFormat(); err != nil {
			p.cfg.Log.Warn("the camera's native format was not pinned", "error", err)
		} else {
			p.cfg.Log.Info("the camera's native format is pinned",
				"why", "a change of size must go through a converter, not through the device")
		}
	}

	// **What is open is published, and the fallback is declared.**
	//
	// The line comes out when it **changes** and not at every open, which is the
	// microphone's rule and matters more here: a camera that will not open at
	// all is retried every thirty seconds, and a state written once every
	// thirty seconds is a log that hides what happened once. Whoever is watching
	// gets it from the alert, which is where a thing that is wrong now belongs.
	prev := p.Camera()
	p.camOpen.Store(&Camera{Link: link, Name: cam.Name})
	p.camFellBack.Store(fellBack)
	// **A choice by whoever is watching is announced even when nothing
	// changed**, and that is the case the "it changed" guard would silence: a
	// camera chosen and not connected falls back on the one already open, so
	// the picture is identical and the choice went nowhere. It is the one
	// moment when saying so is any use, because another one can still be
	// chosen.
	chosen := p.camChosen.Swap(false)
	if fellBack && (chosen || prev.Link != link) {
		p.cfg.Log.Warn("the chosen camera is not connected: opened another one",
			"chosen", wanted, "camera", cam.Name)
	}
	// **And from here on the chosen camera coming back is watched for.** It is
	// started at every open and dies with the session, because what it is
	// watching is a property of this open: which camera we ended up on.
	guard.Go(p.cfg.Log, "the watch for the chosen camera", func() { p.watchForTheChosenCamera(ctx, devices.ListCameras, camRecheckInterval) })
	// **A choice arriving while we were opening is not lost.** The command comes
	// from another goroutine and its close may have preceded the pointer that
	// makes it reachable: reading the wish once only, it would sit written and
	// applied by nobody.
	if p.camWantedLink() != wanted {
		p.camReopen.Store(true)
		cancel()
	}

	w, h, num, den, subtype, err := reader.CurrentFormat()
	if err != nil {
		return err
	}
	fps := wantF
	if den > 0 && num > 0 {
		fps = (num + den/2) / den
	}
	if subtype != "NV12" {
		return fmt.Errorf("the camera delivers %s instead of NV12", subtype)
	}
	p.cfg.Log.Info("camera opened", "format", fmt.Sprintf("%s %dx%d@%d", subtype, w, h, fps))

	// The encoder is built with a function rather than once only because the
	// resolution scale rebuilds it: reconfiguring the running one does not take,
	// while at start-up the size is always exact.
	// The starting mode is the configured one, normally CBR: that is the safe
	// one, with the limit guaranteed by construction.
	if p.curMode.Load() == nil {
		mode := p.cfg.RateControl
		if mode == "" {
			mode = mf.RateCBR
		}
		p.curMode.Store(string(mode))
	}

	// The distance between keyframes is kept in **seconds**, not in frames: the
	// GOP is recomputed from the cadence in force, otherwise on the way down to
	// one frame per second a GOP of sixty frames would become one keyframe a
	// minute, and whoever opened the page at that moment would wait a minute to
	// see anything.
	gopFor := func(f int) int {
		if p.cfg.GOPSeconds <= 0 || f <= 0 {
			return 0
		}
		if g := p.cfg.GOPSeconds * f; g > 0 {
			return g
		}
		return 1
	}

	newEncoder := func(w, h, f, kbps int) (*mf.VideoEncoder, error) {
		return mf.NewVideoEncoder(mf.VideoEncoderConfig{
			Width: w, Height: h, FPS: f,
			BitrateKbps: kbps,
			MinQP:       int(p.curMinQP.Load()),
			MaxQP:       p.cfg.MaxQP,
			Profile:     mf.H264ProfileBase,
			NameFilter:  p.cfg.PreferEncoder,
			Device:      dev,
			GOPFrames:   gopFor(f),
			RateControl: p.RateMode(),
			Quality:     p.cfg.Quality,
		})
	}

	enc, err := newEncoder(w, h, fps, p.cfg.BitrateKbps)
	if err != nil {
		return fmt.Errorf("no usable H.264 encoder: %w", err)
	}
	// **The bitrate the encoder is born with is a request like any other, and it
	// belongs in the watch's reference.**
	//
	// Without this line the reference forms on the first window observed, which
	// already carries a reduced request: the quality loop comes down from the
	// preset to less than half **in one go**, so the jump that constitutes the
	// proof — "I asked for far less and nothing changed" — falls before there is
	// anything to compare it with.
	//
	// The defect does not show on an encoder that obeys, nor on one that comes
	// down in steps: there the requests go on dropping and the proof arrives
	// anyway. It showed on Quick Sync, where the loop settles at once around a
	// value and stays there — ~1100 asked and ~2400 produced for two whole
	// minutes, with the watch silent.
	p.bitrateAsked(p.cfg.BitrateKbps)

	// The probe: once per process, and in a goroutine because it waits several
	// seconds and here is the loop that delivers the frames.
	p.brMu.Lock()
	probe := !p.brProbed
	p.brProbed = true
	p.brMu.Unlock()
	if probe {
		guard.Go(p.cfg.Log, "the bitrate probe", func() { p.probeBitrate(ctx, p.cfg.BitrateKbps) })
	}
	// The way out goes through the lock too, and for the same reason: the
	// capture also closes when it is restarted after a fault, and there the
	// congestion control is still commanding. First the encoder is taken out of
	// the way, then it is closed.
	//
	// **Except when the process is ending, where it is not closed at all.**
	//
	// On one machine the NVIDIA encoder's release takes the process out with a
	// 0xC0000005 — measured, `pat-capture` 6 deaths of 14 against 0 of 14 with
	// the Microsoft software encoder, and **the monitor 4 of 20 orderly
	// shutdowns**, none of which ever wrote `shutdown complete`. It never
	// happens mid-session: every restart through this same defer is clean, and
	// what is different at the end is that the whole process is unwinding around
	// the release.
	//
	// So at the end it is left alone. **The precedent is in this repository**,
	// one layer down, and it is written in the same words: the async callback's
	// pin is deliberately leaked, because "freeing the object while Media
	// Foundation holds it means a process crash instead of an error". Here the
	// OS is about to reclaim every handle this process owns, so the release buys
	// nothing and costs one night in five.
	//
	// **Only the last one is skipped.** A restart still closes, because there
	// the process goes on and an encoder per restart really would accumulate:
	// the condition is the context, which is cancelled only by a shutdown that
	// somebody asked for.
	defer func() {
		p.encMu.Lock()
		last := enc
		p.control.Store(nil)
		p.encMu.Unlock()
		if ctx.Err() != nil {
			p.cfg.Log.Debug("the encoder is left to the operating system",
				"why", "the process is ending, and releasing it has taken it out before")
			return
		}
		last.Close()
	}()

	p.encoderName.Store(enc.Name)
	p.videoWidth.Store(int64(w))
	p.videoHeight.Store(int64(h))
	p.hardware.Store(dev != nil && enc.UsesDevice())
	p.control.Store(enc)
	p.cfg.Log.Info("capture started",
		"encoder", enc.Name,
		"video", fmt.Sprintf("%dx%d@%d", w, h, fps),
		"bitrate", p.cfg.BitrateKbps)
	if !enc.CodecSettingsAccepted() {
		p.cfg.Log.Warn("some encoder settings were not accepted", "detail", enc.CodecNotes())
	}

	// The assembler keeps state between chunks (NALs straddling buffers), so it
	// lives for the whole session. The declared level is brought back to the
	// minimum really needed: encoders overstate it and browsers refuse a level
	// higher than the one they announce.
	var asm media.AUAssembler
	asm.TargetLevelIDC = media.MinLevelIDC(w, h, fps, p.cfg.BitrateKbps)

	motion := newMotionScaler(w, h)
	// The detection does not need every frame: one every so often is enough, and
	// subsampling here costs less than dropping downstream.
	//
	// **It is counted in time and not in frames, and that is a repair.** It
	// used to be `frameNo % (fps / MotionFPS)`, a divisor of the cadence the
	// camera **declares** — and this repository has a whole file about the gap
	// between that number and the one the camera delivers: in the dark the
	// automatic exposure takes it to 9.8-20 fps while the format still says
	// 30. A divisor of 6 applied to 9.8 frames a second is **1.6 analyses a
	// second**, against the five `internal/detect` opens by saying it
	// receives.
	//
	// So the contract was kept in daylight and broken at night, which is when
	// this program works — and it is the same night in which the exposure
	// hunts, that is, the condition the detector's global-shift subtraction
	// exists for. The gate below is the same one the encoder's cadence uses:
	// it delivers five a second whatever the camera does, and when the camera
	// itself falls below five it passes everything, which is the whole of what
	// there is.
	motionGate := newMotionGate(fps)

	// The quantiser reader lives as long as the capture: it keeps SPS and PPS
	// between one frame and the next and re-examines them when they change,
	// which happens on every rebuild of the encoder — that is, at every step of
	// the scale.
	qpFromStream := &media.QPReader{}

	take := func() error {
		for {
			got, err := enc.Take(func(b []byte) error {
				for _, au := range asm.Write(b) {
					frames := p.Stats.VideoFrames.Add(1)
					p.simulatePanic("video", frames)
					if au.Keyframe {
						p.Stats.Keyframes.Add(1)
					}
					p.keyframeSeen(frames, au.Keyframe)
					p.bitrateSeen(len(au.Data))
					p.Stats.VideoBytes.Add(int64(len(au.Data)))

					// **The quantiser is read from the stream where the stream
					// says it, and from the attribute where it does not.**
					//
					// The stream is the portable road — it is what the decoder
					// reads, and on AMD, which does not expose the attribute, it
					// is the only one there is. But on Quick Sync the **slice**
					// quantiser is a constant: 26.0 exactly over 696 frames
					// while the attribute moved between 26 and 42, because that
					// chip does its control per macroblock.
					//
					// It is not our parser, and the proof is not a re-reading:
					// the same parser and the same machine with the software
					// encoder, where the stream reads a real distribution and
					// agrees with the attribute to a bias of **+0.0**. A broken
					// parser does not produce zero bias on one encoder and +6.5
					// on the other.
					//
					// So the attribute is preferred **where there is one**: not
					// because the encoder is more credible than the stream —
					// this project has learnt the opposite — but because where
					// the stream is a constant the attribute is the only one of
					// the two that measures anything, and where both measure
					// they agree.
					//
					// The safety net for encoders never seen is `qpMeasured`,
					// just below: a reading enters only after having changed
					// value at least once. A 26 nailed down with a target of 30
					// would send the loop to the floor for ever.
					// **The reserve, offered every frame and taken almost never.**
					// It is what the encoder declares, and the reader refuses it
					// as soon as the stream has spoken — which on every encoder
					// seen here is the first keyframe. Offering it from here
					// rather than at the three places that build an encoder is
					// what makes it impossible to forget, and it is also the
					// only place where the header exists: the encoder has none
					// until it has produced a frame.
					qpFromStream.Seed(enc.SequenceHeader())
					qp, streamOK := qpFromStream.Feed(au.Data)
					// **The counts live on the pipeline, not on the encoder.**
					// The report is printed after the capture has stopped, and
					// at shutdown the encoder is taken out of the way: reading
					// them from there gives zero and zero, which looks like "the
					// attribute is not there" and is "there is nobody left to
					// ask".
					declared, attrOK := enc.LastQP()
					// The inventory is photographed here, while there is still
					// an encoder to ask. Once only: it does not change within
					// the life of one transform.
					if p.sampleAttrs.Load() == nil {
						if k, q := enc.SampleAttributes(); len(k) > 0 {
							p.sampleAttrs.Store(&attrInventory{keys: k, withQP: q})
						}
					}
					if streamOK {
						if v := int64(qp); v > p.qpStreamMax.Load() {
							p.qpStreamMax.Store(v)
						}
						if v := int64(qp); p.qpStreamMin.Load() == 0 || v < p.qpStreamMin.Load() {
							p.qpStreamMin.Store(v)
						}
						if attrOK {
							p.qpAttrOK.Add(1)
							if d := declared - qp; d != 0 {
								p.qpDivergences.Add(1)
								p.qpAttrBias.Add(int64(d))
								if d < 0 {
									d = -d
								}
								p.qpAttrDeviation.Add(int64(d))
							}
						} else {
							p.qpAttrKO.Add(1)
						}
					}
					switch {
					case attrOK:
						p.qpFromAttr.Store(true)
						p.qpMeasured(declared)
					case streamOK:
						p.qpMeasured(qp)
					}
					p.Stats.LastVideoUnix.Store(time.Now().Unix())
					if sinks.Video != nil {
						sinks.Video(au)
					}
				}
				return nil
			})
			if err != nil {
				return err
			}
			// On an asynchronous transform every METransformHaveOutput
			// authorises exactly one collection: insisting answers E_UNEXPECTED.
			if !got || enc.Async() {
				return nil
			}
		}
	}

	// The size we started at, which is also the scale's cap.
	startW, startH := w, h
	// The cadence in force. It starts from the camera's, which is the cap.
	// curFPS is how many frames are delivered, curDeclared how many are declared
	// to the encoder. At start-up they coincide with the camera's cadence.
	curFPS, curDeclared := fps, fps
	gate := &cadenceGate{}
	p.videoWidth.Store(int64(w))
	p.videoHeight.Store(int64(h))
	p.videoFPS.Store(int64(curDeclared))
	p.videoDelivered.Store(int64(curFPS))

	// The requested size is applied **in here**, not from where it is asked for.
	//
	// It is not fussiness: the change touches three things that have to move
	// together — the Source Reader, the encoder's format and the reducer for
	// motion detection, which is sized on the incoming pixels. Applied from the
	// governor's thread, for an instant the reducer would be reading frames of a
	// size other than the one it was built for.
	applyFormat := func() {
		want := p.wantFormat.Swap(nil)
		if want == nil {
			return
		}
		nw, nh, nf, nd := want.W, want.H, want.FPS, want.Declared
		// The starting size is a **cap**, like the preset's bitrate: congestion
		// can only bring it down, never above what the user chose.
		//
		// It is not only a matter of will: the H.264 level announced in the SDP
		// is fixed on this size, and a larger stream would require one higher
		// than the one declared — which is the way to make decoding fail in
		// silence. Measured trying to climb from 360p to 720p: the stream went
		// on declaring level 3.0, which at 720p30 is not enough.
		if nw > startW || nh > startH {
			nw, nh = startW, startH
		}
		// The starting cadence is a cap too, for the same reason the size is:
		// it is what the camera delivers, and asking for more does not make it
		// go any faster.
		if nf > fps {
			nf = fps
		}
		// The declared one follows the same cap, for the same reason: announcing
		// to the encoder more frames than the camera produces at all is a budget
		// divided by a number that does not exist.
		if nd <= 0 {
			nd = nf
		}
		if nd > fps {
			nd = fps
		}
		if nw == w && nh == h && nf == curFPS && nd == curDeclared {
			return
		}
		// **The Source Reader stays at the camera's cadence.** The extra frames
		// are dropped by the cadence gate below, instead of asking the camera to
		// slow down: a cadence asked of the device can be refused or rounded to
		// a value that is not the one, and the real cadence is known only to us
		// who count the frames. It is the same reason the size is verified on
		// the frame's bytes and not on what the reader declares.
		if nw != w || nh != h {
			if err := reader.SetOutputSize(nw, nh, fps); err != nil {
				p.cfg.Log.Warn("the camera does not deliver this size",
					"size", fmt.Sprintf("%dx%d", nw, nh), "error", err)
				return
			}
			// **The processor is told not to invent frames, at every size
			// change.**
			//
			// A size change is what puts it in the chain, and by default it
			// converts the cadence to match the output type — where we write the
			// scale's 30 while a dim room gives the camera 13.7. What it makes
			// up the difference with is repeated frames, and we encode and send
			// them. Measured on three machines, at a size change, against this
			// same call left out:
			//
			//	NVIDIA + Brio, camera at 13.7   1500 -> 1040 kbit/s, 12% repeats -> 0
			//	AMD + ACER, camera at 17.5      1579 -> 1269 kbit/s
			//	Intel, camera at 27.0           2369 -> 2383, nothing to gain
			//
			// The gain is what the camera is missing from the cadence we
			// declare, so it is largest **in the dark** — which is when this
			// program works — and zero in daylight. It cost 1.2 points of
			// quantiser on the first machine, which is the picture unchanged.
			//
			// **It is asked for after the size change, not before**, because the
			// transform does not exist until then; the documentation asks for it
			// before streaming begins, which we cannot do, so what says it took
			// is the frames and not the HRESULT.
			if !p.cfg.KeepFrameRateConversion {
				if n, err := reader.DisableFrameRateConversion(); err != nil {
					p.cfg.Log.Warn("frame-rate conversion was not switched off", "error", err)
				} else {
					p.cfg.Log.Debug("frame-rate conversion switched off", "transforms", n)
				}
			}
		}
		// The encoder is **rebuilt**, not reconfigured.
		//
		// Reconfiguring the running one does not take, and it is the most
		// insidious fault met so far because every declaration said the
		// opposite: SetOutputType answered S_OK, GetOutputCurrentType read the
		// new size back, the Source Reader really did deliver the new pixels —
		// and the only witness telling the truth was the SPS in the stream,
		// always the old one. A pipeline in that state sends the browser frames
		// that do not match the parameters it decodes them with.
		//
		// At start-up the size is always exact, so that is where it restarts
		// from. `nd` is declared to the encoder, not `nf`: what it needs is how
		// many frames to divide the budget by, and the frames reaching it are
		// the ones the gate lets through.
		// **The rebuild is timed, because the one time it did not come back the
		// log had nothing to say about it.**
		//
		// Between the line the governor writes — "video format changed", with
		// the cadence it wants — and the one at the bottom of this function
		// there are two calls into Media Foundation and nothing else: building
		// the new transform and releasing the old one. When one of the two does
		// not return, the file shows a pair of lines with the second missing,
		// further requests nobody answers, and a monitor with the camera lit and
		// no picture. Which of the two it was cannot be recovered afterwards,
		// and that is the whole cost: a log that says "we went in" and not "we
		// came out".
		//
		// **A duration is not a cure and is not meant as one.** A call that
		// never returns writes no line however well it is timed — for that
		// there is the stack dump the stall raises, in cmd/pat-monitor. What
		// this buys is the case one step before: a rebuild that takes seconds
		// and comes back names itself, instead of being read off the gap
		// between two timestamps by whoever thinks to look.
		builtAt := time.Now()
		fresh, err := newEncoder(nw, nh, nd, p.currentBitrate())
		built := time.Since(builtAt)
		// refused is the time spent inside the attempt that was turned down, and
		// it is zero on every rebuild that does not make one.
		var refused time.Duration
		// **If the new cadence is not accepted, that is what is given up, not
		// the change.** A very low cadence in the output format is the sort of
		// thing an encoder can refuse, and we do not know which ones do: the
		// project's rule is that no feature depends on the manufacturer in front
		// of us. Giving up only the cadence, on those machines the bottom steps
		// simply do not exist and everything else goes on working — which is
		// degrading, not breaking.
		if err != nil && nd != curDeclared {
			p.cfg.Log.Warn("the encoder refuses this cadence, only the size changes",
				"cadence", nd, "error", err)
			nd = curDeclared
			// **The clock restarts and the refusal is kept**, because those are
			// two different questions. What `build` reports is how long the
			// encoder we kept took to build, and measured from before the
			// attempt that failed it would charge one encoder with the time of
			// two — the whole reason these are separate fields is that they
			// accuse different people. What the refusal must not do is vanish:
			// the picture waited for it too, and rebuildWasSlow is where that
			// is argued.
			refused = built
			builtAt = time.Now()
			fresh, err = newEncoder(nw, nh, nd, p.currentBitrate())
			built = time.Since(builtAt)
		}
		if err != nil {
			p.cfg.Log.Warn("no encoder for the new format, staying where we were",
				"format", fmt.Sprintf("%dx%d@%d", nw, nh, nd), "error", err)
			if nw != w || nh != h {
				_ = reader.SetOutputSize(w, h, fps)
			}
			return
		}
		// The replacement happens under the write lock, so whoever is commanding
		// the encoder — the congestion control every second, a PLI on every loss
		// — cannot find the old one in their hands. The old one is closed
		// **after** letting the lock go: by then nobody can have taken it any
		// more, and closing is the one operation worth not doing while holding
		// everything else still.
		old := enc
		p.encMu.Lock()
		enc = fresh
		p.control.Store(enc)
		p.encMu.Unlock()
		closedAt := time.Now()
		old.Close()
		closed := time.Since(closedAt)
		// The three are reported separately because they accuse different
		// people: building is the driver being asked for a new session, closing
		// is it being asked to let the old one go, and a refusal is it being
		// asked for a cadence it will not take. A single "the rebuild took 4s"
		// would leave the next reader exactly where this one was — and a
		// `refused=0s` is itself an answer, which is why it is written whether
		// or not there was a second attempt.
		if rebuildWasSlow(refused, built, closed) {
			p.cfg.Log.Warn("rebuilding the encoder was slow",
				"refused", refused.Round(time.Millisecond),
				"build", built.Round(time.Millisecond),
				"close", closed.Round(time.Millisecond),
				"format", fmt.Sprintf("%dx%d@%d", nw, nh, nd),
				"note", "no frame is encoded while this runs")
		}
		w, h = nw, nh
		curFPS, curDeclared = nf, nd
		// **The gate is commanded by the delivery, never by the declaration.**
		// Driving it from the declaration, and computing the declaration from
		// the measured cadence, gives a closed loop: the gate lowers the
		// measurement, the measurement lowers the declaration again, and there
		// is no way out.
		gate.setFPS(nf, fps)
		motion = newMotionScaler(w, h)
		// The declared level is **not** touched: the SDP announces one only and
		// whoever is watching has already configured the decoder on it. It stays
		// the one for the full pixels, which is always enough for a smaller size.
		p.videoWidth.Store(int64(w))
		p.videoHeight.Store(int64(h))
		// The status page shows "measured/declared", so the declared one goes
		// here: it is the number the measured one is compared with.
		p.videoFPS.Store(int64(curDeclared))
		p.videoDelivered.Store(int64(curFPS))
		// Read back from the reader, not inferred from what it was asked for: a
		// refused assignment would leave it delivering the old size, and the
		// encoder would receive frames of a size other than the one it is
		// configured for. It proves that the assignment took, and nothing about
		// the pixels — a camera too small to fill the frame is upscaled and the
		// size read back agrees all the same.
		rw, rh, _, _, _, rerr := reader.CurrentFormat()
		p.cfg.Log.Info("video format changed",
			"video", fmt.Sprintf("%dx%d@%d", w, h, curFPS),
			"declared", curDeclared,
			"reader", fmt.Sprintf("%dx%d", rw, rh), "error", rerr)
	}

	// The bitrate reconfiguration, which is the only thing that modifies the
	// transform instead of replacing it. In here nobody is calling Feed or
	// ProcessOutput on it at the same instant, which is the only condition under
	// which stopping and restarting it is safe.
	applyReconfig := func() {
		want := int(p.wantReconfig.Swap(0))
		if want <= 0 {
			return
		}
		p.brMu.Lock()
		p.lastReconfig = time.Now()
		p.brMu.Unlock()
		if err := enc.ReconfigureBitrate(want); err != nil {
			// It is not a capture fault: it is a road that did not work, and
			// the other one remains. It is said once and we carry on.
			p.cfg.Log.Warn("bitrate reconfiguration failed, carrying on",
				"kbps", want, "error", err)
		}
	}

	// **Nothing else reconfigures the encoder live, and nothing downstream
	// should reach for it.** Two more such engines used to live here — one
	// changed the quantiser floor for the automatic tuning, the other the
	// bit-spending criterion for the switch between CBR and constant quality —
	// and both of those features are gone: the tuning because in three runs it
	// got two wrong, the switch because the saving turned out to be obtainable by
	// commanding the bitrate alone.
	//
	// The configuration knobs remain, and they apply at construction: `MinQP`
	// and `RateControl` still hold, and are not changed live.

	for ctx.Err() == nil {
		applyFormat()
		applyReconfig()

		ev, err := enc.NextEvent(5 * time.Second)
		if err != nil {
			return err
		}
		switch ev {
		case mf.EventNone:
			return fmt.Errorf("the encoder neither asked nor delivered anything for 5s")

		case mf.EventNeedInput:
			// The request is good once only. ReadSample can return (nil, nil)
			// when the source has no frame ready yet, and going back to the top
			// of the loop in that case means throwing it away: the encoder keeps
			// exactly one in flight and does not send another until it has been
			// answered. The wait that follows is indistinguishable from an
			// encoder that will not start.
			// **Dropping a frame cannot mean going back to the top of the
			// loop**: the encoder's request is good once only, and whoever
			// throws it away waits for ever for the next. So it reads until it
			// finds one to deliver, and that request is always answered with
			// exactly one delivery.
			//
			// Motion detection goes on seeing **every** frame, including the
			// ones the encoder never gets: its cadence is what decides how
			// quickly something moving is noticed, and there is no reason to
			// notice later just because the network got worse. It costs a resize
			// to 160x90, which is the right price for not making the detector
			// hostage to the bandwidth.
			var sample *mf.Sample
			for {
				s, err := readFrame(ctx, reader)
				if err != nil {
					return err
				}
				if s == nil {
					return nil // context cancelled
				}

				if sinks.Motion != nil && motionGate.due(time.Now()) {
					if err := motion.fromSample(s, func(gray []byte) {
						p.Stats.MotionFrames.Add(1)
						sinks.Motion(gray, motion.w, motion.h)
					}); err != nil {
						s.Release()
						return err
					}
				}

				if gate.due(time.Now()) {
					sample = s
					break
				}
				s.Release()
				p.Stats.VideoDropped.Add(1)
			}

			err = enc.Feed(sample)
			sample.Release()
			if errors.Is(err, mf.ErrNotAccepting) {
				// A request issued before a reconfiguration arrived after it.
				// The frame is skipped and the next request awaited: the
				// restarted transform asks for another one straight away, and
				// the rule "a request is good once only" is not broken here
				// because that request no longer exists.
				continue
			}
			if err != nil {
				return err
			}
			// Only the synchronous ones deliver immediately after receiving.
			if !enc.Async() {
				if err := take(); err != nil {
					return err
				}
			}

		case mf.EventHaveOutput:
			if err := take(); err != nil {
				return err
			}
		}
	}
	return nil
}

// readFrame keeps at it until the camera delivers a frame.
func readFrame(ctx context.Context, reader *mf.SourceReader) (*mf.Sample, error) {
	for range 2000 {
		if ctx.Err() != nil {
			return nil, nil
		}
		s, _, err := reader.ReadSample()
		if err != nil {
			return nil, err
		}
		if s != nil {
			return s, nil
		}
	}
	return nil, fmt.Errorf("the camera delivers no frames")
}

// motionScaler derives the reduced image for motion detection.
//
// No filter is needed: the luma plane of an NV12 frame is already the greyscale
// image, and it sits at the head of the buffer. It is averaged in boxes, which at
// these proportions is antialiasing enough and costs a single pass.
type motionScaler struct {
	w, h int
	out  []byte
}

func newMotionScaler(w, h int) *motionScaler {
	return &motionScaler{w: w, h: h, out: make([]byte, motionFrameSize)}
}

func (m *motionScaler) fromSample(s *mf.Sample, fn func([]byte)) error {
	buf, err := s.Buffer()
	if err != nil {
		return err
	}
	defer buf.Release()
	return buf.WithBytes(func(b []byte) error {
		if len(b) < m.w*m.h {
			return fmt.Errorf("frame too short: %d bytes for %dx%d", len(b), m.w, m.h)
		}
		m.scale(b[:m.w*m.h])
		fn(m.out)
		return nil
	})
}

func (m *motionScaler) scale(y []byte) {
	for oy := range MotionHeight {
		y0 := oy * m.h / MotionHeight
		y1 := (oy + 1) * m.h / MotionHeight
		if y1 <= y0 {
			y1 = y0 + 1
		}
		for ox := range MotionWidth {
			x0 := ox * m.w / MotionWidth
			x1 := (ox + 1) * m.w / MotionWidth
			if x1 <= x0 {
				x1 = x0 + 1
			}
			sum, n := 0, 0
			for yy := y0; yy < y1; yy++ {
				row := y[yy*m.w:]
				for xx := x0; xx < x1; xx++ {
					sum += int(row[xx])
					n++
				}
			}
			m.out[oy*MotionWidth+ox] = byte(sum / n)
		}
	}
}

// ---------- audio ----------

// micCleanByOtherRoad says whether the unprocessed signal arrived without raw
// mode, that is, by exclusive mode after the endpoint refused raw.
//
// It asks about the refusal and not about the name of the road on purpose: the
// strings "raw" and "exclusive" belong to internal/audio, and a comparison
// against one of them here would be a second copy of a vocabulary this package
// does not own. RawError is set only when raw was asked for and denied, and
// RawMode stays true when exclusive then granted the same signal — so the pair
// is the question, and neither half answers it alone.
func micCleanByOtherRoad(rawMode bool, rawErr error) bool {
	return rawMode && rawErr != nil
}

// runAudio captures from the microphone, compresses to Opus and feeds the
// detector.
//
// The PCM is used twice: once for the encoder, once for the analysis. They are
// two consumers of the same stream and not two captures, so the microphone stays
// open once only.
func (p *Pipeline) runAudio(ctx context.Context, sinks Sinks) error {
	// The capture's context derives from the general one because we can close it
	// too, to try a better path: see the timer further down.
	runCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	// The close can also be asked for by whoever changes microphone, who is not
	// here: the way to reach it is this pointer. The flag is cleared **before**
	// opening, otherwise a choice arriving while we were closed would be
	// consumed by this open instead of by the next one.
	p.micCancel.Store(cancel)
	p.micReopen.Store(false)
	wanted := p.micWantedID()

	var (
		enc      audiocodec.Encoder
		format   audio.StreamFormat
		mono     []int16 // mono samples waiting to make up an Opus frame
		levelAcc []int16 // samples already reduced to the analysis rate
		levelOut []byte
		conv     []int16
		decim    int
		carry    []int16 // the remainder of the average that did not make a sample
		levelRS  *resample.Stream
		levelF32 []float32
		levelN   int
		gain     float64
		recheck  atomic.Bool
	)
	defer func() {
		if enc != nil {
			enc.Close()
		}
	}()

	// The test source has the same shape as the capture: from here down not a
	// line changes, and that is what makes this prove anything.
	capture := audio.Capture
	if p.cfg.AudioTestTone {
		capture = audio.CaptureTone
	}

	// **The call blocks for the whole session, so the flag cannot wait for it
	// to return.** What marks the end of the opening is the callback running —
	// the endpoint is open — or the call answering with an error. Both clear it.
	p.micOpening.Store(true)
	// The same belt as the camera's, for the same reason: a panic inside the
	// WASAPI open would skip both of the clears below, and a microphone stuck
	// at "being opened" is `mic-missing` and `mic-silent` waived for ever.
	defer p.micOpening.Store(false)
	err := capture(runCtx,
		audio.Options{DeviceID: wanted, Raw: true, FallbackOnRawFailure: true},
		func(s audio.Stream) error {
			p.micOpening.Store(false)
			p.rawMode.Store(s.RawMode)
			// **The mute was already read here and went no further than the
			// log.** It is the cause of the silence the monitor was about to
			// report as `mic-silent`, and it is the one cause of silence that
			// whoever is in front of the machine can undo in a click.
			p.micMuted.Store(s.Muted)
			// The endpoint opened, so whatever Windows was refusing it is not
			// refusing now. See camDenied for why this is cleared at the open
			// and not at the end of a session.
			p.micDenied.Store(false)
			// **Only whoever opened it knows what opened.** The name is not the
			// chosen one: if the chosen microphone is no longer there, WASAPI
			// opens the default, and the status page has to say what is
			// capturing.
			prevMode, _ := p.micMode.Load().(string)
			prevDev := p.Microphone()
			p.micMode.Store(s.Mode)
			p.micOpen.Store(&MicDevice{ID: s.DeviceID, Name: s.DeviceName})
			// The fallback is declared. A microphone unplugged and silently
			// replaced by another is audio that is there and is not the one
			// asked for: without this line, whoever reads the log has no way of
			// knowing why the wrong room can be heard.
			//
			// **But it is declared when it changes, not on every open.** Since
			// the missing endpoint makes it fall back instead of failing, that
			// is a **state**, not an event: on a machine that does not obtain raw
			// mode the recheck reopens every two minutes, and this line would
			// come back ~720 times a night. It is the same reason — and the same
			// condition — as the line announcing the open.
			changed := prevMode != s.Mode || prevDev.ID != s.DeviceID
			if wanted != "" && s.DeviceID != wanted && changed {
				p.cfg.Log.Warn("the chosen microphone is not available: opened another one",
					"chosen", wanted, "device", s.DeviceName, "id", s.DeviceID)
			}
			// The open is announced only when something changes: with the
			// periodic path recheck this line would come back every two minutes,
			// and a log that repeats the same thing all night hides what did
			// happen once only. **Changing endpoint counts as much as changing
			// road**: that was the only one of the two questions this line
			// asked, and since the microphone can be chosen it is not enough.
			if changed {
				p.cfg.Log.Info("microphone opened",
					"device", s.DeviceName, "format", s.Format.String(),
					"mode", s.Mode, "no_oem_effects", s.RawMode)
			} else {
				p.cfg.Log.Debug("microphone reopened", "mode", s.Mode)
			}
			// **The road is declared even when it worked**, and the absence of
			// this line costs an evening. An endpoint can begin refusing raw
			// mode with the same driver and the same APO build as the baseline
			// and a Windows update in between — nothing local changed — and the
			// log then said `mode=exclusive` and nothing else: the only reader
			// of RawError is the warning below, guarded
			// by `!s.RawMode`, which the exclusive road makes false. The
			// refusal's own code, AUDCLNT_E_RAW_MODE_UNSUPPORTED, was in
			// memory and reached no file, and answering "why" took a throwaway
			// program written for the purpose. It is not a fault — the signal
			// is unprocessed by the other route — which is exactly why it is
			// Info and not Warn, and why the sentence says what it costs
			// instead: the microphone is nobody else's while the monitor runs.
			// It follows `changed` like the line above it, for the same reason:
			// the road is a state, and the recheck would write it every two
			// minutes all night.
			if changed && micCleanByOtherRoad(s.RawMode, s.RawError) {
				p.cfg.Log.Info("raw mode refused, so the microphone is open in "+
					"exclusive mode: the signal is unprocessed, and the device is "+
					"held for as long as the monitor runs", "reason", s.RawError)
			}
			// **A choice arriving while we were opening is not lost.** The
			// command comes from another goroutine and its close may have
			// preceded the pointer that makes it reachable: reading the choice
			// once only, it would sit written and applied by nobody.
			if p.micWantedID() != wanted {
				p.micReopen.Store(true)
				cancel()
			}
			// The tone does not go through WASAPI and so has no raw mode to
			// obtain: warning that it "may be filtered" would point at a fault
			// that cannot exist there.
			// Inside a planned reopen nothing is announced: it is the same
			// reason "microphone opened" above comes out only when the mode
			// changes, applied to the other two lines the open writes. Left out,
			// they are worth ~1440 identical lines a night.
			// The grace silences the reopens nobody asked for. A choice by
			// whoever is watching is not one of those: it is the one moment when
			// "this microphone is filtered" really matters, because it has just
			// been chosen and another one can still be.
			chosen := p.micChosen.Swap(false)
			planned := p.inMicGrace() && !chosen
			if !s.RawMode && !p.cfg.AudioTestTone {
				// The reason travels with the warning: without it, the one
				// really dangerous degradation of this program would be left
				// unexplained, and it would not even be known whether to try
				// again.
				if planned {
					p.cfg.Log.Debug("raw mode still not obtained", "reason", s.RawError)
				} else {
					p.cfg.Log.Warn("raw mode not obtained: the audio goes through the OEM "+
						"effects and may be filtered", "reason", s.RawError)
				}
			}

			// **A device fallback is re-examined too, not only a mode one.**
			//
			// The recheck was born for raw mode: the refusal can come from
			// another application holding the microphone — a video call, a voice
			// assistant — and when that one closes, the road to the clean signal
			// opens again. Without it, the monitor would stay on the processed
			// path all night, that is on this machine it would stay mute, over a
			// phone call that ended at nine.
			//
			// **But an unplugged microphone is a fallback of the same kind, and
			// that one would stay for ever.** Since an absent endpoint makes the
			// default open instead of failing, whoever plugs the USB stick back
			// in has no way of returning to it: on a machine that does obtain raw
			// mode — that is, where everything is fine — the timer would not be
			// armed at all, and the capture would stay on the wrong device until
			// the program restarts. The remedy is not a gesture on the page:
			// that one can simply not be made, and from a `<select>` it is not
			// even reachable — an option already selected emits no `change`.
			//
			// The rule stays what it was, applied to two fallbacks instead of
			// one: **it is only retried while we are on the worse path**, and
			// when we are on the good one the timer does not exist and the audio
			// is never interrupted.
			if micRecheckWanted(s.RawMode, s.Muted, wanted, s.DeviceID, p.cfg.AudioTestTone) {
				guard.After(p.cfg.Log, "the microphone re-examination timer",
					micRecheckInterval, func() {
						recheck.Store(true)
						cancel()
					})
			}

			// The rate is dictated by the microphone, provided it is one of the
			// ones Opus compresses natively. There is no resampling: a resampler
			// written in a hurry is the simplest way to alter the pitch of sounds
			// without noticing, and a cry played back a tone higher leaves no
			// visible sign.
			if !audiocodec.RateSupported(s.Format.SampleRate) {
				return fmt.Errorf("the microphone captures at %d Hz, which Opus does not "+
					"compress natively (it accepts %v) and we do not resample: "+
					"pick another rate in the device properties",
					s.Format.SampleRate, audiocodec.SupportedRates)
			}
			var err error
			if enc, err = audiocodec.NewOpus(s.Format.SampleRate, p.cfg.AudioBitrateKbps); err != nil {
				return err
			}
			format = s.Format

			// **The analysis stream is always at 16 kHz**, for any microphone:
			// whoever reads it — the shape detector and the recogniser — no
			// longer has a rate to ask for and depend on. How it gets there is
			// decided by `analysisPlan`.
			var resampled bool
			decim, resampled = analysisPlan(s.Format.SampleRate)
			carry = carry[:0]
			levelRS = nil
			if resampled {
				var err error
				if levelRS, err = resample.NewStream(s.Format.SampleRate, AnalysisSampleRate); err != nil {
					return err
				}
				p.cfg.Log.Info("analysis stream resampled",
					"from_hz", s.Format.SampleRate, "to_hz", AnalysisSampleRate,
					"reason", "the ratio is not a whole number of samples to average")
			}
			p.analysisRate.Store(AnalysisSampleRate)
			levelN = AnalysisSampleRate * int(levelBlockDuration/time.Millisecond) / 1000
			levelOut = make([]byte, 0, levelN*2)
			// The endpoint gain is applied by the audio engine only in shared
			// mode: in exclusive it arrives here still to be done, and without it
			// the audio sounds much quieter while not being filtered at all.
			gain = dbToLinear(s.GainDB + p.cfg.MicGainDB)
			if s.Muted {
				// Mute is an explicit choice by whoever uses the PC, and in
				// exclusive Windows does not apply it for us. Respecting it is
				// right, but the silence that follows has to be explained.
				gain = 0
				p.cfg.Log.Warn("the microphone is muted in Windows: the monitor will hear nothing")
			}
			if s.GainDB != 0 {
				p.cfg.Log.Info("sensitivity raised to the endpoint maximum",
					"gain_db", s.GainDB, "mode", s.Mode)
			}
			// **A gain that could not be read is not a gain of zero**, and on
			// the exclusive path the difference is the whole signal: the audio
			// engine is not there to apply the endpoint volume, so what is not
			// reapplied here is simply lost — measured, 30 dB, that is audio
			// arriving clean and six times quieter with nothing saying so. It
			// is announced on the same terms as the raw-mode refusal: the
			// reason travels with the stream, and a planned reopen does not
			// repeat it all night.
			if s.VolumeError != nil {
				if planned {
					p.cfg.Log.Debug("the endpoint gain still cannot be read",
						"reason", s.VolumeError)
				} else {
					p.cfg.Log.Warn("the endpoint gain could not be read and in exclusive "+
						"mode nobody applies it: the audio may be much quieter",
						"reason", s.VolumeError, "mode", s.Mode)
				}
			}
			if p.audioOK.CompareAndSwap(false, true) && !planned {
				// The audio's return is always announced, because it is news —
				// **except after a reopen we decided on**, where nothing came
				// back: it had not gone away.
				p.cfg.Log.Info("audio active")
			}
			return nil
		},
		func(pcm []byte, silent bool) error {
			p.Stats.AudioBytesIn.Add(int64(len(pcm)))
			if p.cfg.Trace != nil {
				p.cfg.Trace.MicCallback.Mark(time.Now())
			}

			frames := len(pcm) / format.BytesPerFrame()
			if cap(conv) < frames {
				conv = make([]int16, frames)
			}
			conv = conv[:frames]
			n, err := audio.ToMonoS16(pcm, format, conv)
			if err != nil {
				return err
			}
			if gain != 1 {
				applyGain(conv[:n], gain)
			}
			mono = append(mono, conv[:n]...)

			// One Opus packet for every complete frame. The remainder stays
			// queued for the next block: the capture delivers in blocks that are
			// not multiples of 20 ms, and aligning here costs a copy of a few
			// dozen samples.
			frameSamples := enc.FrameSamples()
			for len(mono) >= frameSamples {
				pkt, err := enc.Encode(mono[:frameSamples])
				mono = append(mono[:0], mono[frameSamples:]...)
				if err != nil {
					return err
				}
				if len(pkt) == 0 {
					continue
				}
				p.simulatePanic("audio", p.Stats.AudioPackets.Add(1))
				p.Stats.LastAudioUnix.Store(time.Now().Unix())
				if p.cfg.Trace != nil {
					p.cfg.Trace.AudioEncode.Mark(time.Now())
				}
				if sinks.Audio != nil {
					sinks.Audio(pkt)
				}
			}

			// The stream for the analysis reaches 16 kHz by one of the two roads
			// `analysisPlan` chose.
			if sinks.Level == nil {
				return nil
			}
			if levelRS != nil {
				levelF32 = levelF32[:0]
				for _, v := range conv[:n] {
					levelF32 = append(levelF32, float32(v)/32768)
				}
				for _, v := range levelRS.Write(levelF32) {
					levelAcc = append(levelAcc, toS16(float64(v)*32768))
				}
			} else {
				// The average is also the low-pass filter the subsampling
				// requires: taking one in three and no more would create aliases
				// on the high frequencies, which are of interest to a sound
				// classifier.
				//
				// **The remainder passes to the next call.** WASAPI does not
				// deliver a number of samples that is a multiple of `decim`, and
				// throwing it away loses up to two samples per callback — fifty
				// callbacks a second, that is an analysis stream running slower
				// than real time and falling behind all night.
				carry = append(carry, conv[:n]...)
				i := 0
				for ; i+decim <= len(carry); i += decim {
					sum := 0
					for j := 0; j < decim; j++ {
						sum += int(carry[i+j])
					}
					levelAcc = append(levelAcc, int16(sum/decim))
				}
				carry = append(carry[:0], carry[i:]...)
			}
			for len(levelAcc) >= levelN {
				levelOut = levelOut[:0]
				for _, s := range levelAcc[:levelN] {
					levelOut = append(levelOut, byte(s), byte(s>>8))
				}
				levelAcc = append(levelAcc[:0], levelAcc[levelN:]...)
				sinks.Level(levelOut)
			}
			return nil
		},
	)
	// **The opening is over whichever way this came back.** The callback clears
	// it when the endpoint opens; this covers the road where it never did, so
	// that a refusal is answered by the denied flag rather than staying pending
	// for ever. Storing false twice costs nothing.
	p.micOpening.Store(false)
	// The flag is cleared by the supervisor: clearing it here first would leave
	// it with no way of knowing whether the audio was there.
	//
	// A close we decided on is not a fault and must not look like one: the
	// supervisor recognises it by this error and reopens at once, with no wait
	// and no warnings.
	if recheck.Load() {
		return errMicRecheck
	}
	if p.micReopen.Load() {
		return errMicSwitch
	}
	if err != nil && ctx.Err() == nil {
		return fmt.Errorf("audio capture: %w", err)
	}
	return nil
}

// analysisPlan chooses how the analysis stream's 16 kHz are reached.
//
// **The analysis stream is always at 16 kHz.** Where the ratio is not a whole
// number — a microphone at 24 kHz — the capture rate cannot simply be handed on
// and declared, because with a 24 kHz stream the recogniser cannot run at all,
// and that would be a machine on which the monitor watches less than the others
// with nothing visible saying so.
//
// Both roads stay, and the first is the better one where it exists: **averaging
// a whole number of samples** is exact, has no filters and has nothing to tune —
// and the average is also the low-pass the subsampling requires. The resampler
// is for the rest: 44.1 and 24 kHz, and the microphones at 8 and 12 kHz, which
// go **up** rather than down. There half the spectrum comes out empty, and that
// is honest: that sound has no content up there.
//
// The resampler is trusted because it has been tested with a tone above Nyquist,
// which is the one case where a broken resampler is distinguishable from a
// correct one.
func analysisPlan(captureRate int) (decim int, resampled bool) {
	if captureRate%AnalysisSampleRate == 0 {
		return captureRate / AnalysisSampleRate, false
	}
	return 1, true
}

func applyGain(s []int16, gain float64) {
	for i, v := range s {
		x := float64(v) * gain
		switch {
		case x > 32767:
			s[i] = 32767
		case x < -32768:
			s[i] = -32768
		default:
			s[i] = int16(x)
		}
	}
}

// dbToLinear converts a gain in decibels into the factor to multiply by.
func dbToLinear(db float64) float64 { return math.Pow(10, db/20) }

// toS16 brings a floating-point sample back to 16-bit integers, clipping at the
// extremes. **The clip is needed**: the resampler has a filter, and a filter can
// produce a value just beyond the peak it had at its input — without the clip
// that value wraps around and becomes a sample of the opposite sign, that is a
// click.
func toS16(v float64) int16 {
	switch {
	case v > 32767:
		return 32767
	case v < -32768:
		return -32768
	}
	return int16(v)
}
