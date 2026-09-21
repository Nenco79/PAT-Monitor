//go:build windows

// Package audio captures audio straight from WASAPI in raw mode.
//
// Why opening the microphone the way anyone would is not enough: on this class
// of machine the OEMs install noise-suppression Audio Processing Objects (APOs)
// that live in the Windows capture path, upstream of any application. On the
// development laptop that is "Elevoc Audio Effects Component", whose technical
// material explicitly lists babies crying among the sounds to filter out: for a
// baby monitor that is exactly the signal that is wanted.
//
// AUDCLNT_STREAMOPTIONS_RAW asks Windows for a stream that skips the whole
// processing chain, leaving only what is always on in the hardware. That way
// the app does not depend on the "Audio enhancements" switch, which a driver
// update can turn back on without warning.
package audio

import (
	"context"
	"errors"
	"fmt"
	"time"
	"unsafe"

	"github.com/go-ole/go-ole"
	"github.com/moutend/go-wca/pkg/wca"
	"golang.org/x/sys/windows"

	"patmonitor/internal/wincom"
)

// WAVE format tags.
const (
	waveFormatPCM        = 0x0001
	waveFormatIEEEFloat  = 0x0003
	waveFormatExtensible = 0xFFFE

	// The size of WAVEFORMATEX in C: 18 bytes, packed. In Go the same struct is
	// aligned to 20, so the offsets of the WAVEFORMATEXTENSIBLE fields have to
	// be worked out by hand rather than with unsafe.Sizeof.
	waveFormatExSizeC = 18
	// Inside WAVEFORMATEXTENSIBLE the SubFormat GUID follows
	// wValidBitsPerSample (2 bytes) and dwChannelMask (4 bytes).
	subFormatOffset = waveFormatExSizeC + 2 + 4
)

// audioCategoryOther is AudioCategory_Other: no special semantics, so Windows
// applies none of the communication-related treatments.
const audioCategoryOther = 0

// StreamFormat describes the native format the endpoint delivers.
//
// WASAPI is not asked to convert: the downmix is done downstream by ToMonoS16.
// Asking for a conversion here would mean putting back pieces of the audio
// engine we are going around — that is, reinserting exactly what raw mode
// exists to get out of.
type StreamFormat struct {
	SampleRate    int
	Channels      int
	BitsPerSample int
	Float         bool
}

// BytesPerFrame is the size of one frame (one sample per channel).
func (f StreamFormat) BytesPerFrame() int {
	return f.Channels * f.BitsPerSample / 8
}

// BytesPerSecond is the throughput of the uncompressed stream.
//
// It is there to size buffers in units of time rather than of bytes: between a
// four-capsule floating-point array and a 16-bit mono microphone the throughput
// changes eightfold, so the same quantity of bytes is worth entirely different
// intervals from one machine to the next.
func (f StreamFormat) BytesPerSecond() int {
	return f.SampleRate * f.BytesPerFrame()
}

func (f StreamFormat) String() string {
	kind := "int"
	if f.Float {
		kind = "float"
	}
	return fmt.Sprintf("%d Hz, %d channels, %d bit %s", f.SampleRate, f.Channels, f.BitsPerSample, kind)
}

// CaptureDevice is a WASAPI capture endpoint.
type CaptureDevice struct {
	ID        string
	Name      string
	IsDefault bool
}

// Options configures the opening of the stream.
type Options struct {
	// An empty DeviceID means the default capture endpoint. An ID that is not
	// among the active ones is not an error: the default is opened, and what
	// says which device was really opened is the Stream. See openDevice.
	DeviceID string
	// Raw asks for AUDCLNT_STREAMOPTIONS_RAW. If the endpoint does not support
	// it, FallbackOnRawFailure decides whether to carry on regardless.
	Raw bool
	// Exclusive opens straight in exclusive mode, without going through raw.
	// The measuring tools need it, to compare the two routes under the same
	// conditions; in service, exclusive mode is reached as a fallback.
	Exclusive bool
	// FallbackOnRawFailure allows the stream to be opened in normal mode when
	// raw is refused. Use it with care: it means accepting the OEM's
	// processing.
	FallbackOnRawFailure bool
}

// Stream sums up the state of an open stream.
type Stream struct {
	Format     StreamFormat
	DeviceName string
	DeviceID   string
	// RawMode says whether the signal arrives without going through the OEM's
	// effects. There are two different routes to it: see Mode.
	RawMode bool
	// Mode says by which route, and it has to be reported: "raw" and
	// "exclusive" both give the clean signal but have different consequences —
	// exclusive stops other applications using the microphone.
	Mode string
	// GainDB is the gain whoever consumes the samples has to apply.
	//
	// In shared mode the audio engine applies the endpoint volume itself; in
	// exclusive mode the engine is not there, and that gain goes with it. On
	// this machine it is **30 dB** — the slider at 100% of a scale that goes to
	// +30 — and without reapplying it the audio is heard six times quieter than
	// before while not being filtered. Reapplying it here reproduces what the
	// engine would have done, and it follows the user's slider instead of
	// inventing a constant.
	GainDB float64
	// Muted reports the endpoint's mute, which in exclusive mode is stepped
	// over just like the volume. It has to be respected: it is an explicit
	// choice by whoever uses the PC.
	Muted bool
	// Volume describes the endpoint's control in full. It is for diagnosis: how
	// much gain there is and whether it is in hardware varies from machine to
	// machine, and it is the first thing to look at when a microphone is too
	// quiet.
	Volume VolumeInfo
	// VolumeError is the reason the endpoint's gain could not be read, and it
	// is set **only** when that costs something: in exclusive mode, where the
	// audio engine applies nothing and the gain it would have applied goes with
	// it. Elsewhere an unreadable control changes nothing about what is heard.
	//
	// It exists because a zero gain says two different things — "this endpoint
	// has no gain to recover" and "nobody could ask" — and the second is the
	// one that costs a night: the audio arrives clean and six times quieter,
	// with a green page and a still meter above it.
	VolumeError error
	// RawError is the reason raw mode was not obtained, when that is how it
	// went.
	//
	// Discarding it leaves the most serious degradation this program can suffer
	// — audio going back through the OEM's effects, that is, the filter that
	// zeroes a baby's crying — without an explanation. A silent fallback is
	// worse than a fault: there is not even anything to investigate.
	//
	// **A non-nil RawError does not mean the audio is filtered**, and the
	// difference matters to whoever reads this next: with exclusive mode
	// obtained, `RawMode` is true and this still carries the refusal of *raw*,
	// which is the road that was tried first. The question "is the signal going
	// through the OEM's effects?" is `RawMode`, and that is what both consumers
	// gate on; this field says why the first road was not taken.
	RawError error
}

// comThread runs fn on a thread of its own, with COM initialised in STA.
//
// **The apartment is the only thing this package decides here**, and STA is the
// one WASAPI objects expect to be created in — unlike video capture, which
// wants MTA for the internal queue of the asynchronous transforms. The rest —
// that S_FALSE and RPC_E_CHANGED_MODE are not faults, and that only the first
// has to be balanced — lives in wincom, and only there: scattered, the copies
// diverge, and TestNobodyReadsTheOutcomeOnTheirOwn refuses a second reader.
//
// Accepted preserves what this path already did: in someone else's apartment
// the WASAPI objects are created just the same, and there is no event queue
// here demanding a message loop — unlike video capture, which answers Required
// to the same question.
//
// It stays a function rather than calling wincom.Thread from the five places
// that use it: that way apartment and policy are declared once per package,
// which is exactly what must not be able to scatter.
func comThread(fn func() error) error {
	return wincom.Thread(wincom.STA, wincom.Accepted, fn)
}

// newDeviceEnumerator creates the audio endpoint enumerator. It has to be
// called inside comThread: the object returned belongs to the calling thread.
func newDeviceEnumerator() (*wca.IMMDeviceEnumerator, error) {
	var enum *wca.IMMDeviceEnumerator
	if err := wca.CoCreateInstance(
		wca.CLSID_MMDeviceEnumerator, 0, ole.CLSCTX_ALL,
		wca.IID_IMMDeviceEnumerator, &enum,
	); err != nil {
		return nil, fmt.Errorf("IMMDeviceEnumerator creation: %w", describeAudclnt(err))
	}
	return enum, nil
}

// ListCaptureDevices lists the active capture endpoints.
func ListCaptureDevices() ([]CaptureDevice, error) {
	var out []CaptureDevice
	err := comThread(func() error {
		enum, err := newDeviceEnumerator()
		if err != nil {
			return err
		}
		defer enum.Release()

		// The default device's ID is only used to mark it in the list: not
		// having a default is not an error.
		defaultID := ""
		var def *wca.IMMDevice
		if err := enum.GetDefaultAudioEndpoint(wca.ECapture, wca.EConsole, &def); err == nil {
			_ = def.GetId(&defaultID)
			def.Release()
		}

		var coll *wca.IMMDeviceCollection
		if err := enum.EnumAudioEndpoints(wca.ECapture, wca.DEVICE_STATE_ACTIVE, &coll); err != nil {
			return fmt.Errorf("EnumAudioEndpoints: %w", describeAudclnt(err))
		}
		defer coll.Release()

		var count uint32
		if err := coll.GetCount(&count); err != nil {
			return fmt.Errorf("GetCount: %w", describeAudclnt(err))
		}
		for i := uint32(0); i < count; i++ {
			var dev *wca.IMMDevice
			if err := coll.Item(i, &dev); err != nil {
				continue
			}
			var id string
			_ = dev.GetId(&id)
			name := deviceFriendlyName(dev)
			dev.Release()

			out = append(out, CaptureDevice{ID: id, Name: name, IsDefault: id != "" && id == defaultID})
		}
		return nil
	})
	return out, err
}

// deviceFriendlyName reads the endpoint's readable name; if it is not available
// it returns a placeholder rather than failing.
func deviceFriendlyName(dev *wca.IMMDevice) string {
	var ps *wca.IPropertyStore
	if err := dev.OpenPropertyStore(wca.STGM_READ, &ps); err != nil {
		return "(name unavailable)"
	}
	defer ps.Release()

	var pv wca.PROPVARIANT
	if err := ps.GetValue(&wca.PKEY_Device_FriendlyName, &pv); err != nil {
		return "(name unavailable)"
	}
	if s := pv.String(); s != "" {
		return s
	}
	return "(name unavailable)"
}

// Capture opens the endpoint and runs the capture loop until the context is
// cancelled.
//
// onStart is called once only, before the first data, with the actual format:
// the caller uses it to configure the consumer, which has to know what rate to
// create the encoder at. onData receives the PCM blocks in the endpoint's
// native format; the buffer is valid only for the duration of the call and has
// to be copied if it is needed later.
//
// rawAttempts is how many times raw mode is insisted on before giving up.
//
// That is not fussiness: the fallback is not an equivalent alternative, it is
// the audio going back through the OEM's effects. On this class of machine that
// means a filter that zeroes a baby's crying, that is, a baby monitor that says
// nothing. Half a second is worth spending before accepting it.
//
// The refusal can be transient: reopening the microphone right after closing it
// — which happens on every capture restart — the endpoint can say no and then
// say yes an instant later.
const (
	rawAttempts = 3
	rawRetryGap = 250 * time.Millisecond
)

// describeAudclnt makes a WASAPI error readable.
//
// Without it the refusal arrives as "error 2290679847" followed by a complaint
// from FormatMessage: the AUDCLNT codes are not in the system message table. A
// decimal number cannot be looked up anywhere, while the hexadecimal and the
// name lead straight to the documentation — and this error is read when the
// monitor has gone silent, which is not the moment to start converting bases.
func describeAudclnt(err error) error {
	var oe *ole.OleError
	if !errors.As(err, &oe) {
		return err
	}
	code := uint32(oe.Code())
	// **E_ACCESSDENIED leaves here as a value, not as a sentence.** It is the
	// one code in this function that whoever is upstream has to be able to
	// *decide* on rather than merely print: a microphone the user has taken
	// away is not a microphone that has broken, and the two want different
	// words on the page and different remedies. The hexadecimal stays first
	// all the same, by this function's own rule.
	if wincom.DeniedHRESULT(uintptr(code)) {
		return fmt.Errorf("HRESULT 0x%08X: %w", code, wincom.ErrDenied)
	}
	if name := audclntName(code); name != "" {
		return fmt.Errorf("%s (0x%08X)", name, code)
	}
	return fmt.Errorf("HRESULT 0x%08X", code)
}

// audclntName translates the codes that can be expected here. The values come
// from audioclient.h, not from memory.
func audclntName(code uint32) string {
	switch code {
	case 0x88890004:
		return "AUDCLNT_E_DEVICE_INVALIDATED: the device was removed or reconfigured"
	case 0x8889000A:
		return "AUDCLNT_E_DEVICE_IN_USE: another application holds it in exclusive mode"
	case 0x88890026:
		return "AUDCLNT_E_RESOURCES_INVALIDATED"
	case 0x88890027:
		return "AUDCLNT_E_RAW_MODE_UNSUPPORTED: the endpoint does not grant raw mode; " +
			"the same effect comes from turning off the microphone's audio enhancements"
	case 0x88890028:
		return "AUDCLNT_E_ENGINE_PERIODICITY_LOCKED"
	case 0x88890029:
		return "AUDCLNT_E_ENGINE_FORMAT_LOCKED"
	}
	return ""
}

// openExclusive opens the endpoint in exclusive mode and returns the client,
// already initialised, together with the format it accepted.
//
// It is the second route to the unprocessed signal: in exclusive mode the
// application talks straight to the driver, the Windows audio engine takes no
// part and therefore neither does any APO. Measured the day raw mode was
// refused with AUDCLNT_E_RAW_MODE_UNSUPPORTED and the shared path delivered
// exact zeroes: in exclusive mode, RMS -63 dBFS, that is, the room.
//
// **Whether raw is granted is a state of the endpoint, not a property of the
// machine**, and this comment used to say "measured on this machine, where raw
// mode is refused" — a sentence that was true when it was written and stopped
// being true without anybody touching a line. The same laptop gave mode=raw in
// baselines/capture-intel.txt and refused it later with the same Intel driver
// and the same Elevoc APO build: what happened in between is that a Windows
// update re-installed the endpoint and the four APO components. So this route
// is not a remedy for one laptop, and the shared path delivering zeroes is not
// part of the condition either — raw has been refused with shared measuring RMS
// -38.7 dBFS. What holds is the refusal, whenever it comes.
//
// The format is dictated by the device, not by the engine: the mix format is
// the engine's own result and in exclusive mode it is refused with
// AUDCLNT_E_UNSUPPORTED_FORMAT. It has to be read from
// PKEY_AudioEngine_DeviceFormat, and it is typically integer PCM while the mix
// format is float — reading one as the other produces denormal values, which
// look a great deal like silence.
func openExclusive(dev *wca.IMMDevice) (*wca.IAudioClient2, *wca.WAVEFORMATEX, error) {
	wfx, err := deviceFormat(dev)
	if err != nil {
		return nil, nil, err
	}

	var client *wca.IAudioClient2
	if err := dev.Activate(wca.IID_IAudioClient2, ole.CLSCTX_ALL, nil, &client); err != nil {
		return nil, nil, fmt.Errorf("Activate: %w", describeAudclnt(err))
	}
	if err := client.IsFormatSupported(wca.AUDCLNT_SHAREMODE_EXCLUSIVE, wfx, nil); err != nil {
		client.Release()
		return nil, nil, fmt.Errorf("device format rejected in exclusive mode: %w",
			describeAudclnt(err))
	}

	var def, min wca.REFERENCE_TIME
	if err := client.GetDevicePeriod(&def, &min); err != nil {
		client.Release()
		return nil, nil, fmt.Errorf("GetDevicePeriod: %w", describeAudclnt(err))
	}

	// The **default** period is asked for, not the minimum.
	//
	// The minimum here is 2 ms, and it would wake the capture five times more
	// often than necessary to deliver crumbs: measured, 95% of the deliveries
	// arrived in bursts instead of at a cadence. A baby monitor does not need
	// two milliseconds of latency, it needs regular deliveries — and the
	// default period is the one shared mode works with too.
	//
	// If the alignment does not work out, Windows says so and we start again
	// with the size it worked out: it is a prescribed dance.
	err = client.Initialize(wca.AUDCLNT_SHAREMODE_EXCLUSIVE,
		wca.AUDCLNT_STREAMFLAGS_EVENTCALLBACK, def, def, wfx, nil)
	if isNotAligned(err) {
		var frames uint32
		if e := client.GetBufferSize(&frames); e == nil && frames > 0 && wfx.NSamplesPerSec > 0 {
			aligned := wca.REFERENCE_TIME(float64(10000000)/float64(wfx.NSamplesPerSec)*float64(frames) + 0.5)
			client.Release()
			client = nil
			if e := dev.Activate(wca.IID_IAudioClient2, ole.CLSCTX_ALL, nil, &client); e != nil {
				return nil, nil, fmt.Errorf("Activate after realignment: %w",
					describeAudclnt(e))
			}
			err = client.Initialize(wca.AUDCLNT_SHAREMODE_EXCLUSIVE,
				wca.AUDCLNT_STREAMFLAGS_EVENTCALLBACK, aligned, aligned, wfx, nil)
		}
	}
	if err != nil {
		if client != nil {
			client.Release()
		}
		return nil, nil, describeAudclnt(err)
	}
	return client, wfx, nil
}

// VolumeInfo describes the endpoint's volume control.
type VolumeInfo struct {
	MinDB, MaxDB, CurrentDB float64
	Muted                   bool
	// Hardware says whether the gain is applied by the device rather than in
	// software. The difference matters: analogue gain before the converter
	// improves the signal-to-noise ratio, digital gain raises signal and noise
	// together and so adds nothing we could not do ourselves downstream.
	Hardware bool
	// Available is false when the endpoint exposes no control at all.
	Available bool
	// Why explains why it is not available. A "there is none" with no reason is
	// the thing that wastes the most time.
	Why string
}

func endpointVolumeInfo(dev *wca.IMMDevice) (VolumeInfo, error) {
	var info VolumeInfo
	var vol *wca.IAudioEndpointVolume
	if err := dev.Activate(wca.IID_IAudioEndpointVolume, ole.CLSCTX_ALL, nil, &vol); err != nil {
		// **The error carries the readable form too, not only Why.** Both are
		// read: Why by pat-diag, the error by pat-wasapi and by the capture,
		// which puts it in the line that explains a 30 dB loss on the exclusive
		// path. Returning the raw one there printed a bare decimal HRESULT — the
		// exact defect the describeAudclnt sweep exists for, by the one road that
		// sweep's guard does not read, a bare `return` rather than a wrap.
		err = fmt.Errorf("Activate IAudioEndpointVolume: %w", describeAudclnt(err))
		info.Why = err.Error()
		return info, err
	}
	defer vol.Release()
	info.Available = true

	var level float32
	if err := vol.GetMasterVolumeLevel(&level); err == nil {
		info.CurrentDB = float64(level)
	}
	var min, max, incr float32
	if err := vol.GetVolumeRange(&min, &max, &incr); err == nil {
		info.MinDB, info.MaxDB = float64(min), float64(max)
	} else {
		// **A control whose range cannot be read is not a control.** The one
		// question this struct exists to answer is how much gain there is to
		// recover, and that is `MaxDB`: left at zero by a failed read it says
		// "there is nothing to recover", which is indistinguishable from an
		// endpoint that really has no gain. It is the same conflation as a
		// meter that draws silence when it cannot measure.
		info.Available = false
		err = fmt.Errorf("GetVolumeRange: %w", describeAudclnt(err))
		info.Why = err.Error()
		info.Muted = comBool(vol.GetMute)
		return info, err
	}
	var support uint32
	if err := vol.QueryHardwareSupport(&support); err == nil {
		const endpointHardwareSupportVolume = 0x00000001
		info.Hardware = support&endpointHardwareSupportVolume != 0
	}
	info.Muted = comBool(vol.GetMute)
	return info, nil
}

// comBool reads a BOOL output parameter without getting the neighbouring memory
// corrupted.
//
// The Windows BOOL is a 32-bit integer; the Go bool takes one byte. Passing the
// address of a bool to a COM method that writes a BOOL means writing **four**
// bytes where there is room for one, and the next three are whatever the
// compiler put alongside.
//
// It is not theory: passing &info.Muted, GetMute's write zeroed the two fields
// that followed in the struct — Hardware and Available — and the result was a
// volume control read perfectly and declaring itself non-existent. A fault that
// looks in every way like an unsupported interface.
func comBool(get func(*bool) error) bool {
	var slot int32
	if err := get((*bool)(unsafe.Pointer(&slot))); err != nil {
		return false
	}
	return slot != 0
}

// The volume control is queried once only, from inside the opening: from a
// second handle, while the endpoint is held exclusively, the activation fails.
// The result travels in the Stream.

// sensitivityGain is the gain to apply downstream so that sensitivity is always
// the endpoint's maximum, whatever the Windows slider says.
//
// The microphone slider is a system setting, and it moves for reasons that have
// nothing to do with a baby monitor: a meeting, a game, an application that
// turns it down by itself. Inheriting it means the monitor can go deaf because
// of something done the day before, with nobody connecting the two things. So
// we decide the sensitivity, and whoever wants it otherwise uses mic_gain_db,
// which is a knob of this program and not of Windows.
//
// The real slider is not touched: changing it would change the microphone
// volume for every other application on the PC, and nobody expects that of a
// program that is only supposed to watch a room.
//
// alreadyApplied says whether the audio engine has already applied the current
// volume: it has in shared mode, not in exclusive.
func sensitivityGain(currentDB, maxDB float64, alreadyApplied bool) float64 {
	g := maxDB
	if alreadyApplied {
		g = maxDB - currentDB
	}
	if g < 0 {
		// The slider is past the declared maximum: nothing is turned down,
		// because that would take away signal the user asked for.
		return 0
	}
	return g
}

func isNotAligned(err error) bool {
	var oe *ole.OleError
	return errors.As(err, &oe) && uint32(oe.Code()) == 0x88890019 // AUDCLNT_E_BUFFER_SIZE_NOT_ALIGNED
}

// deviceFormat reads the device's native format from
// PKEY_AudioEngine_DeviceFormat.
//
// The property is a BLOB and go-ole does not expose BLOBs, so the PROPVARIANT
// is read by hand: after the 8 bytes of header come the size and the pointer to
// the data. The contents are copied into Go memory, so they stay valid even
// after the PROPVARIANT has been released.
func deviceFormat(dev *wca.IMMDevice) (*wca.WAVEFORMATEX, error) {
	var ps *wca.IPropertyStore
	if err := dev.OpenPropertyStore(wca.STGM_READ, &ps); err != nil {
		return nil, fmt.Errorf("OpenPropertyStore: %w", describeAudclnt(err))
	}
	defer ps.Release()

	var pv wca.PROPVARIANT
	if err := ps.GetValue(&wca.PKEY_AudioEngine_DeviceFormat, &pv); err != nil {
		return nil, fmt.Errorf("PKEY_AudioEngine_DeviceFormat: %w", describeAudclnt(err))
	}
	// data is declared unsafe.Pointer and not uintptr on purpose: the memory
	// belongs to COM and not to the Go heap, but going through a uintptr would
	// need a conversion that go vet rightly flags as suspicious.
	type blobVal struct {
		size uint32
		_    uint32
		data unsafe.Pointer
	}
	b := (*blobVal)(unsafe.Add(unsafe.Pointer(&pv), 8))
	if b.data == nil || b.size == 0 {
		return nil, fmt.Errorf("the device declares no native format")
	}
	buf := make([]byte, b.size)
	copy(buf, unsafe.Slice((*byte)(b.data), b.size))
	if err := validateFormatBlob(buf); err != nil {
		return nil, err
	}
	return (*wca.WAVEFORMATEX)(unsafe.Pointer(&buf[0])), nil
}

// validateFormatBlob checks that a WAVEFORMATEX really holds the bytes it says
// it does.
//
// **The size was checked against 18 and the reading was authorised by cbSize**,
// which is a number inside the blob. WAVEFORMATEX is eighteen bytes and
// declares how many more follow it; decodeFormat reads the SubFormat GUID at
// offset 24, which needs forty — so a blob of eighteen bytes declaring cbSize
// 22 sent that read twenty-two bytes past the end of a Go slice, into whatever
// the allocator had put there. The same truncated pointer is handed to
// IsFormatSupported, where it is WASAPI doing the reading.
//
// Nobody crafts this one: it comes from the audio driver through
// PKEY_AudioEngine_DeviceFormat. That is the argument for the check and not
// against it — it is a structure from outside the program whose length is
// asserted by its own contents, and this program runs on machines whose drivers
// nobody here has seen.
//
// **It takes the bytes and not the device**, so both directions can be tested:
// a function that interrogates the operating system can only be run, never
// tested, and it would run on the disk of whoever runs the tests.
func validateFormatBlob(b []byte) error {
	if len(b) < waveFormatExSizeC {
		return fmt.Errorf("audio: the device declares a format of %d bytes, "+
			"fewer than the %d of a WAVEFORMATEX", len(b), waveFormatExSizeC)
	}
	// cbSize is the last field of the packed C structure, at offset 16.
	extra := int(b[16]) | int(b[17])<<8
	if n := waveFormatExSizeC + extra; len(b) < n {
		return fmt.Errorf("audio: the device declares %d bytes of format beyond "+
			"the WAVEFORMATEX and the blob holds %d in all, not %d", extra, len(b), n)
	}
	return nil
}

// openClient activates the audio client and tries to obtain raw mode.
//
// It returns the client, whether raw mode was obtained and, when it was not,
// the last reason for the refusal — which the caller has to be able to report
// instead of letting it fall on the floor.
func openClient(dev *wca.IMMDevice, opts Options) (*wca.IAudioClient2, bool, error, error) {
	var lastErr error

	for attempt := 0; attempt < rawAttempts; attempt++ {
		var client *wca.IAudioClient2
		if err := dev.Activate(wca.IID_IAudioClient2, ole.CLSCTX_ALL, nil, &client); err != nil {
			return nil, false, nil, fmt.Errorf("Activate IAudioClient2: %w",
				describeAudclnt(err))
		}
		if !opts.Raw {
			return client, false, nil, nil
		}

		// The decisive step: ask for the raw stream BEFORE Initialize.
		props := wca.AudioClientProperties{
			CbSize:                uint32(unsafe.Sizeof(wca.AudioClientProperties{})),
			BIsOffload:            false,
			AUDIO_STREAM_CATEGORY: audioCategoryOther,
			AUDCLNT_STREAMOPTIONS: wca.AUDCLNT_STREAMOPTIONS_RAW,
		}
		if err := client.SetClientProperties(&props); err == nil {
			return client, true, nil, nil
		} else {
			lastErr = describeAudclnt(err)
		}

		// The client that refused is thrown away: a new attempt starts from a
		// new object, not from one that has already been told no.
		client.Release()
		if attempt < rawAttempts-1 {
			time.Sleep(rawRetryGap)
		}
	}

	// Attempts exhausted: it is opened anyway, and the caller decides whether
	// to accept that.
	var client *wca.IAudioClient2
	if err := dev.Activate(wca.IID_IAudioClient2, ole.CLSCTX_ALL, nil, &client); err != nil {
		return nil, false, nil, fmt.Errorf("Activate IAudioClient2: %w",
			describeAudclnt(err))
	}
	return client, false, lastErr, nil
}

func Capture(
	ctx context.Context,
	opts Options,
	onStart func(Stream) error,
	onData func(pcm []byte, silent bool) error,
) error {
	return comThread(func() error {
		enum, err := newDeviceEnumerator()
		if err != nil {
			return err
		}
		defer enum.Release()

		dev, err := openDevice(enum, opts.DeviceID)
		if err != nil {
			return err
		}
		defer dev.Release()

		var deviceID string
		_ = dev.GetId(&deviceID)
		deviceName := deviceFriendlyName(dev)

		// The volume control is queried before opening the client: with a
		// client already active on the same device the interface cannot be
		// activated, and the available gain would come out as non-existent
		// exactly where it needs to be known.
		volume, volumeErr := endpointVolumeInfo(dev)

		client, rawMode, rawErr, err := openClient(dev, opts)
		if err != nil {
			return err
		}
		// The close sits inside an anonymous function on purpose: defer
		// client.Release() would evaluate the receiver now, and further down
		// the client is replaced by the exclusive one. That way the first one
		// was released twice — a crash on the way out, not on the way in, and
		// therefore invisible until the capture stops.
		defer func() { client.Release() }()

		var wfxPtr *wca.WAVEFORMATEX
		if err := client.GetMixFormat(&wfxPtr); err != nil {
			return fmt.Errorf("GetMixFormat: %w", describeAudclnt(err))
		}
		defer ole.CoTaskMemFree(uintptr(unsafe.Pointer(wfxPtr)))

		mode := "shared"
		if rawMode {
			mode = "raw"
		}

		// Event-driven: Windows signals when a packet is ready, which avoids
		// polling for nothing.
		err = client.Initialize(
			wca.AUDCLNT_SHAREMODE_SHARED,
			wca.AUDCLNT_STREAMFLAGS_EVENTCALLBACK,
			0, 0, wfxPtr, nil,
		)

		// Raw refused: exclusive mode is left.
		//
		// In exclusive mode the application talks to the driver and the audio
		// engine is not there at all, so neither is any APO: the same result as
		// raw, by another route. It costs having the microphone to ourselves
		// for as long as the monitor runs — and for a baby monitor that is an
		// acceptable price, because the alternative is hearing nothing.
		// Exclusive is a fallback for raw denied, not an alternative to shared
		// mode: whoever explicitly asks for the shared path — the comparison
		// tools — has to get it, otherwise they would be measuring one thing
		// while believing they measured another.
		exclusive := false
		if err == nil && !rawMode && (opts.Exclusive || (opts.Raw && rawErr != nil)) {
			excl, exclFmt, exclErr := openExclusive(dev)
			if exclErr == nil {
				client.Release()
				client = excl
				wfxPtr = exclFmt
				rawMode, mode, exclusive = true, "exclusive", true
				err = nil
			} else {
				rawErr = fmt.Errorf("%w; exclusive: %v", rawErr, exclErr)
			}
		}
		if err != nil {
			return fmt.Errorf("IAudioClient.Initialize: %w", describeAudclnt(err))
		}

		// Sensitivity is always the endpoint's maximum, by both routes: in
		// shared mode the engine has already applied the slider and what is
		// missing is added, in exclusive it has applied nothing and all of it
		// goes on. That way the monitor hears the same however it was opened,
		// and does not change sensitivity because someone moved a Windows
		// slider for a meeting.
		gainDB := sensitivityGain(volume.CurrentDB, volume.MaxDB, !exclusive)
		// **A gain of zero has two meanings, and only one of them is harmless.**
		// When the activation of IAudioEndpointVolume fails, `MaxDB` stays at
		// zero and `sensitivityGain` reads that as "there is nothing to
		// recover" — right for an endpoint that exposes no gain, and wrong
		// here. In **exclusive** mode the audio engine applies nothing, so the
		// gain it would have applied goes with it: on this machine that is
		// 30 dB, and the symptom is the one already recorded — the audio
		// arrives clean and six times quieter, enough to make a level meter
		// look stuck. In shared mode the engine has already applied the
		// slider, so an unreadable control costs nothing and is not reported.
		//
		// The reason travels with the Stream and is declared by whoever
		// consumes it, exactly as RawError is: a silent fallback is worse than
		// a fault, because there is not even anything to investigate.
		var volumeUnknown error
		if exclusive && !volume.Available {
			volumeUnknown = volumeErr
			if volumeUnknown == nil {
				volumeUnknown = errors.New(volume.Why)
			}
		}
		// The refusal is a fault only for whoever asked for the clean signal:
		// whoever deliberately opens the shared path is measuring precisely
		// that.
		if opts.Raw && !rawMode && !opts.FallbackOnRawFailure {
			return fmt.Errorf(
				"no route to the unprocessed signal of endpoint %q: %w "+
					"(so the audio goes through the OEM effects)", deviceName, rawErr)
		}

		format, err := decodeFormat(wfxPtr)
		if err != nil {
			return err
		}

		event, err := windows.CreateEvent(nil, 0, 0, nil)
		if err != nil {
			return fmt.Errorf("CreateEvent: %w", err)
		}
		defer windows.CloseHandle(event)

		if err := client.SetEventHandle(uintptr(event)); err != nil {
			return fmt.Errorf("SetEventHandle: %w", describeAudclnt(err))
		}

		var capture *wca.IAudioCaptureClient
		if err := client.GetService(wca.IID_IAudioCaptureClient, &capture); err != nil {
			return fmt.Errorf("GetService IAudioCaptureClient: %w", describeAudclnt(err))
		}
		defer capture.Release()

		if err := onStart(Stream{
			Format:      format,
			DeviceName:  deviceName,
			DeviceID:    deviceID,
			RawMode:     rawMode,
			Mode:        mode,
			GainDB:      gainDB,
			Muted:       volume.Muted,
			Volume:      volume,
			RawError:    rawErr,
			VolumeError: volumeUnknown,
		}); err != nil {
			return err
		}

		if err := client.Start(); err != nil {
			return fmt.Errorf("IAudioClient.Start: %w", describeAudclnt(err))
		}
		defer client.Stop()

		return captureLoop(ctx, capture, event, format, onData)
	})
}

// captureLoop reads packets until the context is cancelled.
func captureLoop(
	ctx context.Context,
	capture *wca.IAudioCaptureClient,
	event windows.Handle,
	format StreamFormat,
	onData func(pcm []byte, silent bool) error,
) error {
	frameSize := format.BytesPerFrame()
	if frameSize <= 0 {
		return fmt.Errorf("audio: invalid frame size for %s", format)
	}
	// A buffer of zeroes, reused for the packets marked as silence, where the
	// contents of the memory carry no meaning.
	silence := make([]byte, 0, 16<<10)

	// The wait timeout is only a safety net, to notice that the device has
	// stopped signalling (a disconnection, say).
	const waitTimeoutMS = 2000
	stalls := 0

	for {
		if err := ctx.Err(); err != nil {
			return nil // cancellation requested: a normal exit
		}

		switch s, err := windows.WaitForSingleObject(event, waitTimeoutMS); {
		case err != nil:
			return fmt.Errorf("WaitForSingleObject: %w", err)
		case s == uint32(windows.WAIT_TIMEOUT):
			stalls++
			// Some drivers stay silent for a long time: we insist a little
			// before declaring the device lost.
			if stalls >= 5 {
				return fmt.Errorf("audio: the endpoint has delivered no data for %v",
					time.Duration(stalls)*waitTimeoutMS*time.Millisecond)
			}
			continue
		}
		stalls = 0

		// One event can correspond to several queued packets.
		for {
			var frames uint32
			if err := capture.GetNextPacketSize(&frames); err != nil {
				return fmt.Errorf("GetNextPacketSize: %w", describeAudclnt(err))
			}
			if frames == 0 {
				break
			}

			var data *byte
			var available, flags uint32
			var devicePos, qpcPos uint64
			if err := capture.GetBuffer(&data, &available, &flags, &devicePos, &qpcPos); err != nil {
				return fmt.Errorf("GetBuffer: %w", describeAudclnt(err))
			}

			n := int(available) * frameSize
			silent := flags&wca.AUDCLNT_BUFFERFLAGS_SILENT != 0

			var err error
			switch {
			case n == 0:
				// nothing to deliver
			case silent || data == nil:
				// With the SILENT flag the buffer must not be read: zeroes are
				// delivered instead.
				if cap(silence) < n {
					silence = make([]byte, n)
				}
				buf := silence[:n]
				for i := range buf {
					buf[i] = 0
				}
				err = onData(buf, true)
			default:
				err = onData(unsafe.Slice(data, n), false)
			}

			if relErr := capture.ReleaseBuffer(available); relErr != nil && err == nil {
				err = fmt.Errorf("ReleaseBuffer: %w", describeAudclnt(relErr))
			}
			if err != nil {
				return err
			}
		}
	}
}

// openDevice opens the requested endpoint, or the default one if id is empty.
//
// **An endpoint asked for and not found is not a fault: it is a microphone that
// has been unplugged.** USB sticks get pulled out and Bluetooth earpieces walk
// away, and refusing to open anything else would mean a baby monitor silent all
// night with a working microphone right under its nose — which is the same
// reasoning by which "no default" does not mean "no microphone". So it falls
// back on the default, and what declares that is whoever opened it: the Stream
// carries the ID and the name of what was really opened, which is not
// necessarily what was asked for.
func openDevice(enum *wca.IMMDeviceEnumerator, id string) (*wca.IMMDevice, error) {
	if id == "" {
		var dev *wca.IMMDevice
		if err := enum.GetDefaultAudioEndpoint(wca.ECapture, wca.EConsole, &dev); err == nil {
			return dev, nil
		}
		// No default does not mean no microphone.
		//
		// Windows assigns the role, and the role can be left empty: all it
		// takes is for the default to have been a Bluetooth earpiece and for
		// that earpiece to walk away. Measured on this machine — after a call
		// with headphones, "Microphone Array" was active and the monitor
		// declared itself deaf.
		//
		// For a baby monitor, giving up here is the worst fault of all: there
		// is a microphone that works, and it is not listened to because a label
		// is missing.
		return firstActiveCapture(enum)
	}

	var coll *wca.IMMDeviceCollection
	if err := enum.EnumAudioEndpoints(wca.ECapture, wca.DEVICE_STATE_ACTIVE, &coll); err != nil {
		return nil, fmt.Errorf("EnumAudioEndpoints: %w", describeAudclnt(err))
	}
	defer coll.Release()

	var count uint32
	if err := coll.GetCount(&count); err != nil {
		return nil, fmt.Errorf("GetCount: %w", describeAudclnt(err))
	}
	for i := uint32(0); i < count; i++ {
		var dev *wca.IMMDevice
		if err := coll.Item(i, &dev); err != nil {
			continue
		}
		var got string
		_ = dev.GetId(&got)
		if got == id {
			return dev, nil
		}
		dev.Release()
	}
	return openDevice(enum, "")
}

// firstActiveCapture returns the first active capture endpoint.
//
// It is the fallback for when Windows has no default to point at. Taking the
// first is arbitrary, and it is right that it should be: the alternative is
// listening to nothing. Whoever wants to decide can fix mic_device_id in the
// configuration, and the name of the chosen endpoint ends up in the log anyway.
func firstActiveCapture(enum *wca.IMMDeviceEnumerator) (*wca.IMMDevice, error) {
	var coll *wca.IMMDeviceCollection
	if err := enum.EnumAudioEndpoints(wca.ECapture, wca.DEVICE_STATE_ACTIVE, &coll); err != nil {
		return nil, fmt.Errorf("no default microphone, and EnumAudioEndpoints "+
			"fails: %w", describeAudclnt(err))
	}
	defer coll.Release()

	var count uint32
	if err := coll.GetCount(&count); err != nil {
		return nil, fmt.Errorf("GetCount: %w", describeAudclnt(err))
	}
	for i := uint32(0); i < count; i++ {
		var dev *wca.IMMDevice
		if err := coll.Item(i, &dev); err != nil {
			continue
		}
		return dev, nil
	}
	// Before declaring that there is no microphone, look at **where we are**.
	//
	// Inside a Remote Desktop session the physical endpoints do not exist by
	// definition, and the generic message would send someone hunting for a
	// fault in the hardware — which is meanwhile working perfectly, as the
	// webcam that opens in the same session demonstrates. It costs one call and
	// turns a mystery into an instruction.
	if remoteSession() {
		return nil, fmt.Errorf("no microphone: this is a Remote Desktop session, " +
			"and there Windows does not expose the machine's audio devices — the webcam " +
			"does work, which is why it looks like a fault. The monitor must be started " +
			"from the console: a process launched over RDP stays deaf even after logout")
	}
	return nil, fmt.Errorf("no microphone: Windows names no default one, and " +
		"none is active")
}

// decodeFormat interprets the WAVEFORMATEX returned by WASAPI, following the
// SubFormat GUID when the format is WAVEFORMATEXTENSIBLE.
func decodeFormat(wfx *wca.WAVEFORMATEX) (StreamFormat, error) {
	f := StreamFormat{
		SampleRate:    int(wfx.NSamplesPerSec),
		Channels:      int(wfx.NChannels),
		BitsPerSample: int(wfx.WBitsPerSample),
	}

	tag := wfx.WFormatTag
	if tag == waveFormatExtensible {
		// **cbSize authorises this read and does not guarantee it.** What
		// guarantees it is that the bytes behind wfx are as many as it claims,
		// which is the caller's to know: from GetMixFormat they are COM's and
		// come by contract, from PKEY_AudioEngine_DeviceFormat they are a copy
		// validateFormatBlob has measured. A pointer arriving here from
		// anywhere else needs the same before it is passed.
		if wfx.CbSize < 22 {
			return f, fmt.Errorf("audio: WAVEFORMATEXTENSIBLE with cbSize=%d is too small", wfx.CbSize)
		}
		sub := (*ole.GUID)(unsafe.Add(unsafe.Pointer(wfx), subFormatOffset))
		// In the KSDATAFORMAT_SUBTYPEs the GUID's first field coincides with
		// the corresponding WAVE format tag.
		tag = uint16(sub.Data1)
	}

	switch tag {
	case waveFormatPCM:
		f.Float = false
	case waveFormatIEEEFloat:
		f.Float = true
	default:
		return f, fmt.Errorf("audio: unhandled WAVE format tag: 0x%04X", tag)
	}
	if f.Channels == 0 || f.SampleRate == 0 || f.BitsPerSample == 0 {
		return f, fmt.Errorf("audio: incomplete format (%s)", f)
	}
	return f, nil
}
