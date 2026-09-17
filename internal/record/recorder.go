package record

import (
	"log/slog"
	"sync"
	"time"

	"patmonitor/internal/media"
)

const (
	// DefaultPostRoll is how long recording continues after an event.
	//
	// Ten seconds are the minimum that answers the question the clip exists for
	// — "and then what happened?" — and they sit under the 10-30 seconds
	// surveillance recorders use. It gets shortened if somebody has to watch
	// many of them, not before having watched one.
	DefaultPostRoll = 10 * time.Second

	// maxClipDuration is the ceiling on how far a clip can stretch.
	//
	// An event arriving while another is being recorded **moves the finish line
	// forward** instead of opening a second overlapping clip: that is the right
	// thing for an episode that continues, and it is also a way never to
	// finish. The ceiling settles it without a knob.
	maxClipDuration = time.Minute

	// maxClipBytes is the ceiling on the memory of a clip in progress. A minute
	// at full bitrate sits well below it; it is there for an encoder producing
	// far more than it is asked for, which on this project is not hypothetical.
	maxClipBytes = 32 << 20
)

// CodeManual is the code of clips asked for by hand.
//
// **It is a code like the alerts' even though no alert emits it**, and for the
// same reason: it becomes part of the file name, so it has to go through
// UsableCode — a malformed code produces a clip that listing, pruning and
// deletion **all skip**, that is, an invisible file taking up space and
// counting in neither ceiling.
//
// It lives here and not where the button is pressed: three places read it — the
// recorder, whoever composes the name, and the page that gives it a word — and
// written three times it would diverge.
const CodeManual = "manual"

// Clip is a complete recording, ready to be written.
type Clip struct {
	Snapshot
	// Code is the code of the event that asked for it.
	Code string
	// At is the instant of the event, that is, the boundary between the seconds
	// before and those after. It is the file's name, and for whoever watches it
	// is the moment to look for.
	At time.Time
	// Keep asks that the clip be born **held**, that is, exempt from the
	// retention.
	//
	// **Only a person asks for it.** An event's clip is written by the monitor
	// on its own, and if nobody watches it, it has to be able to expire: there
	// are dozens a night, and the promise about the disk of whoever hosts us is
	// worth more than the memory of a movement at three in the morning. A clip
	// asked for by hand, on the other hand, exists **because** somebody wanted
	// it, and it is the only one known to be wanted again: deleting it by age
	// would be throwing away the one thing that was expressly asked for.
	//
	// The other direction stays one click away — the recordings page unlocks it,
	// and from then on the retention applies as it does to all the others —
	// because **it can always be released, while an expired clip cannot be got
	// back.**
	Keep bool
}

// RecorderConfig describes a recorder.
type RecorderConfig struct {
	// PostRoll is how long recording lasts after the event. Zero means
	// DefaultPostRoll.
	PostRoll time.Duration
	Log      *slog.Logger
}

// Recorder keeps the pre-roll and, when an event calls it, records the seconds
// that follow as well.
//
// **It owns the ring**, and that is not a convenience: the two have to see
// exactly the same frames in the same order, otherwise a frame would end up in
// both or in neither, and in the clip that would show as a jump right on the
// event's boundary. Whoever captures calls only this.
type Recorder struct {
	ring *Ring
	post time.Duration
	log  *slog.Logger

	// The queue holds one: the writing happens elsewhere and lasts far less
	// than a clip, but if the disk were slow, queueing would mean keeping clips
	// in memory that nobody is writing.
	clips chan Clip

	mu       sync.Mutex
	armed    bool
	code     string
	keep     bool
	eventAt  time.Time
	deadline time.Time
	sps, pps []byte
	video    []Frame
	audio    []Packet
	bytes    int

	triggers int64
	written  int64
	dropped  int64
	cut      int64
}

// NewRecorder prepares a recorder with its ring.
func NewRecorder(cfg RecorderConfig) *Recorder {
	if cfg.Log == nil {
		cfg.Log = slog.New(slog.DiscardHandler)
	}
	if cfg.PostRoll <= 0 {
		cfg.PostRoll = DefaultPostRoll
	}
	return &Recorder{
		ring:  NewRing(cfg.Log),
		post:  cfg.PostRoll,
		log:   cfg.Log,
		clips: make(chan Clip, 1),
	}
}

// Clips hands over the finished recordings. Whoever reads writes them wherever
// they like.
func (r *Recorder) Clips() <-chan Clip { return r.clips }

// WriteVideo passes an access unit to the ring and, if recording is under way,
// to the clip in progress as well.
func (r *Recorder) WriteVideo(au media.AccessUnit, now time.Time) {
	r.mu.Lock()
	defer r.mu.Unlock()

	r.ring.WriteVideo(au, now)
	if !r.armed {
		return
	}

	// **A picture that changes shape closes the clip.** An MP4 carries one pair
	// of parameter sets, and the samples do not carry it with them: the ones
	// coded with the other pair would not decode. Better a clip that ends early
	// than one that breaks halfway.
	//
	// The question, though, is the ring's own, and comparing the bytes is
	// **stricter than necessary**: a rebuild at the same size — the cadence
	// chasing the light, four times in half a minute — would truncate the clip
	// to a few seconds. See compatibleSets.
	if au.Keyframe {
		if sps, pps := parameterSets(au.Data); len(sps) > 0 && len(pps) > 0 &&
			!compatibleSets(r.sps, r.pps, sps, pps) {
			r.log.Debug("clip cut short: the picture changed shape", "code", r.code)
			r.cut++
			r.finish()
			return
		}
	}

	r.video = append(r.video, Frame{Data: au.Data, At: now, Keyframe: au.Keyframe})
	r.bytes += len(au.Data)
	r.checkDone(now)
}

// WriteAudio passes an Opus packet to the ring and to the clip in progress.
func (r *Recorder) WriteAudio(pkt []byte, now time.Time) {
	r.mu.Lock()
	defer r.mu.Unlock()

	r.ring.WriteAudio(pkt, now)
	if !r.armed {
		return
	}
	r.audio = append(r.audio, Packet{Data: pkt, At: now})
	r.checkDone(now)
}

// Trigger asks for a clip for the given event.
//
// **The seconds before are taken now**, not when the clip closes: in ten
// seconds the ring will have thrown away exactly those, and this is the only
// instant they are still there.
//
// If recording is already under way, the finish line moves forward instead of a
// second clip being opened: an episode that continues is one episode, not two.
//
// **It says whether recording is under way from now on**, and the value is for
// one caller only: whoever pressed a button. Before the first keyframe there is
// nothing to save, and a command that does nothing without saying so is a knob
// that moves nothing — the notice reaches the viewer instead of staying in a
// log line. Events ignore it: nobody is waiting for an answer.
func (r *Recorder) Trigger(code string, now time.Time) bool {
	return r.trigger(code, false, now)
}

// TriggerKept asks for a clip born **held**, that is, exempt from the
// retention. See Clip.Keep: a person asks for it, not the monitor.
//
// **If a clip is already in progress it does not open a second one: it gives
// that one the lock** and moves the finish line, as any other trigger does. The
// code stays that of the event that opened it, and rightly so — what happened
// in the room is that — but the request to keep it is not lost: whoever pressed
// "Record" during a movement wanted **that** clip.
func (r *Recorder) TriggerKept(code string, now time.Time) bool {
	return r.trigger(code, true, now)
}

// trigger does the work of both.
func (r *Recorder) trigger(code string, keep bool, now time.Time) bool {
	r.mu.Lock()
	defer r.mu.Unlock()

	r.triggers++
	if r.armed {
		// The lock adds up: between whoever asks for it and whoever does not
		// mention it, whoever asks wins. The other way round would lose an
		// explicit request because an event nobody asked for arrived.
		r.keep = r.keep || keep
		if d := now.Add(r.post); d.After(r.deadline) {
			r.deadline = d
		}
		r.checkDone(now)
		// **True even if checkDone has just closed it**: what the caller asked
		// for — a clip of now — exists, and it is on its way to the disk. The
		// question being answered is "was something recorded?", not "is it
		// still recording?".
		return true
	}

	s := r.ring.Snapshot()
	if len(s.Video) == 0 {
		// Before the first keyframe there is nothing to save. It is not a
		// fault: it is the start-up, and it lasts a few seconds.
		r.log.Debug("no clip: the ring is still empty", "code", code)
		return false
	}
	r.armed = true
	r.code = code
	r.keep = keep
	r.eventAt = now
	r.deadline = now.Add(r.post)
	r.sps, r.pps = s.SPS, s.PPS
	r.video = s.Video
	r.audio = s.Audio
	r.bytes = s.Bytes()
	return true
}

// Tick closes a clip whose time has run out.
//
// It is needed because the finish line is checked by the frames arriving, and
// if no more arrive — a camera that stops, a microphone that vanishes — the
// clip would stay open in memory forever. It has to be called at a regular
// cadence.
func (r *Recorder) Tick(now time.Time) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.armed {
		r.checkDone(now)
	}
}

// checkDone closes the clip if its time has run out or it has passed a ceiling.
func (r *Recorder) checkDone(now time.Time) {
	switch {
	case !now.Before(r.deadline):
		// The finish line, that is, the normal case: it closes and that is not
		// a cut.
	case now.Sub(r.eventAt) >= maxClipDuration:
		r.log.Debug("clip closed at the length ceiling", "code", r.code)
		r.cut++
	case r.bytes >= maxClipBytes:
		r.log.Warn("clip closed at the size ceiling", "code", r.code, "bytes", r.bytes)
		r.cut++
	default:
		return
	}
	r.finish()
}

// finish hands the clip over and goes back to rest. It is called with the lock
// held.
func (r *Recorder) finish() {
	clip := Clip{
		Snapshot: Snapshot{SPS: r.sps, PPS: r.pps, Video: r.video, Audio: r.audio},
		Code:     r.code,
		At:       r.eventAt,
		Keep:     r.keep,
	}
	r.armed = false
	r.code = ""
	r.keep = false
	r.video, r.audio, r.bytes = nil, nil, 0

	select {
	case r.clips <- clip:
		r.written++
	default:
		// The writer is still behind. This one is lost, not the ones after it:
		// the cadence of whoever records is not the disk's to dictate.
		r.dropped++
		r.log.Warn("clip dropped: the previous one is still being written",
			"code", clip.Code, "dropped", r.dropped)
	}
}

// Recording says whether a clip is in progress.
func (r *Recorder) Recording() bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.armed
}

// Stats sums up the ring and the recorder.
func (r *Recorder) Stats() RecorderStats {
	r.mu.Lock()
	st := RecorderStats{
		Recording: r.armed,
		Triggers:  r.triggers,
		Written:   r.written,
		Dropped:   r.dropped,
		Cut:       r.cut,
	}
	if r.armed {
		st.ClipFrames = len(r.video)
		st.ClipBytes = r.bytes
	}
	r.mu.Unlock()
	st.Ring = r.ring.Stats()
	return st
}

// RecorderStats is what the recorder is doing right now.
type RecorderStats struct {
	Ring Stats

	Recording  bool
	ClipFrames int
	ClipBytes  int

	Triggers int64
	Written  int64
	// Dropped counts the clips lost because the previous one was still being
	// written. If it climbs, the disk is not keeping up with the events.
	Dropped int64
	// Cut counts the clips closed before the finish line: encoder rebuilt, or a
	// ceiling reached.
	Cut int64
}
