//go:build windows

package mf

import (
	"fmt"
	"strings"
	"syscall"
	"unsafe"

	"github.com/go-ole/go-ole"
)

// ICodecAPI is the encoder's control panel, and it carries the two commands
// quality on a changing network depends on: from here the bitrate is changed
// while the encoder runs, and a keyframe is asked for when the browser reports
// lost packets.

var iidICodecAPI = guid("{901db4c7-31ce-41a2-85dc-8fa0bf41b8da}")

// The settings we care about. The values come from the headers, not from
// memory: getting one wrong means silently writing the wrong property.
var (
	codecRateControlMode = guid("{1c0608e9-370c-4710-8a58-cb6181c42423}")
	codecMeanBitRate     = guid("{f7222374-2144-4815-b550-a37f8e12ee52}")
	codecBufferSize      = guid("{0db96574-b6a4-4c8b-8106-3773de0310cd}")
	codecQualityVsSpeed  = guid("{98332df8-03cd-476b-89fa-3f9e442dec9f}")
	codecGOPSize         = guid("{95f31b26-95a4-41aa-9303-246a7fc6eef1}")
	codecLowLatencyMode  = guid("{9c27891a-ed7a-40e1-88e8-b22727a024ee}")
	codecForceKeyFrame   = guid("{398c1b98-8353-475a-9ef2-8f265d260345}")

	// The two constant-quality properties. The GUIDs are cross-checked between
	// the mingw-w64 headers and the ones Microsoft publishes in win32metadata,
	// because the SDK is not present on this machine: see the invariant about
	// the quantiser.
	//
	// AVEncCommonQuality goes from 0 to 100 and is VT_UI4, not VT_R4 as one
	// might expect of a "quality". The type is dictated by the declaration, not
	// by the sense of the question — a lesson already paid for with
	// AVEncVideoForceKeyFrame.
	codecQuality    = guid("{fcbf57a3-7ea5-4b0c-9644-69b40c39c391}")
	codecMaxBitRate = guid("{9651eae4-39b9-4ebf-85ef-d7f444ec7465}")

	// The quantiser's floor and ceiling, that is, the limits within which the
	// encoder may move its adaptive algorithm.
	//
	// **They are not AVEncVideoEncodeQP**, which is the lever of constant
	// quality mode and imposes a value; these two leave the decision to the
	// encoder inside a band. It is the difference between dictating the result
	// and giving a constraint, and it is what makes capped CRF possible.
	//
	// Cross-checked between the Windows SDK headers and mingw-w64's, which
	// declare them identical byte for byte. Both VT_UI4, range 0-51.
	// AVEncVideoMaxQP is declared **static**, settable only before starting; on
	// MinQP the documentation says nothing, which is not permission — so both
	// are set when the encoder is built and not while it runs.
	codecMinQP = guid("{0ee22c6a-a37c-4568-b5f1-9d4c2b3ab886}")
	codecMaxQP = guid("{3daf6f66-a6a7-45e0-a8e5-f2743f46a3a2}")
)

// What the stream must not contain, so that it stays readable.
//
// Both values were cross-checked between the mingw-w64 headers and the Windows
// SDK's codecapi.h, which agree byte for byte: it is the procedure this file
// documents, because **a wrong GUID does not complain** — the property is
// simply never applied, and nothing says so.
//
// The declared types come from the documentation and not from the sense of the
// question, which is the other trap: CABAC is VT_BOOL, the B-frame count VT_UI4
// — while AVEncVideoForceKeyFrame, which also asks yes or no, is VT_UI4.
//
// **And both were verified at run time, with a control.** Two agreeing headers
// say the value is transcribed right, not that this encoder knows the key —
// SetValue on a key that does not exist answers S_OK. IsSupported does
// distinguish them, provided one asks it something known to be wrong as well.
// Measured on Quick Sync, with a made-up GUID beside the two:
//
//	CABAC               supported=S_OK     modifiable=S_OK
//	B-frame count       supported=S_OK     modifiable=S_FALSE
//	GOP size (in use)   supported=S_OK     modifiable=S_OK
//	a made-up GUID      supported=E_NOTIMPL  modifiable=E_INVALIDARG
//
// The last row is what makes the other three mean anything. The S_FALSE on the
// B-frame count is "not while running", which is no obstacle: it is set when
// the encoder is built.
var (
	codecCABAC         = guid("{ee6cad62-d305-4248-a50e-e1b255f7caf8}")
	codecBPictureCount = guid("{8d390aac-dc5c-4200-b57f-814d04babab2}")
)

// RateControl is the criterion by which the encoder spends bits.
type RateControl string

const (
	// RateCBR: constant bitrate. It fills the pipe whatever there is to film,
	// and it is what you want when the bandwidth is given and fixed.
	RateCBR RateControl = "cbr"

	// RateQuality: the quality is fixed and the bitrate follows the scene.
	//
	// On a baby monitor that is the opposite of what CBR does, and in the right
	// direction: the room is still for 99% of the night and moves in the 1%
	// that is the only moment that counts. CBR spends when nothing is happening
	// and runs out of bits when something does.
	//
	// It goes **always** with a cap (AVEncCommonMaxBitRate), otherwise a sudden
	// movement would produce a spike the network cannot carry: that would trade
	// blockiness for freezing, which is worse.
	//
	// **But that cap does not exist here.** The H.264 Video Encoder
	// documentation says AVEncCommonMaxBitRate "applies when the rate control
	// mode is PeakConstrainedVBR": asked for in Quality it is not ignored on
	// some hardware whim, it is simply not provided for. Measured, 10507 kbit/s
	// with the cap at 2500. That is why RateCapped exists.
	RateQuality RateControl = "quality"

	// RateCapped: quality with a real cap, in a single loop.
	//
	// It is the *capped CRF* the rest of the world uses for streaming — a
	// quality target with a hard ceiling — and on Windows it is put together
	// from three pieces rather than being one mode: **PeakConstrainedVBR**
	// because it is the only one where the cap applies, **AVEncCommonMaxBitRate**
	// which is the cap, and **AVEncVideoMinQP** which is the quality floor —
	// "the encoder should not produce a QP value lower than what is specified",
	// that is, stop spending once the picture is already good enough.
	//
	// **It has to be verified on the effect.** MinQP is an optional property:
	// an encoder can accept it and not apply it, exactly as Quick Sync does with
	// SetBitrate. The proof is in the QP of the outgoing samples and in the
	// bytes produced, not in S_OK and not in IsSupported either.
	RateCapped RateControl = "capped"
)

// DefaultMinQP is the quality floor when none is asked for.
//
// Thirty is the quantiser the measurements on this machine point to as a clean
// picture, and it is also the point beyond which the encoder starts preserving
// the sensor's noise — which in the dark is incompressible and cannot be seen.
// Below that value it is spending for nothing.
//
// As with DefaultQuality, on a different encoder the number will be another
// one, and the way to find it is `pat-capture -rc capped -minqp N`.
const DefaultMinQP = 30

// DefaultQuality is the quality level when none is asked for.
//
// The scale runs from 0 to 100 and **it is not the quantiser**: the mapping
// belongs to the encoder and has to be measured with `pat-capture -rc quality
// -q N`. On Quick Sync, measured:
//
//	q=70 -> QP 25, 10507 kbit/s   (the sensor's noise, preserved at great cost)
//	q=50 -> QP 30,   979 kbit/s
//	q=30 -> QP 36,   250 kbit/s
//	q=15 -> QP 40,   145 kbit/s
//
// 50 is the right value here because it lands on QP 30, which our measurements
// point to as a clean picture, and because the jump from 50 to 70 is **ten
// times** the bandwidth for five points of quantiser: below 30 the encoder
// starts preserving the sensor's noise, which is incompressible and cannot be
// seen.
//
// On a different encoder the number will be another one, and the way to find it
// is that command line.
const DefaultQuality = 50

// Bitrate control modes.
const (
	rateControlCBR                = 0
	rateControlPeakConstrainedVBR = 1
	rateControlUnconstrainedVBR   = 2
	rateControlQuality            = 3
)

type codecAPI struct {
	ole.IUnknown
}

// Mind RegisterForEvent and UnregisterForEvent: they sit between SetValue and
// SetAllDefaults, where one would not think to put them. Deducing this table
// instead of copying it means calling the wrong method with plausible
// arguments.
type codecAPIVtbl struct {
	ole.IUnknownVtbl
	IsSupported              uintptr
	IsModifiable             uintptr
	GetParameterRange        uintptr
	GetParameterValues       uintptr
	GetDefaultValue          uintptr
	GetValue                 uintptr
	SetValue                 uintptr
	RegisterForEvent         uintptr
	UnregisterForEvent       uintptr
	SetAllDefaults           uintptr
	SetValueWithNotify       uintptr
	SetAllDefaultsWithNotify uintptr
	GetAllSettings           uintptr
	SetAllSettings           uintptr
	SetAllSettingsWithNotify uintptr
}

func (c *codecAPI) vtbl() *codecAPIVtbl {
	return (*codecAPIVtbl)(unsafe.Pointer(c.RawVTable))
}

func (c *codecAPI) setUINT32(key *ole.GUID, value uint32) error {
	v := ole.NewVariant(ole.VT_UI4, int64(value))
	r, _, _ := syscall.SyscallN(c.vtbl().SetValue,
		uintptr(unsafe.Pointer(c)), uintptr(unsafe.Pointer(key)), uintptr(unsafe.Pointer(&v)))
	return check("SetValue", r)
}

// isSupported and isModifiable question the encoder about a property instead of
// trying it: they answer S_OK or S_FALSE, and S_FALSE is not a COM error, so it
// has to be compared explicitly. They are there to know, before the first PLI,
// whether a keyframe on request is something this encoder can do.
func (c *codecAPI) query(method uintptr, key *ole.GUID) error {
	r, _, _ := syscall.SyscallN(method, uintptr(unsafe.Pointer(c)), uintptr(unsafe.Pointer(key)))
	if r != 0 {
		return fmt.Errorf("0x%08X", uint32(r))
	}
	return nil
}

func (c *codecAPI) isSupported(key *ole.GUID) error {
	return c.query(c.vtbl().IsSupported, key)
}

func (c *codecAPI) isModifiable(key *ole.GUID) error {
	return c.query(c.vtbl().IsModifiable, key)
}

func (c *codecAPI) setBool(key *ole.GUID, value bool) error {
	// VARIANT_TRUE is -1, not 1: it is the Visual Basic legacy, and a 1 here is
	// read as "true" by some encoders and ignored by others.
	var raw int64
	if value {
		raw = -1
	}
	v := ole.NewVariant(ole.VT_BOOL, raw)
	r, _, _ := syscall.SyscallN(c.vtbl().SetValue,
		uintptr(unsafe.Pointer(c)), uintptr(unsafe.Pointer(key)), uintptr(unsafe.Pointer(&v)))
	return check("SetValue", r)
}

// applyCodecSettings configures the bitrate control.
//
// These are not finishing touches: some hardware encoders will not start until
// they know by what criterion to spend the bits, and the rate control mode is
// among the properties Windows certification declares mandatory.
//
// Every setting is attempted: an encoder that does not support one answers and
// we carry on. What is not done is pretending it went well — the report lists
// them all.
func (e *VideoEncoder) applyCodecSettings(cfg VideoEncoderConfig) error {
	obj, err := e.t.QueryInterface(iidICodecAPI)
	if err != nil {
		e.codecNotes = append(e.codecNotes, fmt.Sprintf("ICodecAPI unavailable: %v", err))
		return nil
	}
	e.codec = (*codecAPI)(unsafe.Pointer(obj))

	note := func(name string, err error) {
		if err != nil {
			e.codecNotes = append(e.codecNotes, fmt.Sprintf("%s: %v", name, err))
		}
	}

	bps := uint32(cfg.BitrateKbps * 1000)

	// The bitrate is **always a cap**, whatever the criterion by which the bits
	// are spent: it is the bandwidth the network has declared it can carry, and
	// exceeding it does not produce a better picture but lost packets.
	switch cfg.RateControl {
	case RateQuality:
		// The quality is fixed and the bitrate follows the scene — but with the
		// cap, otherwise a sudden movement would produce a spike the network
		// cannot carry.
		note("rate control mode", e.codec.setUINT32(codecRateControlMode, rateControlQuality))
		q := cfg.Quality
		if q <= 0 || q > 100 {
			q = DefaultQuality
		}
		note("quality", e.codec.setUINT32(codecQuality, uint32(q)))
		note("max bitrate", e.codec.setUINT32(codecMaxBitRate, bps))
	case RateCapped:
		// The three pieces go together and none of them does the job alone: the
		// mode because it is the only one where the cap holds, the cap because
		// it is the network's limit, and the quantiser floor because it is the
		// quality beyond which spending makes no sense.
		note("rate control mode", e.codec.setUINT32(codecRateControlMode, rateControlPeakConstrainedVBR))
		note("mean bitrate", e.codec.setUINT32(codecMeanBitRate, bps))
		note("max bitrate", e.codec.setUINT32(codecMaxBitRate, bps))
		q := cfg.MinQP
		if q <= 0 || q > 51 {
			q = DefaultMinQP
		}
		note("min quantiser", e.codec.setUINT32(codecMinQP, uint32(q)))
	default:
		// Constant bitrate: on a link with a given bandwidth it is what fills
		// the pipe without bursting it.
		note("rate control mode", e.codec.setUINT32(codecRateControlMode, rateControlCBR))
		note("mean bitrate", e.codec.setUINT32(codecMeanBitRate, bps))
	}

	// The leaky-bucket reservoir is worth one second of stream. Bigger absorbs
	// difficult scenes better, but it is extra latency.
	//
	// The quantiser ceiling applies in **every** mode, and has to be imposed
	// before starting. Without it an encoder can refuse to approximate beyond a
	// certain point and overshoot the bitrate to hold the quality — measured on
	// AMD, 4295 kbit/s with 2500 requested and the quantiser never above 29.
	if cfg.MaxQP > 0 && cfg.MaxQP <= 51 {
		note("max quantiser", e.codec.setUINT32(codecMaxQP, uint32(cfg.MaxQP)))
	}

	note("buffer size", e.codec.setUINT32(codecBufferSize, bps))

	// Without low latency the encoder may accumulate several frames to work on
	// them in parallel: excellent for a file, terrible for a monitor. One frame
	// in, one frame out.
	note("low latency", e.codec.setBool(codecLowLatencyMode, true))

	// **The stream has to stay readable, and a default is not a guarantee.**
	// We ask for Baseline, which forbids both of these, but a profile is a
	// request like any other and this file is a monument to requests that were
	// accepted and not executed.
	//
	// What each costs if it slips through is a silence, not an error. With
	// CABAC on, ParsePPS refuses the PPS and the quantiser reading stops for
	// the whole session: the bitrate loop sits at the cap, the saving is lost
	// and nothing is written anywhere. B frames were worse until the refusal
	// added beside it in media.QPSlice — they were read, and read wrong.
	//
	// **They go through the notes, and that is the right list.** `codecNotes`
	// means "a setting I applied was refused", which is what these are: two
	// SetValue calls like every other in this function. What is deliberately
	// kept out of it is further down — `keyFrameNote` and `bitrateNote`, which
	// report what the encoder **declares** about a property nothing is setting,
	// and where E_NOTIMPL means "I do not answer that" rather than no.
	//
	// And a refusal here is worth the line. If CABAC cannot be turned off on an
	// encoder that uses it, ParsePPS refuses the PPS and the quantiser reading
	// dies for the whole session: one line at capture start is cheap against a
	// silence that lasts all night.
	note("entropy coding", e.codec.setBool(codecCABAC, false))
	note("B frames", e.codec.setUINT32(codecBPictureCount, 0))

	if cfg.GOPFrames > 0 {
		note("keyframe distance", e.codec.setUINT32(codecGOPSize, uint32(cfg.GOPFrames)))
	}
	if cfg.QualityVsSpeed > 0 {
		note("quality vs speed", e.codec.setUINT32(codecQualityVsSpeed, uint32(cfg.QualityVsSpeed)))
	}

	// The keyframe on request is not set here — it is asked for when it is
	// needed — but what the encoder declares about it is noted, because it is
	// the first thing to look at when the picture stays frozen.
	//
	// Deliberately outside codecNotes: that list means "settings refused" and
	// raises a warning. Nothing is being set here, and the AMD encoder answers
	// E_NOTIMPL to IsModifiable while producing keyframes perfectly well —
	// measured, 48 ms over 24 requests. Warning there would mean alarming every
	// AMD user about a question their encoder does not answer.
	e.keyFrameNote = declarationNote(
		e.codec.isSupported(codecForceKeyFrame),
		e.codec.isModifiable(codecForceKeyFrame))

	// The same for the bitrate, and for a measured reason: on this machine the
	// congestion control asked for **300 kbit/s for twenty seconds** while the
	// encoder was producing 2500, and the cellular link lost 33% of the
	// packets. SetValue had answered S_OK to every command.
	//
	// Knowing what the encoder declares solves nothing on its own — the proof is
	// still weighing the frames, which is what RunBitrateControl does — but it
	// tells "I cannot change it while running" from "I say yes and do not do
	// it", which lead to two different remedies.
	e.bitrateNote = declarationNote(
		e.codec.isSupported(codecMeanBitRate),
		e.codec.isModifiable(codecMeanBitRate))
	return nil
}

// declarationNote sums up what the encoder declares about a property.
func declarationNote(supported, modifiable error) string {
	switch {
	case supported != nil:
		return fmt.Sprintf("not declared (%v)", supported)
	case modifiable != nil:
		// E_NOTIMPL here means "I do not answer that question", not "no".
		return fmt.Sprintf("declared; no answer on changing it while running (%v)", modifiable)
	}
	return "declared, and changeable while running"
}

// KeyFrameNote reports what the encoder declares about the keyframe on request.
//
// It is a declaration, not a proof: whoever wants to know whether it really
// works has to look at whether the keyframe comes out, and that is what the
// pipeline does.
func (e *VideoEncoder) KeyFrameNote() string {
	if e.keyFrameNote == "" {
		return "not queried"
	}
	return e.keyFrameNote
}

// BitrateNote reports what the encoder declares about changing the bitrate
// while running.
//
// As with the keyframe, it is a declaration and not a proof: whoever wants to
// know whether the command takes has to weigh the frames that come out.
func (e *VideoEncoder) BitrateNote() string {
	if e.bitrateNote == "" {
		return "not queried"
	}
	return e.bitrateNote
}

// SetBitrate changes the bitrate while the encoder is working.
//
// It is half the reason this package exists: on 5G the bandwidth swings
// constantly, and a fixed bitrate either wastes or loses packets.
func (e *VideoEncoder) SetBitrate(kbps int) error {
	if e.codec == nil {
		return fmt.Errorf("this encoder exposes no ICodecAPI")
	}
	bps := uint32(kbps * 1000)

	// The reservoir moves with the bitrate: in CBR it is what decides how many
	// bits a frame may spend, and leaving it sized for the old value would be
	// incoherent. On its own, though, it **is not enough** — tried: with Quick
	// Sync the throughput does not shift by a single kbit.
	if err := e.codec.setUINT32(codecBufferSize, bps); err != nil {
		return err
	}

	// In constant quality the number that governs is not the mean but the
	// **cap**: the mean is not even looked at, and changing it would leave the
	// encoder free to exceed the bandwidth the network has declared it can
	// carry. It is the kind of slip that gives no error and shows up only as
	// lost packets.
	if e.cfg.RateControl == RateQuality {
		return e.codec.setUINT32(codecMaxBitRate, bps)
	}
	// In capped CRF **both** move: the cap because it is the network's limit,
	// the mean because in PeakConstrainedVBR it is the target the encoder works
	// around. Moving only one would leave the two telling different stories —
	// and which of them the encoder listens to is not something to guess at.
	if e.cfg.RateControl == RateCapped {
		if err := e.codec.setUINT32(codecMaxBitRate, bps); err != nil {
			return err
		}
	}
	return e.codec.setUINT32(codecMeanBitRate, bps)
}

// ReconfigureBitrate changes the bitrate by reconfiguring the transform instead
// of asking it.
//
// It exists because there are encoders that accept SetBitrate and do not carry
// it out, and that is no corner case: it is Quick Sync, that is, the integrated
// GPU of nearly every laptop. The defect has been documented since 2013 on the
// same DLL, Intel has confirmed it as a limit of its *fixed function* MFT, and
// in thirteen years of drivers it has not changed. Chrome falls for it too: its
// Windows encoder calls SetValue and nothing else, so on this hardware even
// Chrome's WebRTC transmits more than the network can carry.
//
// The mechanical reason also says why this route ought to work: underneath the
// MFT is Media SDK, where the bitrate is changed **only** with
// MFXVideoENCODE_Reset. The MFT does not take that route when it receives
// SetValue; reassigning its output type asks it the same thing by the way it is
// obliged to honour — and indeed at start-up the number takes exactly.
//
// It is not free, and it is to be called only once the other route has been
// **measured** inert: the transform stops and restarts, which costs a keyframe
// and a few frames of transient.
func (e *VideoEncoder) ReconfigureBitrate(kbps int) error {
	if kbps <= 0 {
		return fmt.Errorf("invalid bitrate: %d", kbps)
	}
	cfg := e.cfg
	cfg.BitrateKbps = kbps
	return e.reconfigure(cfg)
}

// There is no method here for changing the frame **size**, and that is
// deliberate: this route does not work. Tried and measured, the transform
// accepts the new format, reads it back, and goes on producing the old SPS. The
// resolution scale rebuilds the encoder instead.

// reconfigure stops the transform, reassigns its formats and restarts it.
func (e *VideoEncoder) reconfigure(cfg VideoEncoderConfig) error {
	// The format is not reassigned with the transform running: it is stopped
	// first, in the reverse order to the one it was started in. The flush
	// discards the frames in flight, which is what we want — they are frames
	// coded with the old bitrate, that is, precisely the ones the network has
	// no use for.
	_ = e.t.processMessage(msgCommandFlush, 0)
	_ = e.t.processMessage(msgNotifyEndOfStream, 0)
	_ = e.t.processMessage(msgNotifyEndStreaming, 0)

	if err := e.negotiate(cfg); err != nil {
		return fmt.Errorf("reconfiguration to %dx%d, %d kbit/s: %w",
			cfg.Width, cfg.Height, cfg.BitrateKbps, err)
	}
	// Read back, not taken on trust. SetOutputType can answer S_OK and leave
	// the format where it was: it is the same thing SetValue does with the
	// bitrate, and here it would be more insidious, because the stream would go
	// on coming out at the old size while the rest of the program believes it
	// has changed scale. The caller can then back out instead of feeding a
	// misaligned pipeline.
	if got, err := e.t.outputCurrentType(); err == nil {
		gw, gh, gerr := got.FrameSize()
		got.Release()
		if gerr == nil && (gw != cfg.Width || gh != cfg.Height) {
			return fmt.Errorf("the encoder accepted %dx%d and kept %dx%d",
				cfg.Width, cfg.Height, gw, gh)
		}
	}
	e.cfg = cfg

	// The ICodecAPI is realigned too: the output type and the codec properties
	// are two distinct stores describing the same thing, and leaving one behind
	// means relying on which of the two the encoder looks at.
	if e.codec != nil {
		bps := uint32(cfg.BitrateKbps * 1000)
		_ = e.codec.setUINT32(codecBufferSize, bps)
		_ = e.codec.setUINT32(codecMeanBitRate, bps)
	}

	// **The previous session's events have to be thrown away before starting
	// again.**
	//
	// The channel that carries them is buffered, so the stop does not empty it:
	// inside are permissions — "there is a frame ready" — referring to a
	// transform that no longer exists. Consuming one means calling ProcessOutput
	// without the right to, and the prescribed answer is E_UNEXPECTED, which the
	// video loop reads as a fault and turns into a capture restart.
	//
	// Measured on AMD: as soon as the watch switched to this route, three
	// restarts in half a minute with ProcessOutput answering HRESULT
	// 0x8000FFFF. It is the same invariant as always — a request is good once
	// only — crossed here by a restart rather than by a loop.
	if e.cb != nil {
		e.cb.drain()
	}
	return e.startStreaming()
}

// ForceKeyFrame asks for the next frame to be a keyframe.
//
// It is the other half: when the browser reports a loss with a PLI, without
// this the picture stays frozen until the periodic keyframe.
//
// The value is a UINT32, not a boolean, however much "force a keyframe" is a
// yes-or-no question. Passed as VT_BOOL the Intel encoder answers S_OK and
// produces nothing: measured, twelve requests and no keyframe beyond the usual
// ones. What decides is the type declared by the property, and this one is
// declared ULONG.
//
// The boolean fallback stays because tomorrow's machine is not this one: an
// encoder from another vendor might refuse the UINT32 and accept the boolean.
// The other is only tried if the first gives an error — if it answers S_OK and
// does nothing, the only way to notice is to look at whether the keyframe
// arrived, and that is what the pipeline does.
func (e *VideoEncoder) ForceKeyFrame() error {
	if e.codec == nil {
		return fmt.Errorf("this encoder exposes no ICodecAPI")
	}
	err := e.codec.setUINT32(codecForceKeyFrame, 1)
	if err == nil {
		return nil
	}
	if err2 := e.codec.setBool(codecForceKeyFrame, true); err2 == nil {
		return nil
	}
	return err
}

// CodecNotes lists the settings the encoder did not accept.
//
// **It is text to show, not a value to decide on**: whoever wants to know
// whether everything went well asks CodecSettingsAccepted. Judging by comparing
// this sentence with "all accepted" breaks the moment the sentence is
// translated, and it breaks in silence — no compilation error, no red test, and
// a WARN on every capture start announcing refused settings while carrying "all
// accepted" along as the detail. It is the MicHealth family: **a sentence in one
// language is a decision taken in that language**, and it survives only as long
// as nobody translates.
func (e *VideoEncoder) CodecNotes() string {
	if len(e.codecNotes) == 0 {
		return "all accepted"
	}
	return strings.Join(e.codecNotes, "; ")
}

// CodecSettingsAccepted says whether the encoder accepted everything.
func (e *VideoEncoder) CodecSettingsAccepted() bool {
	return len(e.codecNotes) == 0
}
