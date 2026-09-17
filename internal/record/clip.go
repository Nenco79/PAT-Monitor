package record

import (
	"errors"
	"io"
	"time"

	"patmonitor/internal/media"
)

const (
	// opusFrameDuration is the duration of an Opus packet. It is the hub's own
	// constant, and it holds because we produce them: audiocodec emits one
	// packet for every complete 20 ms frame.
	opusFrameDuration = 20 * time.Millisecond

	// opusChannels is how many channels are declared. The SDP's warning
	// applies: the stream is mono, players expect the declaration to say two.
	opusChannels = 2

	// minSampleDuration keeps the duration above zero. Two frames delivered at
	// the same instant really do happen — on AMD a third of the intervals are
	// bursts, `min 0s` — and a sample of zero duration is not a sample.
	minSampleDuration = time.Millisecond
)

// ErrNoVideo says there is nothing to write.
var ErrNoVideo = errors.New("record: the pre-roll has no video")

// WriteClip writes the snapshot as a progressive MP4.
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
func WriteClip(w io.Writer, s Snapshot) error {
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
	for i, f := range s.Video {
		if err := clip.AddVideo(f.Data, videoDuration(s.Video, i)); err != nil {
			return err
		}
	}
	if len(s.Audio) > 0 {
		// The snapshot already drops the audio preceding the first frame, so
		// this interval is never negative.
		clip.SetAudioDelay(s.Audio[0].At.Sub(s.Video[0].At))
		for i := range s.Audio {
			clip.AddAudio(s.Audio[i].Data, audioDuration(s.Audio, i))
		}
	}
	return clip.Marshal(w)
}

// videoDuration is how long frame i lasts, that is, the interval up to the next
// one.
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
func videoDuration(frames []Frame, i int) time.Duration {
	var d time.Duration
	switch {
	case i+1 < len(frames):
		d = frames[i+1].At.Sub(frames[i].At)
	case i > 0:
		d = frames[i].At.Sub(frames[i-1].At)
	default:
		// A single frame: there is no interval to derive it from.
		d = opusFrameDuration
	}
	return max(d, minSampleDuration)
}

// audioDuration is how long packet i lasts, **rounded to a whole number of Opus
// frames**.
//
// The sum is not done on the raw interval as it is for video, and the reason is
// that here the truth is known: a packet carries exactly 20 ms of sound. The
// arrival instants, on the other hand, wobble — measured by pat-capture, from
// 12.6 to 27.5 ms with a median of 20 — because they say when WASAPI handed us
// the block, not how much sound is inside it. Declaring 12.6 ms for a packet
// holding 20 would be false on every line.
//
// Rounding to whole frames makes all the jitter fall on one of them, and **a
// gap stays a gap**: a microphone reopening after half a second is worth
// twenty-five frames, and those twenty-five are declared instead of bringing
// everything else forward.
func audioDuration(packets []Packet, i int) time.Duration {
	if i+1 >= len(packets) {
		return opusFrameDuration
	}
	gap := packets[i+1].At.Sub(packets[i].At)
	frames := (gap + opusFrameDuration/2) / opusFrameDuration
	return max(frames, 1) * opusFrameDuration
}
