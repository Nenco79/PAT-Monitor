//go:build windows

package audio

import (
	"context"
	"fmt"
	"sync"
	"time"
	"unsafe"

	"github.com/go-ole/go-ole"
	"github.com/moutend/go-wca/pkg/wca"

	"patmonitor/internal/guard"
)

// Playback: the talk-back, that is, the voice of whoever is watching coming out
// of the room's speakers.
//
// **In shared mode, unlike capture, and that is not an inconsistency.** Capture
// goes raw to step over the APOs that filter crying: there, Windows' processing
// is the enemy. Here there is nothing to step over, and exclusive mode would
// cost dearly — it would seize the speakers, silencing the rest of the computer
// for the length of a sentence, and it would step over the volume slider just
// as whoever is in the room would like to turn it down. **The rule is not
// "always raw": it is "the audio engine takes part where it is needed".**
//
// The conversion is asked of the system. The engine almost always runs at 48
// kHz in floating point, but that is not guaranteed — it can be 44.1 — and
// resampling by hand from 48 to 44.1 would alter the pitch of the voice without
// leaving a single clue. AUDCLNT_STREAMFLAGS_AUTOCONVERTPCM leaves it to
// whoever knows how it is done: the same rule as the microphone, applied in the
// other direction.

const (
	// Constants go-wca does not expose. AUTOCONVERTPCM asks the engine to accept
	// a format other than its own and convert it; SRC_DEFAULT_QUALITY picks the
	// standard resampler rather than the cheap one.
	audclntStreamFlagsAutoConvertPCM = 0x80000000
	audclntStreamFlagsSRCDefault     = 0x08000000

	// The device buffer is only available room: how much really goes into it is
	// decided by what we write, and we write only what we have.
	renderBufferDuration = 500 * time.Millisecond
	// How often we look to see whether there is anything to deliver.
	renderTick = 10 * time.Millisecond
	// How much we accept holding in the queue before throwing away.
	renderMaxQueue = 400 * time.Millisecond
	// How long we wait for the device to open. Measured: 313 ms in the normal
	// case, so five seconds are wide for a genuine delay and tight for a fault —
	// and whoever pressed "Talk" is not waiting longer than that anyway.
	renderOpenTimeout = 5 * time.Second
)

// Player plays 16-bit mono PCM on the audio output.
//
// It opens when someone starts talking and closes when they stop: the speakers
// are not held all night by a feature that is used for ten seconds.
type Player struct {
	rate     int
	log      Logger
	cancel   context.CancelFunc
	finished chan struct{}

	mu    sync.Mutex
	queue []int16
	// dropped counts the samples thrown away because the queue was full. It is
	// the number that tells "the network delivers in bursts" from "playback
	// cannot keep up", and without it the two sound the same.
	dropped int
}

// Logger is the minimum needed here: the package must not depend on slog for
// two lines.
type Logger interface {
	Warn(msg string, args ...any)
	Info(msg string, args ...any)
}

// NewPlayer opens the audio output and starts consuming whatever is written to
// it.
//
// An empty deviceID means the Windows default output, which is the right one in
// the normal case and is also the only one that follows someone swapping
// headphones without restarting the monitor.
func NewPlayer(rate int, deviceID string, log Logger) (*Player, error) {
	if rate <= 0 {
		rate = 48000
	}
	p := &Player{rate: rate, log: log, finished: make(chan struct{})}

	ctx, cancel := context.WithCancel(context.Background())
	p.cancel = cancel

	// The opening happens on the thread that will then serve the device: COM
	// objects belong to the thread that created them, and using them elsewhere
	// is the kind of mistake that does not give an error.
	started := make(chan error, 1)
	guard.Go(nil, "the audio output", func() {
		defer close(p.finished)
		err := comThread(func() error { return p.serve(ctx, deviceID, started) })
		if err != nil && ctx.Err() == nil && log != nil {
			log.Warn("audio playback interrupted", "error", err)
		}
	})

	// **The opening must not be able to wait forever**, and it is not enough
	// that every path inside serve answers: if comThread does not even get as
	// far as calling it — which has happened, see the comment there — nobody
	// answers. An extra that lasts ten seconds cannot have the power to stop a
	// monitor that has to watch all night: once the wait is over, talk-back
	// declares itself unavailable and the rest carries on.
	select {
	case err := <-started:
		if err != nil {
			cancel()
			<-p.finished
			return nil, err
		}
	case <-p.finished:
		// The thread left without saying anything.
		cancel()
		return nil, fmt.Errorf("the audio output closed before it opened")
	case <-time.After(renderOpenTimeout):
		cancel()
		return nil, fmt.Errorf("the audio output did not open within %v", renderOpenTimeout)
	}
	return p, nil
}

// Write queues mono samples. It never blocks: the writer is the network.
func (p *Player) Write(pcm []int16) {
	if len(pcm) == 0 {
		return
	}
	max := p.rate * int(renderMaxQueue) / int(time.Second)

	p.mu.Lock()
	defer p.mu.Unlock()
	p.queue = append(p.queue, pcm...)
	if len(p.queue) > max {
		// **The old is thrown away, not the new.** A late voice is no use to
		// anyone: whoever is listening wants what is being said now, and keeping
		// the queue would mean talking with half a second of delay for the whole
		// rest of the session.
		excess := len(p.queue) - max
		p.queue = append(p.queue[:0], p.queue[excess:]...)
		p.dropped += excess
	}
}

// Dropped says how many samples were thrown away because the queue was full.
func (p *Player) Dropped() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.dropped
}

// Close stops playback and releases the output.
func (p *Player) Close() error {
	if p == nil || p.cancel == nil {
		return nil
	}
	p.cancel()
	<-p.finished
	return nil
}

// take pulls up to n samples off the queue.
func (p *Player) take(n int) []int16 {
	p.mu.Lock()
	defer p.mu.Unlock()
	if len(p.queue) == 0 || n <= 0 {
		return nil
	}
	if n > len(p.queue) {
		n = len(p.queue)
	}
	out := append([]int16(nil), p.queue[:n]...)
	p.queue = append(p.queue[:0], p.queue[n:]...)
	return out
}

// serve opens the device and keeps it fed for as long as the context lives.
func (p *Player) serve(ctx context.Context, deviceID string, started chan<- error) error {
	failed := func(err error) error {
		started <- err
		return err
	}

	enum, err := newDeviceEnumerator()
	if err != nil {
		return failed(err)
	}
	defer enum.Release()

	dev, err := openRenderDevice(enum, deviceID)
	if err != nil {
		return failed(err)
	}
	defer dev.Release()

	var client *wca.IAudioClient
	if err := dev.Activate(wca.IID_IAudioClient, ole.CLSCTX_ALL, nil, &client); err != nil {
		return failed(fmt.Errorf("audio output open: %w", describeAudclnt(err)))
	}
	defer client.Release()

	if err := initRender(client, p.rate); err != nil {
		return failed(err)
	}

	var render *wca.IAudioRenderClient
	if err := client.GetService(wca.IID_IAudioRenderClient, &render); err != nil {
		return failed(fmt.Errorf("IAudioRenderClient: %w", describeAudclnt(err)))
	}
	defer render.Release()

	var bufferFrames uint32
	if err := client.GetBufferSize(&bufferFrames); err != nil {
		return failed(fmt.Errorf("GetBufferSize: %w", describeAudclnt(err)))
	}
	if err := client.Start(); err != nil {
		return failed(fmt.Errorf("playback start: %w", describeAudclnt(err)))
	}
	defer client.Stop()

	if p.log != nil {
		p.log.Info("audio output opened", "rate", p.rate,
			"buffer_frames", bufferFrames, "device", deviceOrDefault(deviceID))
	}
	started <- nil
	return p.renderLoop(ctx, client, render, bufferFrames)
}

func deviceOrDefault(id string) string {
	if id == "" {
		return "default"
	}
	return id
}

// initRender asks the audio engine to accept **our** format.
//
// 16-bit mono at Opus's rate: that way a device frame is one of our samples,
// and there is no conversion left to write. If the engine were to refuse, we
// say so and stop rather than falling back on a home-made conversion — a
// fallback that is never executed is the least tested part of the program, put
// exactly where nobody could notice.
func initRender(client *wca.IAudioClient, rate int) error {
	wfx := &wca.WAVEFORMATEX{
		WFormatTag:      waveFormatPCM,
		NChannels:       1,
		NSamplesPerSec:  uint32(rate),
		WBitsPerSample:  16,
		NBlockAlign:     2,
		NAvgBytesPerSec: uint32(rate) * 2,
		CbSize:          0,
	}
	const flags = audclntStreamFlagsAutoConvertPCM | audclntStreamFlagsSRCDefault
	err := client.Initialize(
		wca.AUDCLNT_SHAREMODE_SHARED, flags,
		wca.REFERENCE_TIME(renderBufferDuration/100), 0, wfx, nil,
	)
	if err != nil {
		return fmt.Errorf("the audio output rejects %d Hz mono 16-bit: %w",
			rate, describeAudclnt(err))
	}
	return nil
}

// renderLoop hands the device whatever is in the queue.
//
// **Only what we have is written, never filler silence.** Filling the free
// space with zeroes looks tidier and does the opposite: the silence queues up
// **in front of** the voice, and the sentence comes out half a second after
// being spoken, for the whole session. In shared mode an empty buffer produces
// no defect at all — the engine mixes what is there, and from us there is
// nothing.
func (p *Player) renderLoop(
	ctx context.Context,
	client *wca.IAudioClient,
	render *wca.IAudioRenderClient,
	bufferFrames uint32,
) error {
	tick := time.NewTicker(renderTick)
	defer tick.Stop()

	for {
		select {
		case <-ctx.Done():
			return nil
		case <-tick.C:
		}

		var padding uint32
		if err := client.GetCurrentPadding(&padding); err != nil {
			return fmt.Errorf("GetCurrentPadding: %w", describeAudclnt(err))
		}
		free := int(bufferFrames) - int(padding)
		pcm := p.take(free)
		if len(pcm) == 0 {
			continue
		}

		var data *byte
		if err := render.GetBuffer(uint32(len(pcm)), &data); err != nil {
			return fmt.Errorf("GetBuffer: %w", describeAudclnt(err))
		}
		if data != nil {
			copy(unsafe.Slice((*int16)(unsafe.Pointer(data)), len(pcm)), pcm)
		}
		if err := render.ReleaseBuffer(uint32(len(pcm)), 0); err != nil {
			return fmt.Errorf("ReleaseBuffer: %w", describeAudclnt(err))
		}
	}
}

// openRenderDevice opens the requested output, or the default one if id is
// empty. Twin of openDevice, which does the same for capture.
func openRenderDevice(enum *wca.IMMDeviceEnumerator, id string) (*wca.IMMDevice, error) {
	if id == "" {
		var dev *wca.IMMDevice
		if err := enum.GetDefaultAudioEndpoint(wca.ERender, wca.EConsole, &dev); err != nil {
			return nil, fmt.Errorf("no default audio output: %w", describeAudclnt(err))
		}
		return dev, nil
	}

	var coll *wca.IMMDeviceCollection
	if err := enum.EnumAudioEndpoints(wca.ERender, wca.DEVICE_STATE_ACTIVE, &coll); err != nil {
		return nil, fmt.Errorf("EnumAudioEndpoints: %w", describeAudclnt(err))
	}
	defer coll.Release()

	var n uint32
	if err := coll.GetCount(&n); err != nil {
		return nil, fmt.Errorf("GetCount: %w", describeAudclnt(err))
	}
	for i := uint32(0); i < n; i++ {
		var dev *wca.IMMDevice
		if err := coll.Item(i, &dev); err != nil {
			continue
		}
		var got string
		if err := dev.GetId(&got); err == nil && got == id {
			return dev, nil
		}
		dev.Release()
	}
	// **There is no silent fallback to the default.** Whoever wrote an id in
	// the configuration chose a speaker: talking out of a different one,
	// without saying so, is how a knob comes to look broken.
	return nil, fmt.Errorf("audio output %q not found among the active ones", id)
}
