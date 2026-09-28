package record

import (
	"errors"
	"io"
	"time"

	"patmonitor/internal/audiocodec"
	"patmonitor/internal/media"
)

const (
	// opusChannels is how many channels are declared. The SDP's warning
	// applies: the stream is mono, players expect the declaration to say two.
	opusChannels = 2

	// minSampleDuration keeps the duration above zero. Two frames delivered at
	// the same instant really do happen — on AMD a third of the intervals are
	// bursts, `min 0s` — and a sample of zero duration is not a sample. It is
	// borrowed from the interval after, not added: see videoDurations.
	minSampleDuration = time.Millisecond
)

// ErrNoVideo says there is nothing to write.
var ErrNoVideo = errors.New("record: the pre-roll has no video")

// writeClip writes the snapshot as a progressive MP4.
//
// MP4 and not a raw .h264 because the pre-roll carries **the audio too**, and a
// bare Annex-B cannot hold it.
//
// **Progressive and not fragmented**: without an index an fMP4 does not declare
// how long it lasts, and on the first download Explorer answered with an empty
// duration while the same clip rewritten progressive answered four seconds. It
// costs a thousand bytes on a megabyte and a half. The fragmented one stays
// where its job is: sending a file that is not finished yet.
//
// **The durations come from the arrival instants, not from a constant.**
// AccessUnit carries no time — the camera's PTS is discarded by the pipeline,
// and the hub invents the duration from the measured cadence — so the ring
// notes the instant each piece reaches it, and here those instants become the
// timeline.
//
// **The two tracks sit on the same clock, and that is the invariant not to
// break.** Audio and video have separate lives on purpose: the microphone may
// open after the camera or reopen halfway through, and every missing interval
// on either of them has to be declared in full. Shortening it — a cap on a
// sample's duration, a packet assumed equal to the previous one — makes
// **everything after it** slide against the other track, and a slide is not
// visible looking at the file: it is heard watching the clip.
func writeClip(w io.Writer, s Snapshot) error {
	if len(s.Video) == 0 {
		return ErrNoVideo
	}
	if len(s.SPS) == 0 || len(s.PPS) == 0 {
		return media.ErrNoParameterSets
	}

	clip, err := media.NewMP4Clip(s.SPS, s.PPS, opusChannels)
	if err != nil {
		return err
	}
	video := videoDurations(s.Video)
	var audio []time.Duration
	if len(s.Audio) > 0 {
		audio = audioDurations(s.Audio)
		// **The picture lasts as long as the sound that follows it.** A clip
		// in progress keeps the room's audio after the camera has stopped —
		// the sound is what is worth having then — and the last frame has
		// no successor to take its end from. Its end is the audio track's,
		// which is measured: what is invented is only in the other direction.
		var videoEnd, audioEnd time.Duration
		for _, d := range video {
			videoEnd += d
		}
		audioEnd = s.Audio[0].At.Sub(s.Video[0].At)
		for _, d := range audio {
			audioEnd += d
		}
		if audioEnd > videoEnd {
			video[len(video)-1] += audioEnd - videoEnd
		}
	}
	for i, f := range s.Video {
		if err := clip.AddVideo(f.Data, video[i]); err != nil {
			return err
		}
	}
	if len(s.Audio) > 0 {
		// The snapshot already drops the audio preceding the first frame, so
		// this interval is never negative.
		clip.SetAudioDelay(s.Audio[0].At.Sub(s.Video[0].At))
		for i := range s.Audio {
			clip.AddAudio(s.Audio[i].Data, audio[i])
		}
	}
	return clip.Marshal(w)
}

// videoDurations is how long each frame lasts, that is, the interval up to the
// next one.
//
// **There is no cap, and one must not be added**: if the camera stops for three
// seconds, the truth is that that picture stayed on screen for three seconds.
// Truncating it to one would move every following frame two seconds earlier,
// and with them the sync with the audio, which lived those three seconds in
// full. A frame that lasts a long time is visible and true; audio that slides
// is invisible and false.
//
// The last one has no successor and takes the previous one's duration:
// declaring an invented one would be the only figure in the clip that does not
// come from a measurement.
//
// **Each frame is placed where it arrived, and a burst borrows.** A frame
// arriving with its predecessor still needs a duration, and that millisecond
// used to be added: nothing took it back, so on AMD, where a third of the
// intervals are bursts, the picture ended up behind the sound — some 140 ms on
// an ordinary clip, 0.6 s on one at the length ceiling. A frame's end is now its
// successor's arrival, pushed later only as far as the floor needs, and the
// next interval pays it back.
func videoDurations(frames []Frame) []time.Duration {
	out := make([]time.Duration, len(frames))
	var pos time.Duration // where the current frame starts on the timeline
	for i := range frames {
		var end time.Duration
		switch {
		case i+1 < len(frames):
			end = frames[i+1].At.Sub(frames[0].At)
		case i > 0:
			end = pos + frames[i].At.Sub(frames[i-1].At)
		default:
			// A single frame: there is no interval to derive it from.
			end = pos + audiocodec.FrameDuration
		}
		end = max(end, pos+minSampleDuration)
		out[i] = end - pos
		pos = end
	}
	return out
}

// audioDurations is how long each packet lasts, **a whole number of Opus
// frames**.
//
// The sum is not done on the raw interval as it is for video, and the reason is
// that here the truth is known: a packet carries exactly 20 ms of sound. The
// arrival instants, on the other hand, wobble — measured by pat-capture, from
// 12.6 to 27.5 ms with a median of 20 — because they say when WASAPI handed us
// the block, not how much sound is inside it. Declaring 12.6 ms for a packet
// holding 20 would be false on every line.
//
// **A gap stays a gap**: a microphone reopening after half a second is worth
// twenty-five frames, and those twenty-five are declared instead of bringing
// everything else forward.
//
// **The gap is measured against where the packet was due, not against the
// previous arrival.** Each gap used to be rounded on its own and floored at a
// frame, so a late packet was counted twice — once as a gap, once when the next
// short interval was raised to a whole frame — and every stall followed by a
// burst moved the audio twenty milliseconds later for the rest of the clip. Now
// a packet lies right after the previous one unless it arrived at least a frame
// after it was due, and then the whole frames of that lateness, and no more,
// are declared as a gap: a late packet absorbs its own lateness.
func audioDurations(packets []Packet) []time.Duration {
	out := make([]time.Duration, len(packets))
	var pos time.Duration // where the current packet starts, from the first
	for i := range packets {
		out[i] = audiocodec.FrameDuration
		if i+1 < len(packets) {
			due := pos + audiocodec.FrameDuration
			if late := packets[i+1].At.Sub(packets[0].At) - due; late >= audiocodec.FrameDuration {
				out[i] += late / audiocodec.FrameDuration * audiocodec.FrameDuration
			}
		}
		pos += out[i]
	}
	return out
}
