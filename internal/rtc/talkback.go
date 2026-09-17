package rtc

import (
	"errors"
	"io"
	"log/slog"
	"sync"
	"time"

	"github.com/pion/webrtc/v4"

	"patmonitor/internal/audiocodec"
	"patmonitor/internal/guard"
)

// Talk-back: the voice of whoever is watching, played into the room.
//
// **It is half duplex, and that is not a simplification.** While somebody is
// speaking, the monitor stops sending the room's audio: if it did not, the
// microphone would pick up the speakers and the speaker would hear themselves
// come back half a second later — the intercom howl. Real echo cancellation is a
// piece of signal processing that is not here and will not be, for a feature
// that lasts ten seconds; the silence of the return replaces it, and it is also
// how every walkie-talkie works.
//
// **And one person speaks at a time.** Two voices together in a room are not a
// conversation, they are noise, and mixing them would need a mixer for a case
// that does not arise. Whoever arrives second finds the door shut and the page
// tells them so.

// Player is what talk-back needs to be able to do with PCM: play it.
//
// It is an interface because `internal/audio` exists only on Windows, and
// because this way the logic can be tested without speakers.
type Player interface {
	Write(pcm []int16)
	Close() error
}

// OpenPlayer opens the audio output. Whoever builds the hub supplies it: nothing
// here knows anything about WASAPI.
type OpenPlayer func(rate int) (Player, error)

const (
	// talkIdle is how much silence counts as the end of a turn.
	// **The browser does not decide it**, since it could close without saying
	// anything: if the packets stop arriving, the room becomes audible again by
	// itself.
	talkIdle = 2 * time.Second
	// talkMax is the absolute cap on one turn. It guards against the case that
	// ruins the night: a page left open with the microphone on by mistake, which
	// would keep the room mute until morning.
	talkMax = 2 * time.Minute
	// talkOpenRetry is how long to wait before trying to open the audio output
	// again after a refusal.
	//
	// **Without it, it is retried fifty times a second.** Whoever speaks sends a
	// packet every twenty milliseconds and each one asked for the device afresh:
	// where there is no output — inside a Remote Desktop session, to name one
	// that happens — that becomes fifty failed COM calls a second and fifty
	// identical lines of log, that is a monitor working for nothing instead of
	// watching the room. Measured: the refusal arrives as HRESULT 0x80070057,
	// straight away, so the loop turns at the speed of the packets.
	talkOpenRetry = 5 * time.Second
)

// Talkback governs who speaks and holds the audio output open while it is
// needed.
type Talkback struct {
	open OpenPlayer
	log  *slog.Logger

	mu      sync.Mutex
	speaker int64 // id of the viewer holding the floor, 0 if nobody
	player  Player
	since   time.Time
	lastPkt time.Time

	// spent is whoever has exceeded the maximum duration, and they stay out
	// **until they really fall silent**.
	//
	// Without this, the cap limits nothing: the floor is taken away at two
	// minutes and the next packet — which arrives twenty milliseconds later, if
	// the microphone is still on — takes it back, and the room stays mute all
	// night in two-minute blocks. The case the cap exists to cover is exactly
	// the one where the packets do not stop.
	spent     int64
	spentSeen time.Time

	// opening says somebody is opening the audio output right now, outside the
	// lock. It keeps the other packets out in the meantime.
	opening bool

	// notBefore is when the audio output may be tried again after a refusal.
	// See talkOpenRetry.
	notBefore time.Time
}

func NewTalkback(open OpenPlayer, log *slog.Logger) *Talkback {
	if log == nil {
		log = slog.Default()
	}
	return &Talkback{open: open, log: log}
}

// Active says whether somebody is speaking right now. WriteAudio consults it:
// that is the half duplex.
func (t *Talkback) Active() bool {
	if t == nil {
		return false
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.speaker != 0
}

// Speaker is the id of whoever holds the floor, 0 if nobody.
func (t *Talkback) Speaker() int64 {
	if t == nil {
		return 0
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.speaker
}

// acquire gives the floor to a viewer, if it is free.
//
// **The audio output is opened outside the lock.** `t.open` is a call into a
// device — WASAPI, COM, a driver — and holding a mutex across a call like that
// means handing that driver the right to stop everything that touches it. Over
// this lock pass `Active`, which **WriteAudio consults for every audio packet**,
// and the status page, which the tray asks every second: a slow open sounds like
// stuttering audio, and an open that does not return is a monitor stopped with
// the camera on. Measured, the open costs 313 ms in the ordinary case, that is
// fifteen audio packets for every press of "Talk".
func (t *Talkback) acquire(viewer int64, now time.Time) (Player, bool) {
	t.mu.Lock()

	// Whoever already has the floor keeps it, and refreshes their own time.
	if t.speaker == viewer {
		p := t.player
		t.lastPkt = now
		t.mu.Unlock()
		return p, true
	}
	// `opening` keeps the second packet out while the first opens the device:
	// without it, fifty packets a second would open fifty audio outputs, and
	// fifty voices overlapping with themselves.
	if t.speaker != 0 || t.opening || now.Before(t.notBefore) {
		t.mu.Unlock()
		return nil, false
	}
	// Whoever has used up the maximum duration comes back only after falling
	// silent. The silence is measured on their own packets, which go on arriving
	// here even while they are refused the floor: it is the only way to separate
	// "they have finished speaking" from "they left the microphone on".
	if t.spent == viewer {
		if now.Sub(t.spentSeen) <= talkIdle {
			t.spentSeen = now
			t.mu.Unlock()
			return nil, false
		}
		t.spent, t.spentSeen = 0, time.Time{}
	}
	t.opening = true
	t.mu.Unlock()

	p, err := t.open(audiocodec.SampleRate)

	t.mu.Lock()
	defer t.mu.Unlock()
	t.opening = false
	if err != nil {
		// **It is retried in a while, and said once.** The next packet arrives
		// in twenty milliseconds, and without this pause it would retry at once:
		// where there is no output that is fifty failed calls a second, with the
		// log buried under identical lines.
		t.notBefore = now.Add(talkOpenRetry)
		t.log.Warn("talk-back unavailable: audio output not opened",
			"error", err, "retry_in", talkOpenRetry)
		return nil, false
	}
	// The track may have ended in the meantime: closing straight away what was
	// just opened is better than leaving it open with no owner.
	if t.speaker != 0 {
		_ = p.Close()
		return nil, false
	}
	t.speaker, t.player = viewer, p
	t.since, t.lastPkt = now, now
	t.log.Info("talk-back", "session", viewer, "state", "speaking")
	return p, true
}

// release takes the floor away and closes the output.
func (t *Talkback) release(viewer int64, reason string) {
	t.mu.Lock()
	if t.speaker != viewer {
		t.mu.Unlock()
		return
	}
	p, held := t.player, time.Since(t.since)
	t.speaker, t.player = 0, nil
	t.mu.Unlock()

	if p != nil {
		_ = p.Close()
	}
	t.log.Info("talk-back", "session", viewer, "state", "ended",
		"duration", held.Round(time.Millisecond), "reason", reason)
}

// expire closes a turn left without packets or gone on too long.
//
// **The monitor counts the time, not the browser.** A page closing abruptly, a
// network dropping, a phone freezing: in all three cases the packets stop
// arriving and nobody tells us, and without this the room would stay mute for
// ever.
func (t *Talkback) expire(now time.Time) {
	t.mu.Lock()
	viewer, since, last := t.speaker, t.since, t.lastPkt
	t.mu.Unlock()
	if viewer == 0 {
		return
	}
	switch {
	case now.Sub(last) > talkIdle:
		t.release(viewer, "silence")
	case now.Sub(since) > talkMax:
		t.mu.Lock()
		t.spent, t.spentSeen = viewer, now
		t.mu.Unlock()
		t.release(viewer, "time limit")
	}
}

// Receive consumes an audio track arriving from a viewer.
//
// It runs for the whole life of the track: WebRTC opens it at negotiation and
// keeps it, so nothing arrives here until somebody presses to speak.
func (t *Talkback) Receive(track *webrtc.TrackRemote, viewer int64) {
	if t == nil || t.open == nil {
		return
	}
	dec, err := audiocodec.NewOpusDecoder(audiocodec.SampleRate, audiocodec.Channels)
	if err != nil {
		t.log.Warn("talk-back: decoder not created", "error", err)
		return
	}
	defer dec.Close()
	defer t.release(viewer, "track closed")

	// The expiry has to be watched even when nothing is arriving, which is
	// exactly the case it is needed for.
	done := make(chan struct{})
	defer close(done)
	guard.Go(t.log, "the talk-back expiry", func() {
		tick := time.NewTicker(500 * time.Millisecond)
		defer tick.Stop()
		for {
			select {
			case <-done:
				return
			case now := <-tick.C:
				t.expire(now)
			}
		}
	})

	// **A refusal is said once only.** Whoever speaks while somebody else has
	// the floor sends fifty packets a second, and one line for each would bury a
	// night's log.
	refused := false

	for {
		pkt, _, err := track.ReadRTP()
		if err != nil {
			if !errors.Is(err, io.EOF) {
				t.log.Debug("talk-back: read finished", "session", viewer, "error", err)
			}
			return
		}
		if len(pkt.Payload) == 0 {
			continue
		}

		p, ok := t.acquire(viewer, time.Now())
		if !ok {
			if !refused {
				t.log.Info("talk-back refused: someone else already has the floor",
					"session", viewer, "speaker", t.Speaker())
				refused = true
			}
			continue
		}
		refused = false

		pcm, err := dec.Decode(pkt.Payload)
		if err != nil {
			// An unreadable packet does not end the turn: it is concealed and we
			// carry on, which is what concealment exists for.
			if pcm, err = dec.Conceal(); err != nil {
				continue
			}
		}
		p.Write(pcm)
	}
}
