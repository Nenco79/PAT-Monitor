// Package record keeps in memory the seconds that come **before** an event.
//
// An alert always arrives late on whatever caused it: by the time the detector
// says "movement in the room", the frames that explain what happened have gone
// past. Without a ring holding them, any clip starts after the fact.
//
// The ring knows neither files nor the network: it accumulates and hands over a
// snapshot, and whoever receives it decides where to send it. It is the same
// division as media.FMP4Muxer, and for the same reason: there is nothing in
// here that needs a disk to be tested.
package record

import (
	"bytes"
	"log/slog"
	"sync"
	"time"

	"patmonitor/internal/media"
)

const (
	// wholeGops is how many **whole** groups are kept, besides the one in
	// progress. With the GOP fixed at two seconds — and it stays fixed — that
	// is four seconds guaranteed and up to six.
	//
	// To have two **whole** ones at every instant, the one still growing has to
	// be kept as well, that is, three entries in the queue. Keeping only two
	// would give one whole one plus one halfway, that is, two seconds
	// guaranteed and not four.
	wholeGops = 2

	// maxBytes is the ceiling on memory, and it is not fussiness: if the
	// encoder stops producing keyframes — something this project allows for,
	// kfDeclaredBad exists for it — the group in progress never closes and
	// grows forever. It is the only way this ring can eat the machine.
	//
	// Eight megabytes are about four times the worst case measured: six seconds
	// at AMD's 3900 kbit/s peak is 2.9 MB. Wide enough never to bite in
	// service, tight enough not to be a fault.
	maxBytes = 8 << 20

	// maxAudioAge throws away audio that is too old even when there is no video
	// to prune it against. It really happens: audio and video have separate
	// life cycles on purpose, so the camera can stop while the microphone keeps
	// going.
	maxAudioAge = 12 * time.Second
)

// Frame is a stored access unit, with the instant it arrived.
//
// The bytes are **not copied**: media.AUAssembler hands them over already owned
// by whoever receives them — it does a make and a copy before emitting them —
// and nothing in here modifies them.
type Frame struct {
	Data     []byte
	At       time.Time
	Keyframe bool
}

// Packet is a stored Opus packet. The same note about the bytes applies:
// audiocodec already copies the packet out of its own working buffer on
// purpose, declaring that whoever receives it queues it and consumes it later.
type Packet struct {
	Data []byte
	At   time.Time
}

// group is one GOP: it begins with a keyframe and ends where another one
// begins.
type group struct {
	frames []Frame
	bytes  int
}

// Ring keeps the last whole GOPs of video and the audio that goes with them.
//
// The two Write methods are called from the goroutines of their respective
// producers, which must not block: for the video that is the one pinned to the
// thread reading from the camera. Nothing in here touches the disk or copies a
// byte.
type Ring struct {
	log *slog.Logger

	mu       sync.Mutex
	gops     []group
	audio    []Packet
	sps, pps []byte
	bytes    int

	resets  int64 // encoder rebuilt: the ring restarted from nothing
	dropped int64 // groups thrown away to make room
	refused int64 // frames that arrived before a keyframe
}

// NewRing prepares an empty ring. The log may be nil.
func NewRing(log *slog.Logger) *Ring {
	if log == nil {
		log = slog.New(slog.DiscardHandler)
	}
	return &Ring{log: log}
}

// WriteVideo appends an access unit.
//
// **The ring always begins on a keyframe**, and frames arriving before the
// first one are refused: that way there is nothing to trim when it comes to
// writing, and a piece that begins in the middle of a GOP does not decode.
func (r *Ring) WriteVideo(au media.AccessUnit, now time.Time) {
	if len(au.Data) == 0 {
		return
	}

	r.mu.Lock()
	defer r.mu.Unlock()

	if au.Keyframe {
		r.noteParameterSets(au.Data)
		r.gops = append(r.gops, group{})
		for len(r.gops) > wholeGops+1 {
			r.evictOldest()
		}
	}
	if len(r.gops) == 0 {
		// This is not a fault: it is the start of the capture, or the first
		// frame after the ceiling emptied everything. It is counted because if
		// it never stopped it would mean the keyframes are not arriving.
		r.refused++
		return
	}

	g := &r.gops[len(r.gops)-1]
	g.frames = append(g.frames, Frame{Data: au.Data, At: now, Keyframe: au.Keyframe})
	g.bytes += len(au.Data)
	r.bytes += len(au.Data)

	for r.bytes > maxBytes {
		if len(r.gops) > 1 {
			r.evictOldest()
			continue
		}
		// **A single group overflowing the ceiling is an encoder that has
		// stopped sending keyframes.** Its first frames cannot be thrown away —
		// what would be left is a piece beginning in the middle of a GOP, that
		// is, nothing usable — so everything goes and it starts again from the
		// next keyframe. Until that arrives there is no pre-roll, and that is
		// the truth: without a keyframe there was none anyway.
		r.log.Warn("preroll emptied: the encoder is not sending keyframes",
			"bytes", r.bytes, "frames", len(g.frames))
		r.clear()
		break
	}

	r.trimAudio(now)
}

// WriteAudio appends an Opus packet.
func (r *Ring) WriteAudio(pkt []byte, now time.Time) {
	if len(pkt) == 0 {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.audio = append(r.audio, Packet{Data: pkt, At: now})
	r.trimAudio(now)
}

// noteParameterSets updates the SPS and PPS and, **if they are incompatible,
// empties the ring**.
//
// Comparing the bytes here would be too strict: it would empty on every rebuild
// of the encoder. On a machine where the cadence swings — the camera dropping
// to 9 fps and climbing back, that is, any night — the rebuilds are continuous:
// four in twenty-six seconds is on record, and the signature reported by
// whoever uses it is five-second clips that start at the event, with not an
// instant of what came before.
//
// The right question is not whether the bytes are equal, it is whether the old
// samples and the new ones can live **in the same file**. See compatibleSets.
func (r *Ring) noteParameterSets(au []byte) {
	sps, pps := parameterSets(au)
	if len(sps) == 0 || len(pps) == 0 {
		// A keyframe without parameter sets says nothing new: the encoder
		// repeats them on every IDR, but if it ever stopped, keeping the
		// earlier ones is better than declaring a change that did not happen.
		return
	}
	if len(r.sps) > 0 && compatibleSets(r.sps, r.pps, sps, pps) {
		// **The first ones are kept**, not the last: they are the ones
		// everything already in the ring was coded with, and what arrives now
		// decodes with them just as well.
		return
	}
	if len(r.sps) > 0 {
		r.resets++
		r.log.Debug("preroll reset: the picture changed shape",
			"frames_lost", r.frameCount(), "resets", r.resets)
	}
	r.clear()
	r.sps = append([]byte(nil), sps...)
	r.pps = append([]byte(nil), pps...)
}

// evictOldest throws away the oldest group.
func (r *Ring) evictOldest() {
	if len(r.gops) == 0 {
		return
	}
	r.bytes -= r.gops[0].bytes
	r.gops = r.gops[1:]
	r.dropped++
}

// clear empties video and audio without touching the parameter sets or the
// counters.
func (r *Ring) clear() {
	r.gops = nil
	r.audio = nil
	r.bytes = 0
}

// trimAudio keeps the audio that accompanies the video being held.
//
// The cut sits at the more recent of the two criteria: the first video frame
// kept, and the absolute age for when there is no video at all.
func (r *Ring) trimAudio(now time.Time) {
	cut := now.Add(-maxAudioAge)
	if f, ok := r.firstFrame(); ok && f.At.After(cut) {
		cut = f.At
	}
	i := 0
	for i < len(r.audio) && r.audio[i].At.Before(cut) {
		i++
	}
	if i > 0 {
		// Shift forward reusing the array: a snapshot already taken is
		// unaffected, because it copies the headers instead of sharing them.
		r.audio = append(r.audio[:0], r.audio[i:]...)
	}
}

func (r *Ring) firstFrame() (Frame, bool) {
	for _, g := range r.gops {
		if len(g.frames) > 0 {
			return g.frames[0], true
		}
	}
	return Frame{}, false
}

func (r *Ring) frameCount() int {
	n := 0
	for _, g := range r.gops {
		n += len(g.frames)
	}
	return n
}

// parameterSets pulls the SPS and PPS out of an access unit.
func parameterSets(au []byte) (sps, pps []byte) {
	media.IterateAnnexB(au, func(n media.NAL) bool {
		switch n.Type {
		case media.NALTypeSPS:
			sps = n.Data
		case media.NALTypePPS:
			pps = n.Data
		}
		return true
	})
	return sps, pps
}

// compatibleSets says whether two parameter sets can describe the samples of
// the same file.
//
// **An MP4 carries one pair only**, in the header, and the samples do not carry
// it with them — avccSample strips the SPS and PPS from every frame, because
// repeating them would swell the file. So everything ending up in a clip has to
// be decodable with the declared pair.
//
// The PPS has to be **identical**: it carries pic_init_qp_minus26, and the
// frames' slice_qp_delta values are relative to it. A different PPS gives no
// error, it gives a picture with the wrong quantiser — a plausible number and a
// corrupt video.
//
// Of the SPS what counts is the **size**. Rebuilding the encoder at the same
// resolution changes the timing in the VUI, that is, the declared cadence: a
// field our file does not even use, because we write the sample durations
// ourselves from the arrival instants. Throwing away four seconds of pre-roll
// for that is paying a great deal for nothing.
func compatibleSets(sps1, pps1, sps2, pps2 []byte) bool {
	if !bytes.Equal(pps1, pps2) {
		return false
	}
	if bytes.Equal(sps1, sps2) {
		return true
	}
	w1, h1, err1 := media.SPSSize(sps1)
	w2, h2, err2 := media.SPSSize(sps2)
	if err1 != nil || err2 != nil {
		// An SPS that cannot be read is not judged compatible: when in doubt it
		// starts again, which costs the pre-roll and not the clip.
		return false
	}
	return w1 == w2 && h1 == h2
}
