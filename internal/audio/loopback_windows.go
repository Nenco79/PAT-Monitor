//go:build windows

package audio

import (
	"fmt"
	"math"
	"time"
	"unsafe"

	"github.com/go-ole/go-ole"
	"github.com/moutend/go-wca/pkg/wca"
)

// Loopback capture: listening to what comes out of the speakers.
//
// **It exists because "written into the buffer" is not "it was heard".** The
// render path can accept the format, drain the queue and throw nothing away
// while producing no sound at all — a mute driver, the wrong endpoint, the
// volume at zero — and from inside the program the two cases are identical. It
// is the same question as the microphone that delivers zeroes, from the other
// side of the room.
//
// WASAPI offers it ready made: open a **capture** client on a **render**
// endpoint with AUDCLNT_STREAMFLAGS_LOOPBACK, and what arrives is the mix the
// engine is playing.
const audclntStreamFlagsLoopback = 0x00020000

// LoopbackLevel measures, for as long as it is told to, the level of what comes
// out of the default audio output, and returns RMS in dBFS plus the peak.
func LoopbackLevel(window time.Duration) (rmsDBFS, peak float64, err error) {
	err = comThread(func() error {
		enum, e := newDeviceEnumerator()
		if e != nil {
			return e
		}
		defer enum.Release()

		dev, e := openRenderDevice(enum, "")
		if e != nil {
			return e
		}
		defer dev.Release()

		var client *wca.IAudioClient
		if e := dev.Activate(wca.IID_IAudioClient, ole.CLSCTX_ALL, nil, &client); e != nil {
			return fmt.Errorf("loopback open: %w", describeAudclnt(e))
		}
		defer client.Release()

		// In loopback the engine's format is taken as it is: we are not playing
		// anything, we are listening to its mix.
		var wfx *wca.WAVEFORMATEX
		if e := client.GetMixFormat(&wfx); e != nil {
			return fmt.Errorf("GetMixFormat: %w", describeAudclnt(e))
		}
		defer ole.CoTaskMemFree(uintptr(unsafe.Pointer(wfx)))

		format, e := decodeFormat(wfx)
		if e != nil {
			return e
		}
		if e := client.Initialize(wca.AUDCLNT_SHAREMODE_SHARED, audclntStreamFlagsLoopback,
			wca.REFERENCE_TIME(500*time.Millisecond/100), 0, wfx, nil); e != nil {
			return fmt.Errorf("Initialize for loopback: %w", describeAudclnt(e))
		}

		var capture *wca.IAudioCaptureClient
		if e := client.GetService(wca.IID_IAudioCaptureClient, &capture); e != nil {
			return fmt.Errorf("IAudioCaptureClient: %w", describeAudclnt(e))
		}
		defer capture.Release()

		if e := client.Start(); e != nil {
			return fmt.Errorf("loopback capture start: %w", describeAudclnt(e))
		}
		defer client.Stop()

		var sum float64
		var n int
		end := time.Now().Add(window)
		for time.Now().Before(end) {
			time.Sleep(10 * time.Millisecond)
			for {
				var frames uint32
				// **An error here is not "nothing is queued".** It was read as
				// one, so a device invalidated while the measurement runs —
				// AUDCLNT_E_DEVICE_INVALIDATED, which is what an endpoint that
				// goes away answers — broke the inner loop, the outer one span
				// out the window, and the function returned a floor of -120 dBFS
				// with a nil error: an output that had gone was reported as an
				// output that is quiet, and the verdict accused the driver.
				if e := capture.GetNextPacketSize(&frames); e != nil {
					return fmt.Errorf("GetNextPacketSize: %w", describeAudclnt(e))
				}
				if frames == 0 {
					break
				}
				var data *byte
				var avail, flags uint32
				var dp, qp uint64
				if e := capture.GetBuffer(&data, &avail, &flags, &dp, &qp); e != nil {
					return fmt.Errorf("GetBuffer: %w", describeAudclnt(e))
				}
				if data != nil && flags&wca.AUDCLNT_BUFFERFLAGS_SILENT == 0 {
					samples := int(avail) * format.Channels
					vs, e := floatSamples(data, samples, format)
					if e != nil {
						// The buffer has to be given back before leaving: the
						// deferred Stop does not release it.
						_ = capture.ReleaseBuffer(avail)
						return e
					}
					for _, v := range vs {
						sum += v * v
						peak = math.Max(peak, math.Abs(v))
						n++
					}
				}
				if e := capture.ReleaseBuffer(avail); e != nil {
					return fmt.Errorf("ReleaseBuffer: %w", describeAudclnt(e))
				}
			}
		}
		// **No samples is a reading here, not a failure to measure**, and the
		// difference is the whole reason this stays -120 instead of an error.
		// The client started, so the engine is running; in loopback what arrives
		// is the mix it is playing, and no audio — whether the driver marks the
		// buffers SILENT or hands over nothing at all — means it is playing
		// nothing. For the floor measured before the tone that is the right
		// answer, and for the window during the tone it is exactly the verdict
		// the instrument exists to reach: "the path accepts the samples and
		// produces no sound".
		//
		// It was briefly an error, and that aborted `pat-wasapi` at its first
		// call — on a machine deliberately kept quiet, which is what the tool
		// asks for — so the tone was never played and no verdict was ever
		// reached. The case this function really could not measure is an
		// unreadable sample format, and that one now comes back as an error from
		// `floatSamples` instead of as silence.
		if n == 0 {
			rmsDBFS = -120
			return nil
		}
		r := math.Sqrt(sum / float64(n))
		if r <= 0 {
			rmsDBFS = -120
		} else {
			rmsDBFS = 20 * math.Log10(r)
		}
		return nil
	})
	return rmsDBFS, peak, err
}

// floatSamples normalises the engine's buffer into values between -1 and 1.
//
// **It goes through `sampleAt`, which is the one reader of a StreamFormat.** It
// used to carry a switch of its own over two cases, 32-bit float and 16-bit
// integer, and return an empty slice for everything else — while `decodeFormat`
// accepts any bit depth under the PCM and IEEE-float tags and `sampleAt`
// converts five. So one format had two readers that disagreed about what could
// be read, and the one inside the measuring instrument answered *silence*: on an
// engine mixing at 64-bit float, or 24- or 32-bit integer, the meter reported a
// floor of -120 dBFS and the verdict accused the driver. Two lists of the same
// thing always diverge; there is now one.
func floatSamples(data *byte, n int, f StreamFormat) ([]float64, error) {
	bps := f.BitsPerSample / 8
	if bps <= 0 {
		return nil, fmt.Errorf("audio: unconvertible samples: %s", f)
	}
	buf := unsafe.Slice(data, n*bps)
	out := make([]float64, 0, n)
	for i := range n {
		v, err := sampleAt(buf[i*bps:], f)
		if err != nil {
			return nil, err
		}
		out = append(out, v)
	}
	return out, nil
}

// RenderVolume reads the volume of the default audio output.
//
// **It exists to answer "I cannot hear anything" without guessing.** The render
// path can be perfect and the sound still be inaudible because the slider is
// down or the output is muted, and from inside the program those two cases are
// indistinguishable from a fault. It is the same question pat-wasapi asks about
// the microphone, from the other side of the room.
func RenderVolume() (VolumeInfo, error) {
	var info VolumeInfo
	err := comThread(func() error {
		enum, e := newDeviceEnumerator()
		if e != nil {
			return e
		}
		defer enum.Release()
		dev, e := openRenderDevice(enum, "")
		if e != nil {
			return e
		}
		defer dev.Release()
		info, e = endpointVolumeInfo(dev)
		return e
	})
	return info, err
}
