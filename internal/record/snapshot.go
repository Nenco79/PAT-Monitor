package record

import "time"

// Snapshot is what the ring held at one instant.
//
// It exists because writing to a file **cannot happen where the frames
// arrive**: those callbacks run on the goroutine pinned to the thread reading
// from the camera, and the contract of pipeline.Sinks says they must not block.
// The snapshot is taken under the lock in a microsecond and handed to whoever
// has time.
//
// The slice headers **are copied**, the bytes are not: that way the snapshot
// survives whatever the ring does afterwards — pruning the audio reuses its
// array, and without the copy a snapshot taken earlier would change under the
// feet of whoever is writing it.
type Snapshot struct {
	SPS, PPS []byte
	Video    []Frame
	Audio    []Packet
}

// Span is how much time the stored video covers.
func (s Snapshot) Span() time.Duration {
	if len(s.Video) < 2 {
		return 0
	}
	return s.Video[len(s.Video)-1].At.Sub(s.Video[0].At)
}

// Bytes is how much the stored video weighs.
func (s Snapshot) Bytes() int {
	n := 0
	for _, f := range s.Video {
		n += len(f.Data)
	}
	return n
}

// Snapshot returns what the ring holds right now.
//
// The audio is cut to the video's window: a packet older than the first frame
// would give a clip that starts with sound over a black screen, and one more
// recent than the last would stretch the audio track past the video one.
//
// **What falls outside the cut is not a negligible remainder.** The
// misalignment is at most one packet, twenty milliseconds, only if the audio
// arrives without interruption and starts before the video — and the two
// captures have separate lives precisely because that is no guarantee. The
// microphone may open after the camera or reopen halfway through, and then the
// interval is seconds. It is not fixed here: WriteClip declares it in the file,
// along with the gaps.
func (r *Ring) Snapshot() Snapshot {
	r.mu.Lock()
	defer r.mu.Unlock()

	s := Snapshot{
		SPS: r.sps,
		PPS: r.pps,
	}
	if n := r.frameCount(); n > 0 {
		s.Video = make([]Frame, 0, n)
		for _, g := range r.gops {
			s.Video = append(s.Video, g.frames...)
		}
	}
	if len(s.Video) == 0 {
		return s
	}

	first, last := s.Video[0].At, s.Video[len(s.Video)-1].At
	for _, p := range r.audio {
		if p.At.Before(first) || p.At.After(last) {
			continue
		}
		s.Audio = append(s.Audio, p)
	}
	return s
}

// Stats sums up the ring's state. It goes to the log at -v: it is the only way
// to know the pre-roll is there before it is actually needed.
type Stats struct {
	Gops         int
	Frames       int
	Bytes        int
	Span         time.Duration
	AudioPackets int

	// Resets counts the times a rebuilt encoder emptied the ring. It is the
	// first thing to look at if a pre-roll comes out short.
	Resets int64
	// Dropped counts the groups thrown away to make room.
	Dropped int64
	// Refused counts the frames that arrived before a keyframe. If it never
	// stops climbing, the keyframes are not arriving.
	Refused int64
}

// Stats photographs the counters.
func (r *Ring) Stats() Stats {
	r.mu.Lock()
	defer r.mu.Unlock()

	st := Stats{
		Gops:         len(r.gops),
		Frames:       r.frameCount(),
		Bytes:        r.bytes,
		AudioPackets: len(r.audio),
		Resets:       r.resets,
		Dropped:      r.dropped,
		Refused:      r.refused,
	}
	if first, ok := r.firstFrame(); ok {
		if g := r.gops[len(r.gops)-1]; len(g.frames) > 0 {
			st.Span = g.frames[len(g.frames)-1].At.Sub(first.At)
		}
	}
	return st
}
