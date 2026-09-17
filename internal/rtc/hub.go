// Package rtc delivers the encoded streams to browsers over WebRTC.
//
// The media travels peer-to-peer: the signalling goes through the web server
// (little traffic, fine even over a relayed tunnel), while video and audio
// negotiate a direct path with ICE. That way no ports need opening on the router
// and the video crosses no relay.
package rtc

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"strconv"
	"sync"
	"sync/atomic"
	"time"

	"github.com/pion/interceptor"
	"github.com/pion/interceptor/pkg/cc"
	"github.com/pion/interceptor/pkg/gcc"
	"github.com/pion/interceptor/pkg/stats"
	"github.com/pion/rtcp"
	"github.com/pion/webrtc/v4"
	"github.com/pion/webrtc/v4/pkg/media"

	patmedia "patmonitor/internal/media"

	"patmonitor/internal/guard"
)

// defaultVideoFrameDuration corresponds to 30 fps and is the fallback when the
// configuration names no framerate.
const defaultVideoFrameDuration = time.Second / 30

// opusFrameDuration corresponds to -frame_duration 20 passed to libopus.
const opusFrameDuration = 20 * time.Millisecond

// Opus SDP parameters.
const (
	// opusSDPChannels is always 2, even when the audio is mono.
	//
	// It is not a mistake: RFC 7587 prescribes that in Opus's a=rtpmap the
	// channel count always be 2, because the real count travels inside the
	// bitstream and not in the SDP. Declaring "opus/48000/1", however much
	// better it describes what we send, matches nothing in the browsers and makes
	// the negotiation fail with "codec is not supported by remote".
	opusSDPChannels = 2

	// opusFmtpLine is the line browsers expect. useinbandfec asks for error
	// correction inside the stream, useful on home Wi-Fi where the odd packet
	// gets lost.
	opusFmtpLine = "minptime=10;useinbandfec=1"
)

// Config configures the hub.
type Config struct {
	ICEServers []webrtc.ICEServer
	// FPS is the preset's cadence. It seeds the media clock's first interval and
	// the cadence declared to the encoder, both of which then chase the measured
	// one: a nominal number is a starting point here, never a limit — see
	// nextVideoDurationAt and cadence.go.
	FPS int

	// BitrateKbps is the preset's bitrate: the cap the congestion control can
	// only bring down.
	BitrateKbps int

	// AudioKbps is the share the audio takes from the bandwidth, and which is
	// therefore not available to the video.
	AudioKbps int

	// OnBitrate receives the bitrate to impose on the encoder when the measured
	// bandwidth calls for it. Nil disables the adjustment entirely.
	OnBitrate func(kbps int)

	// Width and Height are the preset's size: the cap that nothing raises. Zero
	// disables the resolution scale.
	//
	// **They are not the size the capture starts at.** On a camera with fewer
	// pixels that one is smaller, and it can change while the monitor runs: it
	// arrives through VideoStartSize, and it is what the scale's steps are built
	// from. These remain the fallback for a Hub with no such answer to read.
	Width, Height int

	// VideoStartSize is the size the capture starts from **now**: the preset
	// lowered to what the camera in use really declares. Nil leaves Width,
	// Height and FPS to decide, which is what a Hub built without it gets.
	//
	// It is a function and not a value because the camera can be changed while
	// the monitor runs, and the resolution scale is built on this size: read once
	// at start-up, it would describe a camera that is no longer there.
	VideoStartSize func() (w, h, fps int)

	// LevelIDC is the H.264 level_idc announced in the SDP, and it is the
	// **preset's**, not this capture's: see the comment on profileLevelID for
	// why the two differ and why announcing the larger of them is the safe
	// direction. Zero keeps the stream's own level, which is what a Hub built
	// without it gets.
	LevelIDC byte

	// OnVideoFormat receives the size and cadence to impose when the available
	// bits no longer cover all the pixels. Nil disables the scale: only the
	// bitrate adjustment remains.
	//
	// The cadence arrives together with the size and not by another road because
	// the two are the same step: below the last size the scale carries on by
	// taking frames away instead of pixels, and whoever receives them has to
	// apply them in one go. See scale.go.
	// The cadences are two and have to be delivered separately: `delivered` is
	// how many frames must reach the encoder — that is, the resolution scale —
	// `declared` how many are announced to it so it divides the budget by the
	// right number. Passing only one, whoever receives it has to guess which of
	// the two jobs it serves, and guessing wrong closes a loop: see videoFormat
	// in internal/pipeline.
	OnVideoFormat func(w, h, delivered, declared int)

	// VideoFormat says what the pipeline is **really** sending: size and frames
	// delivered.
	//
	// **It exists because a format request may not reach its destination.**
	// `OnVideoFormat` deposits it, and whoever applies it may give up — the
	// camera that does not deliver that size, the encoder that will not be born —
	// or lose it entirely: **a capture restart starts again from the preset**,
	// and nobody says so. From that moment the scale reasons about a step we are
	// not sending, and since it commands only **on changes** it never notices:
	// `atFullSize()` false switches the saving off and nails the bitrate to the
	// cap while the full pixels go onto the network.
	//
	// It is the usual rule — **the check is taken as close as possible to
	// whoever consumes the datum** — applied to the governor: the pipeline is not
	// asked whether it accepted, what it sends is looked at. Nil disables the
	// realignment.
	//
	// The cadence to compare is the **delivered** one, not the declared: the
	// scale commands the first, and the second chases the camera on its own.
	VideoFormat func() (w, h, fps int)

	// TargetQP is the quantiser to hold: the bitrate comes down when the picture
	// comes out better than that, because those bits are not visible. Zero
	// switches the saving off and leaves the bitrate at the network's cap.
	TargetQP int

	// QP reports how much the encoder is approximating, and whether it declares
	// it at all.
	//
	// The hub does not use it to decide: it writes it in the log next to the
	// bandwidth, because it is the only number that answers "but does it look
	// bad?" without somebody having to sit and watch the screen. Bitrate and
	// estimate say what we spent, not what came of it.
	QP func() (qp int, known bool)

	// MotionStarted says whether an episode of movement in the room has begun
	// since the last time it was asked, and **consumes the signal by reading
	// it**, like QP.
	//
	// It is the only sensor that says "bandwidth is about to be needed" without
	// going through the encoder, and it serves one thing: making the quality loop
	// give back in one go the discount it took while the room was still. See
	// quality.go.
	//
	// The hub does not know who produces it, as with the quantiser: a boolean
	// arrives here, and what an episode is is decided by `internal/detect`.
	MotionStarted func() bool

	// OnKeyframeRequest is invoked when a viewer reports having lost the
	// reference. Nil means the encoder cannot produce a keyframe on demand: then
	// the only mitigation left is the short GOP.
	//
	// The hub does not know the encoder on purpose: all that is known here is
	// that somebody needs a keyframe, not who has to produce it.
	OnKeyframeRequest func()

	// OpenPlayer opens the audio output for talk-back. Nil disables it: the hub
	// announces no incoming track, and the page does not show the button.
	//
	// It is a function and not a device because the output opens **when somebody
	// speaks** and closes straight afterwards: the speakers do not stay occupied
	// all night by a feature used for ten seconds.
	OpenPlayer OpenPlayer

	Log *slog.Logger
}

// Stats exposes the hub's counters.
type Stats struct {
	ViewersNow   atomic.Int64
	ViewersTotal atomic.Int64
	KeyframeReqs atomic.Int64
	// KeyframeForced counts the requests actually forwarded to the encoder. The
	// difference from KeyframeReqs is the ones absorbed by the minimum interval,
	// and it is the number to look at when a storm is suspected.
	KeyframeForced atomic.Int64
	VideoSamples   atomic.Int64
	AudioSamples   atomic.Int64
	WriteFailures  atomic.Int64
	ConnectFailure atomic.Int64
}

// Hub owns the shared tracks and creates one PeerConnection per viewer.
type Hub struct {
	cfg   Config
	log   *slog.Logger
	Stats Stats

	// talk governs the talk-back: who holds the floor, and the audio output
	// while they do. Nil when there is no OpenPlayer.
	talk *Talkback

	mu         sync.RWMutex
	videoTrack *webrtc.TrackLocalStaticSample
	audioTrack *webrtc.TrackLocalStaticSample
	// profileLevelID is what the SDP announces, and its two halves have two
	// different sources.
	//
	// **Profile and constraint flags come from the SPS of the real stream**, not
	// from the encoder's probe: the two can differ — measured, 4d001f against
	// 4d0020 — and what the browser must agree with is what is actually sent.
	//
	// **The level comes from the preset**, through Config.LevelIDC. A stream below
	// the announced level is not a problem: that field says how much capacity to
	// allocate, and allocating more than needed breaks nothing — measured on Edge,
	// 640x360 announced as 3.1, 151 frames and none dropped. What this used to
	// rest on was that the first SPS is the full-pixel one "because the capture
	// always starts from the preset", and that premise is false: PickCameraSize
	// lowers the size to what the camera really has, so on a small webcam the
	// frozen value was the *camera's*. It held by luck — the scale can only come
	// down from where it started — and it stops holding the moment the camera can
	// change while the monitor runs.
	//
	// **It freezes on the first value seen, and does not chase the stream.**
	// Rebuilding the encoder smaller or slower, the new SPS carries a lower level
	// — measured on wifi, 42e01f -> 42e016 coming down to 640x352@6, and back to
	// 42e01f climbing. What the freeze guards is that NewViewer reads this field
	// **at the moment of the offer**: whoever connected while the stream was down
	// would negotiate the low level and then receive a stream **above** their own
	// contract. It has happened — one session negotiated 2.2 and received 3.1 for
	// eleven minutes. Chrome decoded it anyway, but "this decoder forgives" is not
	// a guarantee: it is the sort of thing discovered on somebody else's phone.
	profileLevelID string
	// streamPLID is the last level read from the SPS. It serves the alarm alone:
	// the watch stays **non-directional**, because the case that really matters
	// is a stream rising above the preset, and that would be a fault now too.
	streamPLID string
	sps, pps   []byte

	ready     chan struct{}
	readyOnce sync.Once

	// frameDuration is constant for the whole life of the hub, so it is read
	// without a lock.
	frameDuration time.Duration

	// The video's media clock: it serves to keep the time declared in the RTP
	// timestamps aligned with real time.
	clockMu     sync.Mutex
	videoStart  time.Time
	videoMedia  time.Duration
	lastFrameAt time.Time
	// avgInterval is the moving average of the real interval between frames, used
	// for the timestamps. For the framerate shown to the user an exact count is
	// needed instead: see MeasuredFPS.
	avgInterval time.Duration
	frameCount  int64
	// A sliding window for the framerate shown to the user: how many frames in
	// the recent stretch, not how many since the start. See MeasuredFPS.
	fpsWinStart time.Time
	fpsWinCount int64
	fpsRecent   float64

	// The last keyframe asked of the encoder, so as not to ask for more than one
	// at a time.
	keyframeMu     sync.Mutex
	lastKeyframeAt time.Time

	// One bandwidth estimate per viewer: there is only one encoder and it adapts
	// to the worst. See worstEstimate.
	bweMu sync.Mutex
	bwe   map[*Viewer]viewerEstimate
	// targetKbps is the last bitrate imposed, for the status page.
	targetKbps atomic.Int64

	// The losses declared by the receivers in the RTCP reports: the only direct
	// proof that we are sending more than gets through. See believableDrop.
	lossMu sync.Mutex
	// lossWorst is the worst fraction declared, and lossWorstAt is when **it**
	// was declared — not when the last report of any kind arrived. Keeping a
	// single instant for the two meant that, with two viewers, the healthy one's
	// reports refreshed the other's loss window: the worst value never expired
	// and stayed nailed down. Observed, 89.8% for over a minute after whoever
	// was losing had already left.
	lossWorst   float64
	lossWorstAt time.Time
	// videoBytes counts the encoded bytes that came out of the encoder, and
	// measuredKbps the throughput derived from them. They serve to check that the
	// encoder really does what it is asked: see checkOvershoot.
	videoBytes   atomic.Int64
	measuredKbps atomic.Int64
}

// keyframeMinGap is the minimum interval between two forced keyframes.
//
// The video track is one for all the viewers: a keyframe repairs them all at
// once, so PLIs arriving close together ask for the same thing several times.
// Serving them one by one is counter-productive exactly when it matters most:
// losses arrive in groups when the bandwidth drops, and a keyframe weighs about
// ten ordinary frames — congestion would be answered by sending more data.
//
// Half a second is a quarter of the GOP: the first request is served at once,
// which is the case that matters.
const keyframeMinGap = 500 * time.Millisecond

// senderSSRC reads the SSRC assigned to a sender, zero if it has none yet.
func senderSSRC(s *webrtc.RTPSender) uint32 {
	enc := s.GetParameters().Encodings
	if len(enc) == 0 {
		return 0
	}
	return uint32(enc[0].SSRC)
}

// requestKeyframe forwards the request to the encoder while respecting the
// minimum interval. It returns true if it was forwarded.
func (h *Hub) requestKeyframe(now time.Time) bool {
	if h.cfg.OnKeyframeRequest == nil {
		return false
	}
	h.keyframeMu.Lock()
	if !h.lastKeyframeAt.IsZero() && now.Sub(h.lastKeyframeAt) < keyframeMinGap {
		h.keyframeMu.Unlock()
		return false
	}
	h.lastKeyframeAt = now
	h.keyframeMu.Unlock()

	h.Stats.KeyframeForced.Add(1)
	h.cfg.OnKeyframeRequest()
	return true
}

// emaWeight tunes how responsive the moving average is: higher is smoother. At
// ~30 frames per second, 16 corresponds to half a second of memory.
const emaWeight = 16

// videoClockGapReset is the pause beyond which the media clock restarts from
// zero. It covers capture restarts, the PC suspending and the webcam
// reconnecting: without it, on return real time would be far ahead of media time
// and the correction would try to recover the gap for minutes.
const videoClockGapReset = 2 * time.Second

// nextVideoDuration returns the duration to declare for the next frame.
//
// The nominal framerate is not enough as a reference: the real one changes with
// the filming conditions. In the dark the webcam's automatic exposure lengthens
// the exposure time and the cadence drops (measured: 20 fps instead of 30, at
// every resolution). Declaring 33.3 ms per frame anyway would make media time
// advance far more slowly than real time and the delay would grow.
//
// So a moving average of the actual interval is used. The average is needed
// because the frames come out of the pipe in bursts: using the instantaneous
// interval would give near-zero durations to the frames close together. On top
// of it a small correction realigns media time with real time, to absorb the
// average's residual errors.
func (h *Hub) nextVideoDuration() time.Duration {
	return h.nextVideoDurationAt(time.Now())
}

// nextVideoDurationAt is nextVideoDuration with the instant made explicit, so
// the clock's behaviour can be checked at cadences that would be awkward to
// reproduce live, like a webcam dropping to a few frames in the dark.
func (h *Hub) nextVideoDurationAt(now time.Time) time.Duration {
	h.clockMu.Lock()
	defer h.clockMu.Unlock()

	gap := !h.lastFrameAt.IsZero() && now.Sub(h.lastFrameAt) > videoClockGapReset
	last := h.lastFrameAt
	h.lastFrameAt = now

	if h.videoStart.IsZero() || gap {
		h.videoStart = now
		h.videoMedia = h.frameDuration
		h.avgInterval = h.frameDuration
		h.frameCount = 1
		h.fpsWinStart = now
		h.fpsWinCount = 1
		h.fpsRecent = 0
		return h.frameDuration
	}
	h.frameCount++
	h.countFrameForFPS(now)

	// The raw interval is clamped before entering the average: a burst must not
	// drag it to zero, nor an isolated pause make it explode.
	//
	// The limits follow the current average, not the nominal framerate. Anchoring
	// them to the nominal would make them a fixed cap — at 30 fps declared, four
	// times 33 ms is 133 ms, that is 7.5 fps — and a webcam dropping below that
	// threshold in the dark would see its own media time advance more slowly than
	// real time, with the delay starting to grow again. Following the average
	// instead, a prolonged slowdown is reached in a few frames whatever its size,
	// while a single anomalous interval stays damped. Real pauses are caught by
	// videoClockGapReset anyway.
	interval := now.Sub(last)
	if lo := h.avgInterval / 4; interval < lo {
		interval = lo
	}
	if hi := h.avgInterval * 4; interval > hi {
		interval = hi
	}
	h.avgInterval += (interval - h.avgInterval) / emaWeight

	// positive drift: media time is ahead of real time.
	//
	// The limit on the correction decides how quickly the clock recovers a gap.
	// Keeping it at an eighth of the interval makes it too mild: at low cadences,
	// where the moving average takes a few seconds to adjust, the gap accumulated
	// in the meantime is reabsorbed so slowly that it stays visible for half a
	// minute. At half the interval the recovery is four times quicker and the
	// declared durations still stay within half again of their value, which the
	// receiver absorbs without difficulty.
	maxCorrection := h.avgInterval / 2
	drift := h.videoMedia - now.Sub(h.videoStart)
	correction := -drift
	if correction > maxCorrection {
		correction = maxCorrection
	}
	if correction < -maxCorrection {
		correction = -maxCorrection
	}

	dur := h.avgInterval + correction
	if dur < time.Millisecond {
		dur = time.Millisecond
	}
	h.videoMedia += dur
	return dur
}

// MeasuredFPS returns the framerate really delivered by the source.
//
// It is a count of frames, not the moving average used for the timestamps: with
// bursty arrivals that average is a biased estimator, because the truncation of
// the short intervals and of the long ones does not cancel out. Here an exact
// number is needed, because it is shown to the user to tell a slow webcam from a
// defect in the streaming.
//
// **And it is a count over a recent window, not since the start of the capture.**
// A cumulative number ages instead of measuring: a webcam that started at 20 fps
// in the dark goes on declaring 20 even after the room has brightened and the
// cadence has gone back to 30, because the average is dominated by the previous
// half hour. The symptom, as reported by whoever was watching, was precise: "if
// it starts at 20 it never goes up again; if it starts at 24 or 27 then it
// varies" — that is, the average moved only when it was already near the true
// value.
//
// A number that ages is worse than an absent number: it sends somebody looking
// for a fault in the camera while the camera has already recovered.
func (h *Hub) MeasuredFPS() float64 { return h.measuredFPSAt(time.Now()) }

// fpsWindow is the stretch the cadence shown to the user is counted over.
//
// Five seconds is long enough to contain a hundred frames or so — with bursty
// arrivals a shorter window would give a number that jumps — and short enough to
// follow a light coming on without being dragged by what happened half an hour
// earlier.
const fpsWindow = 5 * time.Second

// countFrameForFPS updates the sliding window. It has to be called with clockMu
// held.
func (h *Hub) countFrameForFPS(now time.Time) {
	h.fpsWinCount++
	if elapsed := now.Sub(h.fpsWinStart); elapsed >= fpsWindow {
		h.fpsRecent = float64(h.fpsWinCount) / elapsed.Seconds()
		h.fpsWinStart = now
		h.fpsWinCount = 0
	}
}

// measuredFPSAt is MeasuredFPS with the instant made explicit, so it can be
// checked at cadences that from life would mean waiting for sunset.
func (h *Hub) measuredFPSAt(now time.Time) float64 {
	h.clockMu.Lock()
	defer h.clockMu.Unlock()
	if h.videoStart.IsZero() || h.frameCount < 2 {
		return 0
	}
	// **An open window that has outrun its length answers instead of the closed
	// one.** `fpsRecent` is recomputed only when a frame arrives, so with the
	// frames stopped it went on answering the last closed window for ever: a
	// capture restart lasts up to half a minute, and for all of it the status
	// page declared the cadence of before — a camera that has stopped, reported
	// as delivering twenty-four frames a second. The gap reset three functions
	// up clears it, but only when a frame arrives, which is precisely what is
	// not happening.
	//
	// This is the paragraph above turned round: **a number that ages is worse
	// than an absent number**, and the direction that hides a stopped camera is
	// the worse of the two. Counting the open window against real time answers
	// with the frames there have really been — which decays towards zero on its
	// own while nothing arrives, and needs no new state to do it.
	//
	// In ordinary running this costs nothing: a frame closes the window within
	// a frame's time of its reaching five seconds, so the branch is only taken
	// when the frames really have stopped.
	if elapsed := now.Sub(h.fpsWinStart); elapsed >= fpsWindow {
		return float64(h.fpsWinCount) / elapsed.Seconds()
	}
	// Until the first window has closed it answers with what there is, otherwise
	// at start-up the status page would show zero for five seconds and it would
	// look as though the video were not starting.
	if h.fpsRecent == 0 {
		elapsed := now.Sub(h.videoStart).Seconds()
		if elapsed <= 0 {
			return 0
		}
		return float64(h.frameCount) / elapsed
	}
	return h.fpsRecent
}

func New(cfg Config) *Hub {
	if cfg.Log == nil {
		cfg.Log = slog.Default()
	}
	dur := defaultVideoFrameDuration
	if cfg.FPS > 0 {
		dur = time.Second / time.Duration(cfg.FPS)
	}
	h := &Hub{
		cfg:           cfg,
		log:           cfg.Log,
		ready:         make(chan struct{}),
		frameDuration: dur,
	}
	if cfg.OpenPlayer != nil {
		h.talk = NewTalkback(cfg.OpenPlayer, cfg.Log)
	}
	return h
}

// TalkbackActive says whether somebody is speaking into the room right now.
//
// The page needs it, and the question it answers is not "who is speaking": it is
// **"is my voice getting through?"**. Whoever presses to speak while the floor
// already belongs to somebody else would otherwise see the button turn green and
// nothing happen, which is the worst shape of fault — everything answers
// correctly and nothing occurs.
func (h *Hub) TalkbackActive() bool { return h.talk.Active() }

// WaitReady waits for the first keyframe with the parameter sets to arrive, as
// they are needed to build a correct SDP.
func (h *Hub) WaitReady(ctx context.Context) error {
	select {
	case <-h.ready:
		return nil
	case <-ctx.Done():
		return fmt.Errorf("video stream not ready yet: %w", ctx.Err())
	}
}

// Ready says whether the hub can accept viewers.
func (h *Hub) Ready() bool {
	select {
	case <-h.ready:
		return true
	default:
		return false
	}
}

// ICEServers returns the hole-punching servers, to be passed to the browser too.
//
// They have to be given to both sides: if the browser does not receive them it
// discovers only its own local addresses, and the connection succeeds only when
// viewer and monitor are on the same network. From a cellular network the only
// candidate it would offer is a private address of the operator's, unreachable
// from here.
func (h *Hub) ICEServers() []webrtc.ICEServer {
	return h.cfg.ICEServers
}

// ProfileLevelID returns the value announced in the SDP.
func (h *Hub) ProfileLevelID() string {
	h.mu.RLock()
	defer h.mu.RUnlock()
	return h.profileLevelID
}

// WriteVideo delivers an Access Unit to the tracks. It has to be called from the
// pipeline's video sink.
func (h *Hub) WriteVideo(au patmedia.AccessUnit) {
	// Update the parameter sets: they serve both for the SDP and for knowing
	// when the hub is ready.
	patmedia.IterateAnnexB(au.Data, func(n patmedia.NAL) bool {
		switch n.Type {
		case patmedia.NALTypeSPS:
			h.setSPS(n.Data)
		case patmedia.NALTypePPS:
			h.setPPS(n.Data)
		}
		return true
	})

	track := h.ensureTracks()
	if track == nil {
		return // parameter sets not known yet
	}

	// The duration is NOT the interval between two arrivals: the frames come out
	// of the pipe in bursts, and measuring the arrivals would give near-zero
	// durations to several consecutive frames. RTP time would advance far less
	// than real time, the browser's playback buffer would grow and the delay
	// would accumulate without end.
	if err := track.WriteSample(media.Sample{
		Data:     au.Data,
		Duration: h.nextVideoDuration(),
	}); err != nil {
		// With zero viewers the write is not an error: the track is simply not
		// bound to any sender.
		if !errors.Is(err, io.ErrClosedPipe) {
			h.Stats.WriteFailures.Add(1)
		}
		return
	}
	h.Stats.VideoSamples.Add(1)
	h.videoBytes.Add(int64(len(au.Data)))
}

// WriteAudio delivers an Opus packet to the tracks.
//
// The packet arrives ready from the encoder: there is no container left to take
// apart, because the Ogg served only to pull the packets out of an external
// process.
func (h *Hub) WriteAudio(packet []byte) {
	h.mu.RLock()
	track := h.audioTrack
	h.mu.RUnlock()
	if track == nil {
		return
	}
	// **Half duplex: while somebody speaks, the room is not sent.** If it were,
	// the microphone would pick up the speakers and the speaker would hear
	// themselves come back — the intercom howl. It lasts as long as the sentence,
	// and it holds for every viewer because the outgoing track is a single shared
	// one: see talkback.go.
	if h.talk.Active() {
		return
	}
	if err := track.WriteSample(media.Sample{Data: packet, Duration: opusFrameDuration}); err != nil {
		if !errors.Is(err, io.ErrClosedPipe) {
			h.Stats.WriteFailures.Add(1)
		}
		return
	}
	h.Stats.AudioSamples.Add(1)
}

func (h *Hub) setSPS(sps []byte) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if len(h.sps) == len(sps) && string(h.sps) == string(sps) {
		return
	}
	h.sps = append(h.sps[:0], sps...)
	if id, err := patmedia.ProfileLevelID(sps); err == nil {
		if h.streamPLID != "" && h.streamPLID != id {
			h.log.Warn("profile-level-id changed while running",
				"from", h.streamPLID, "to", id, "announced", h.profileLevelID,
				"note", "the SDP keeps announcing the full-resolution level")
		}
		h.streamPLID = id
		// **Only the first enters the SDP.** See the comment on the field:
		// following it would give whoever connects in a moment of scarce
		// bandwidth a tighter contract than the one they will need in a minute.
		if h.profileLevelID == "" {
			h.profileLevelID = h.announce(sps, id)
		}
	}
}

func (h *Hub) setPPS(pps []byte) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.pps = append(h.pps[:0], pps...)
}

// ensureTracks creates the shared tracks as soon as the parameter sets are
// known.
//
// The tracks are born once only and are added to every PeerConnection: the
// encoding happens once regardless of the number of viewers, which is the point
// of having a hub.
func (h *Hub) ensureTracks() *webrtc.TrackLocalStaticSample {
	h.mu.RLock()
	if h.videoTrack != nil {
		t := h.videoTrack
		h.mu.RUnlock()
		return t
	}
	plid, hasPPS := h.profileLevelID, len(h.pps) > 0
	h.mu.RUnlock()

	if plid == "" || !hasPPS {
		return nil
	}

	h.mu.Lock()
	defer h.mu.Unlock()
	if h.videoTrack != nil {
		return h.videoTrack
	}

	video, err := webrtc.NewTrackLocalStaticSample(
		webrtc.RTPCodecCapability{
			MimeType:    webrtc.MimeTypeH264,
			ClockRate:   90000,
			SDPFmtpLine: h264FmtpLine(h.profileLevelID),
		}, "video", "pat-monitor")
	if err != nil {
		h.log.Error("video track creation", "error", err)
		return nil
	}
	audio, err := webrtc.NewTrackLocalStaticSample(
		webrtc.RTPCodecCapability{
			MimeType:    webrtc.MimeTypeOpus,
			ClockRate:   48000,
			Channels:    opusSDPChannels,
			SDPFmtpLine: opusFmtpLine,
		}, "audio", "pat-monitor")
	if err != nil {
		h.log.Error("audio track creation", "error", err)
		return nil
	}

	h.videoTrack, h.audioTrack = video, audio
	h.log.Info("WebRTC tracks ready", "profile-level-id", h.profileLevelID)
	h.readyOnce.Do(func() { close(h.ready) })
	return h.videoTrack
}

// startSize is the size the capture starts from, or zeros when nobody can say.
// Zero is "I do not know" and never "no pixels": whoever reads it must leave
// what it has alone.
func (h *Hub) startSize() (w, height, fps int) {
	if h.cfg.VideoStartSize == nil {
		return 0, 0, 0
	}
	return h.cfg.VideoStartSize()
}

// announce composes the profile-level-id for the SDP: profile and constraint
// flags from the real stream, because those describe the encoder, and the level
// from the preset, because that is the only bound the whole session respects.
//
// **The comparison is a belt and should never bite.** By construction the
// stream cannot exceed the preset — the size only comes down from where the
// capture started and the bitrate cap only comes down — so a stream above the
// announced level means that construction is broken somewhere. It is here
// because the two directions do not cost the same: below the announcement is
// free, above it is what stops a decoder.
func (h *Hub) announce(sps []byte, streamID string) string {
	if h.cfg.LevelIDC == 0 || len(streamID) < 4 {
		return streamID
	}
	if len(sps) > 3 && sps[3] > h.cfg.LevelIDC {
		return streamID
	}
	return streamID[:4] + fmt.Sprintf("%02x", h.cfg.LevelIDC)
}

func h264FmtpLine(profileLevelID string) string {
	// packetization-mode=1 enables the STAP-A/FU-A, indispensable because a
	// keyframe does not fit in a single UDP packet.
	return "level-asymmetry-allowed=1;packetization-mode=1;profile-level-id=" + profileLevelID
}

// Viewer is one viewer's WebRTC session.
type Viewer struct {
	pc  *webrtc.PeerConnection
	hub *Hub
	log *slog.Logger

	id        int64
	startedAt time.Time

	// The source of the report's numbers: the statistics interceptor, and the
	// SSRCs to question it with.
	stats                stats.Getter
	videoSSRC, audioSSRC uint32

	// State of the periodic report: see Report.
	statsMu  sync.Mutex
	lastSnap sessionSnapshot

	// counted remembers whether this viewer entered the count of the connected
	// ones, and it is needed because the fact has to be **remembered, not
	// inferred**.
	//
	// Reading it back from the connection's state at the moment of closing
	// always finds a state that has already changed: whoever calls Close is the
	// state-change handler, which runs **after** the transition, so it finds
	// `closed` and takes nothing away. Every reload of the page then makes a +1
	// without the -1, and the viewer count climbs for ever. The count is also
	// what answers the question "who is watching now?", which on a baby monitor
	// is not an ornament.
	//
	// The compare-and-swap serves the rest: it guarantees that the +1 and the -1
	// happen once only even if Close is called from several points, which does
	// happen — a dropped WebSocket, a failed state, session revocation.
	counted atomic.Bool

	// talkTr is the talk-back's transceiver. It serves only to read its mid
	// after SetLocalDescription: before that the mid does not exist yet.
	talkTr *webrtc.RTPTransceiver

	// iceSnap is the negotiation's facts, taken while the agent was still there.
	//
	// **Measured, and it is the whole reason this field exists**: with the peer
	// connection closed, GetStats answers zero candidates, zero pairs and zero
	// checks — not an error, zeros. The facts are asked for exactly when a
	// session failed to connect, and a failure closes the session, so the
	// obvious shape reads the agent one instant after it has gone and the log
	// says "the viewer sent no candidates" about a browser that sent five. It is
	// the family of the final report's `last_video_kbps=0`, one governor across,
	// and worse, because that one looked wrong while this one looks like a
	// diagnosis.
	iceSnap atomic.Pointer[ICEFacts]

	// candSent and candRefused count what arrived over the signalling channel
	// and what pion would not take of it. They are here and not in the agent's
	// statistics because the agent cannot know them: a candidate the browser
	// never sent and one it sent badly are the same absence from where it sits,
	// and they are two different faults — the second is ours.
	candSent, candRefused atomic.Int64

	closeOnce sync.Once
	closed    chan struct{}
}

// TalkMid is the m-line the viewer can send their own voice on. Empty when
// talk-back is not available.
func (v *Viewer) TalkMid() string {
	if v == nil || v.talkTr == nil {
		return ""
	}
	return v.talkTr.Mid()
}

// NewViewer creates a PeerConnection with the shared tracks and produces the
// offer.
//
// The server makes the offer: that way it controls the m-lines and the codecs
// announced directly, avoiding the mismatches that arise from answering the
// offers of different browsers.
func (h *Hub) NewViewer() (*Viewer, *webrtc.SessionDescription, error) {
	h.mu.RLock()
	video, audio, plid := h.videoTrack, h.audioTrack, h.profileLevelID
	h.mu.RUnlock()

	if video == nil || audio == nil {
		return nil, nil, errors.New("stream not ready yet: no keyframe received")
	}

	engine := &webrtc.MediaEngine{}
	if err := engine.RegisterCodec(webrtc.RTPCodecParameters{
		RTPCodecCapability: webrtc.RTPCodecCapability{
			MimeType:    webrtc.MimeTypeH264,
			ClockRate:   90000,
			SDPFmtpLine: h264FmtpLine(plid),
		},
		PayloadType: 102,
	}, webrtc.RTPCodecTypeVideo); err != nil {
		return nil, nil, fmt.Errorf("registering the H.264 codec: %w", err)
	}
	if err := engine.RegisterCodec(webrtc.RTPCodecParameters{
		RTPCodecCapability: webrtc.RTPCodecCapability{
			MimeType:    webrtc.MimeTypeOpus,
			ClockRate:   48000,
			Channels:    opusSDPChannels,
			SDPFmtpLine: opusFmtpLine,
		},
		PayloadType: 111,
	}, webrtc.RTPCodecTypeAudio); err != nil {
		return nil, nil, fmt.Errorf("registering the Opus codec: %w", err)
	}

	ir := &interceptor.Registry{}

	// The stream statistics recorder.
	//
	// It is needed because `PeerConnection.GetStats()` **produces nothing for
	// the sender**: the type `webrtc.OutboundRTPStreamStats` exists, can be read
	// from the report and stays at zero for ever, because in Pion only the
	// receivers populate it. Building a report on top of that gives a report of
	// zeros that looks like a dead connection — measured on a session that was
	// meanwhile delivering audio and video perfectly well.
	//
	// The real numbers live in the statistics interceptor, which has to be
	// questioned per SSRC. RegisterDefaultInterceptors already installs one, but
	// hands its Getter to a map private to the package: the only way to have it
	// is to register one of our own.
	var statsGetter stats.Getter
	statsFactory, err := stats.NewInterceptor()
	if err != nil {
		return nil, nil, fmt.Errorf("stream statistics: %w", err)
	}
	statsFactory.OnNewPeerConnection(func(_ string, g stats.Getter) { statsGetter = g })
	ir.Add(statsFactory)

	// Congestion control, if somebody gathers its conclusions.
	//
	// The estimate costs nothing to produce — the browser sends the TWCC feedback
	// anyway — but it has to be asked for: without the TWCC extension on the way
	// out the packets do not carry the sequence number the feedback is based on,
	// and the estimate stays at zero for ever without saying so.
	var estimator cc.BandwidthEstimator
	if h.cfg.OnBitrate != nil {
		factory, err := cc.NewInterceptor(func() (cc.BandwidthEstimator, error) {
			return gcc.NewSendSideBWE(
				// The limits are in transport bandwidth, not in video bitrate:
				// the cap is what the preset at full quality occupies on the
				// wire, audio and headers included. Giving gcc the video number
				// alone would keep the estimate below what is needed and the
				// encoder would never return to the preset.
				//
				// It starts from the cap and not from a cautious value: starting
				// lower means beginning every viewing with a worse picture and
				// then climbing, and whoever opens the monitor at night watches
				// precisely the first seconds.
				gcc.SendSideBWEInitialBitrate(transportBudget(h.cfg.BitrateKbps, h.cfg.AudioKbps)*1000),
				gcc.SendSideBWEMaxBitrate(transportBudget(h.cfg.BitrateKbps, h.cfg.AudioKbps)*1000),
				gcc.SendSideBWEMinBitrate(transportBudget(bitrateFloorKbps, h.cfg.AudioKbps)*1000),
				// No pacer: the cadence of the packets is already dictated by the
				// media clock, and queueing them a second time here would only
				// add delay.
				gcc.SendSideBWEPacer(gcc.NewNoOpPacer()),
			)
		})
		if err != nil {
			return nil, nil, fmt.Errorf("congestion control: %w", err)
		}
		factory.OnNewPeerConnection(func(_ string, e cc.BandwidthEstimator) { estimator = e })
		ir.Add(factory)
		if err := webrtc.ConfigureTWCCHeaderExtensionSender(engine, ir); err != nil {
			return nil, nil, fmt.Errorf("TWCC extension: %w", err)
		}
	}

	if err := webrtc.RegisterDefaultInterceptors(engine, ir); err != nil {
		return nil, nil, fmt.Errorf("interceptor: %w", err)
	}

	api := webrtc.NewAPI(webrtc.WithMediaEngine(engine), webrtc.WithInterceptorRegistry(ir))
	pc, err := api.NewPeerConnection(webrtc.Configuration{ICEServers: h.cfg.ICEServers})
	if err != nil {
		return nil, nil, fmt.Errorf("creating the PeerConnection: %w", err)
	}

	v := &Viewer{
		pc: pc, hub: h, log: h.log,
		id:        nextSessionID(),
		startedAt: time.Now(),
		closed:    make(chan struct{}),
	}

	videoSender, err := pc.AddTrack(video)
	if err != nil {
		_ = pc.Close()
		return nil, nil, fmt.Errorf("adding the video track: %w", err)
	}
	audioSender, err := pc.AddTrack(audio)
	if err != nil {
		_ = pc.Close()
		return nil, nil, fmt.Errorf("adding the audio track: %w", err)
	}

	// The talk-back track is announced **straight away, in the offer**, even if
	// nobody ever speaks.
	//
	// **It is the reason pressing "talk" renegotiates nothing.** If the m-line
	// appeared at the moment of need, the browser would have to make a new offer
	// and the monitor accept it, that is a complete renegotiation inside the
	// user's gesture — with its round of ICE and its class of faults, exactly
	// when somebody is in a hurry to speak. Announcing it empty leaves the
	// browser one thing to do: attach the microphone to the place already laid.
	if h.talk != nil {
		tr, err := pc.AddTransceiverFromKind(webrtc.RTPCodecTypeAudio,
			webrtc.RTPTransceiverInit{Direction: webrtc.RTPTransceiverDirectionRecvonly},
		)
		if err != nil {
			_ = pc.Close()
			return nil, nil, fmt.Errorf("talk-back track: %w", err)
		}
		v.talkTr = tr
		pc.OnTrack(func(track *webrtc.TrackRemote, _ *webrtc.RTPReceiver) {
			if track.Kind() != webrtc.RTPCodecTypeAudio {
				return
			}
			h.talk.Receive(track, v.id)
		})
	}

	// The statistics are questioned per SSRC, and the SSRC is assigned by the
	// sender: it has to be read now and kept, because at report time the sender
	// is no longer to hand.
	v.stats = statsGetter
	v.videoSSRC = senderSSRC(videoSender)
	v.audioSSRC = senderSSRC(audioSender)

	// The senders have to be read: without consuming the incoming RTCP, Pion
	// accumulates the packets and does not process the reception reports.
	guard.Go(h.log, "the video RTCP drain", func() { v.drainRTCP(videoSender, true) })
	guard.Go(h.log, "the audio RTCP drain", func() { v.drainRTCP(audioSender, false) })

	pc.OnConnectionStateChange(func(s webrtc.PeerConnectionState) {
		v.log.Info("viewer connection state", "session", v.id, "state", s.String())
		switch s {
		case webrtc.PeerConnectionStateConnected:
			// Having been counted is **remembered**, not read back from the
			// connection's state: see counted.
			if v.counted.CompareAndSwap(false, true) {
				h.Stats.ViewersNow.Add(1)
				h.Stats.ViewersTotal.Add(1)
			}
		case webrtc.PeerConnectionStateDisconnected, webrtc.PeerConnectionStateClosed:
			v.Close()
		case webrtc.PeerConnectionStateFailed:
			h.Stats.ConnectFailure.Add(1)
			v.Close()
		}
	})

	offer, err := pc.CreateOffer(nil)
	if err != nil {
		_ = pc.Close()
		return nil, nil, fmt.Errorf("creating the offer: %w", err)
	}
	if err := pc.SetLocalDescription(offer); err != nil {
		_ = pc.Close()
		return nil, nil, fmt.Errorf("SetLocalDescription: %w", err)
	}

	// **The estimate is put into the hub's map last, once nothing below can
	// fail.** The interceptor's callback fires inside NewPeerConnection, that
	// is before the viewer exists, so it is collected up there; registering it
	// up there as well is what made the entry outlive the five failures in
	// between — adding the two tracks, the talk-back transceiver, the offer and
	// SetLocalDescription. Each of those closes the PeerConnection and returns
	// an error, and **the only thing that removes an entry is Viewer.Close**,
	// which nobody can call: the viewer is never handed to the caller.
	//
	// What that leaks is not the memory. `worstEstimate` counts the map, and
	// its count is the loop's `viewers`: one dead entry and **`viewers == 0`
	// never happens again**, so `bitrateGovernor.release`, `qualityGovernor.
	// release` and `scaleGovernor.release` stop being called for the life of
	// the process. Every viewer from then on inherits the discount, the
	// quantiser window and the estimate of whoever left — which is exactly the
	// state those three functions were written to prevent, and each of them
	// carries the measurement of what it cost. And the worst estimate is a
	// minimum: a dead viewer's last one, frozen, would hold the encoder down
	// for everybody who came after.
	//
	// Registering last removes the class rather than the five instances. There
	// is nothing between here and the return that can fail, and nothing else
	// can reach this viewer yet: it is published to the caller on the next
	// line.
	if estimator != nil {
		h.bweMu.Lock()
		if h.bwe == nil {
			h.bwe = make(map[*Viewer]viewerEstimate)
		}
		h.bwe[v] = viewerEstimate{bwe: estimator, since: time.Now()}
		h.bweMu.Unlock()
	}
	return v, pc.LocalDescription(), nil
}

// drainRTCP consumes a sender's RTCP and counts the keyframe requests.
func (v *Viewer) drainRTCP(sender *webrtc.RTPSender, video bool) {
	buf := make([]byte, 1500)
	for {
		n, _, err := sender.Read(buf)
		if err != nil {
			return
		}
		if !video {
			continue
		}
		pkts, err := rtcp.Unmarshal(buf[:n])
		if err != nil {
			continue
		}
		for _, p := range pkts {
			// A PLI means the browser has lost the reference: until it receives a
			// keyframe it has nothing to show, and the picture stays frozen. With
			// the GOP at 2 seconds as the only answer the wait reaches two
			// seconds of frozen picture; asking the encoder reduces it to one
			// frame.
			if _, ok := p.(*rtcp.PictureLossIndication); ok {
				v.hub.Stats.KeyframeReqs.Add(1)
				v.hub.requestKeyframe(time.Now())
			}
			// The fraction lost declared by the receiver is the only direct proof
			// that we are sending more than gets through. It arrives here about
			// once a second, already computed over the interval, and does not
			// disturb the session report, which keeps its own counters.
			if rr, ok := p.(*rtcp.ReceiverReport); ok {
				for _, r := range rr.Reports {
					v.hub.recordLoss(float64(r.FractionLost)/256, time.Now())
				}
			}
		}
	}
}

// SetAnswer applies the browser's SDP answer.
func (v *Viewer) SetAnswer(answer webrtc.SessionDescription) error {
	if err := v.pc.SetRemoteDescription(answer); err != nil {
		return fmt.Errorf("SetRemoteDescription: %w", err)
	}
	return nil
}

// AddICECandidate adds a candidate received from the browser.
//
// The candidate goes into the log too: when the connection will not establish
// itself, the two lists of candidates are the only thing that says why. A browser
// offering only private addresses is behind a NAT that cannot be punched; one
// offering only IPv6 has no way of speaking to our IPv4 candidates.
//
// **It is also the only place that can count them**, which is why the tally is
// taken here rather than read back from the agent afterwards: see ICEFacts.
func (v *Viewer) AddICECandidate(c webrtc.ICECandidateInit) error {
	v.log.Debug("remote candidate", "candidate", c.Candidate)
	v.candSent.Add(1)
	if err := v.pc.AddICECandidate(c); err != nil {
		v.candRefused.Add(1)
		return err
	}
	return nil
}

// OnICECandidate registers the callback for the local candidates (trickle ICE).
func (v *Viewer) OnICECandidate(fn func(*webrtc.ICECandidate)) {
	v.pc.OnICECandidate(func(c *webrtc.ICECandidate) {
		if c != nil {
			v.log.Debug("local candidate",
				"type", c.Typ.String(),
				"proto", c.Protocol.String(),
				"address", net.JoinHostPort(c.Address, strconv.Itoa(int(c.Port))))
		}
		fn(c)
	})
}

// Done closes when the session ends.
func (v *Viewer) Done() <-chan struct{} { return v.closed }

// SelectedPair describes the ICE path chosen — `host` on the same network,
// `srflx` through a NAT — which is the datum that says whether a connection
// from outside really worked. It is read by the log, and by the details.
func (v *Viewer) SelectedPair() string {
	// **Absence is declared empty, not in words.** With a phrase like "in
	// negotiation" here, the comparison that separates "I do not know yet" from a
	// real path is made on that text, in two places in `viewerlog.go`: that is a
	// decision taken in one language, which the first translation would switch
	// off in silence. It is the `MicHealth` family.
	pair, err := v.selectedPair()
	if err != nil || pair == nil {
		return ""
	}
	// Typ is an integer enum: converting it with string() would give a rune, not
	// the name ("host", "srflx", "relay").
	return pair.Local.Typ.String() + " <-> " + pair.Remote.Typ.String()
}

// CloseUnlessConnected closes the session only if the media is not already
// flowing, and says whether it closed it.
//
// It serves whoever owns the signalling channel: that can drop on its own — an
// intermediary closing an idle WebSocket, the phone changing network — while
// audio and video carry on perfectly well, because after the negotiation they
// travel by a road that has nothing more to do with the signalling. Closing there
// would mean interrupting the viewing over a fault that does not concern it.
//
// Whoever really leaves is not left hanging: ICE stops receiving consent and the
// connection's state machine goes to disconnected and then to failed, which close
// the session by their own road.
func (v *Viewer) CloseUnlessConnected() bool {
	if v.pc.ConnectionState() == webrtc.PeerConnectionStateConnected {
		return false
	}
	v.Close()
	return true
}

// Close closes the session, once only even when invoked from several points.
//
// **The first thing it does is remember, and the order is the point**: see
// iceSnap. Everything below it takes away the agent that answers those
// questions.
func (v *Viewer) Close() {
	v.closeOnce.Do(func() {
		v.rememberICEFacts()
		if v.counted.CompareAndSwap(true, false) {
			v.hub.Stats.ViewersNow.Add(-1)
		}
		// The estimate leaves with the viewer: keeping it would mean adapting the
		// encoder to the network of whoever is no longer watching.
		v.hub.bweMu.Lock()
		delete(v.hub.bwe, v)
		v.hub.bweMu.Unlock()
		_ = v.pc.Close()
		close(v.closed)
	})
}
