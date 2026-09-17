package record

import (
	"bytes"
	"encoding/binary"
	"testing"
	"time"

	"github.com/bluenviron/mediacommon/v2/pkg/formats/mp4/codecs"
	"github.com/bluenviron/mediacommon/v2/pkg/formats/pmp4"
)

// boxes lists an MP4's top-level boxes, reading them from the specification:
// four bytes of length and four of type, one after another.
//
// It is written by hand on purpose. Recounting the fields with the same code
// that wrote them only proves that code is equal to itself; here the file is
// walked the way whoever consumes it walks it.
func boxes(t *testing.T, b []byte) map[string]int {
	t.Helper()
	out := map[string]int{}
	for off := 0; off+8 <= len(b); {
		size := int(binary.BigEndian.Uint32(b[off:]))
		if size < 8 || off+size > len(b) {
			t.Fatalf("malformed box at %d: it declares %d bytes out of %d available", off, size, len(b)-off)
		}
		out[string(b[off+4:off+8])] = off
		off += size
	}
	return out
}

// movieDuration reads the duration declared in the mvhd, which is the thing a
// fragmented MP4 does **not** have and the reason this clip is not fragmented.
func movieDuration(t *testing.T, b []byte, moov int) time.Duration {
	t.Helper()
	end := moov + int(binary.BigEndian.Uint32(b[moov:]))
	for off := moov + 8; off+8 <= end; {
		size := int(binary.BigEndian.Uint32(b[off:]))
		if size < 8 {
			t.Fatal("malformed box inside moov")
		}
		if string(b[off+4:off+8]) == "mvhd" {
			p := off + 8
			ver := b[p]
			p += 4 // version and flags
			if ver == 1 {
				p += 16 // 64-bit creation and modification
				scale := binary.BigEndian.Uint32(b[p:])
				dur := binary.BigEndian.Uint64(b[p+4:])
				return time.Duration(dur) * time.Second / time.Duration(scale)
			}
			p += 8 // 32-bit creation and modification
			scale := binary.BigEndian.Uint32(b[p:])
			dur := binary.BigEndian.Uint32(b[p+4:])
			return time.Duration(dur) * time.Second / time.Duration(scale)
		}
		off += size
	}
	t.Fatal("no mvhd: the file does not declare how long it lasts")
	return 0
}

// The clip has to be a real file, not bytes that look like one: two tracks, the
// video starting from a sync sample, and the audio beside it. If it started
// from a predicted sample a player would have nowhere to start, and that is
// exactly what the pre-roll exists to guarantee.
func TestAClipStartsWithAKeyframeAndCarriesBothTracks(t *testing.T) {
	const step = 100 * time.Millisecond

	r := NewRing(nil)
	at := feedGOP(t, r, sps720p, t0, 20, step)
	at = feedGOP(t, r, sps720p, at, 20, step)
	for i := 0; i < 200; i++ {
		r.WriteAudio([]byte{0xfc, byte(i)}, t0.Add(time.Duration(i)*opusFrameDuration))
	}
	s := r.Snapshot()

	var buf bytes.Buffer
	if err := WriteClip(&buf, s); err != nil {
		t.Fatalf("WriteClip: %v", err)
	}

	found := boxes(t, buf.Bytes())
	if _, ok := found["ftyp"]; !ok {
		t.Error("the file does not start with ftyp")
	}
	if _, ok := found["mdat"]; !ok {
		t.Error("no mdat: the file does not carry the samples")
	}
	moov, ok := found["moov"]
	if !ok {
		t.Fatal("no moov")
	}
	// **The property that made the format change.** An fMP4 without an index
	// does not declare how long it lasts, and Explorer answered with an empty
	// duration.
	if _, fragmented := found["moof"]; fragmented {
		t.Error("the clip is fragmented: without an index it does not declare how long it lasts")
	}
	if got, want := movieDuration(t, buf.Bytes(), moov), s.Span(); got < want-step || got > want+step {
		t.Errorf("the file declares it lasts %v, but the samples cover %v", got, want)
	}

	var pres pmp4.Presentation
	if err := pres.Unmarshal(bytes.NewReader(buf.Bytes())); err != nil {
		t.Fatalf("the file does not read back: %v", err)
	}
	if len(pres.Tracks) != 2 {
		t.Fatalf("%d tracks declared instead of 2", len(pres.Tracks))
	}
	video, audio := pres.Tracks[0], pres.Tracks[1]

	h264, ok := video.Codec.(*codecs.H264)
	if !ok {
		t.Fatalf("the first track is %T instead of H.264", video.Codec)
	}
	if !bytes.Equal(h264.SPS, s.SPS) {
		t.Error("the header carries an SPS other than the samples': the player would decode with the wrong parameters")
	}
	if _, ok := audio.Codec.(*codecs.Opus); !ok {
		t.Errorf("the second track is %T instead of Opus", audio.Codec)
	}
	if len(video.Samples) != len(s.Video) {
		t.Errorf("%d video samples instead of %d", len(video.Samples), len(s.Video))
	}
	if len(audio.Samples) != len(s.Audio) {
		t.Errorf("%d audio samples instead of %d", len(audio.Samples), len(s.Audio))
	}
	if video.Samples[0].IsNonSyncSample {
		t.Error("the first video sample is not a sync sample: the clip starts in the middle of a GOP")
	}
	// The durations come from the arrival instants, not from a constant: a
	// hundred milliseconds at 90 kHz is nine thousand units.
	if got := video.Samples[0].Duration; got != 9000 {
		t.Errorf("the first sample lasts %d units instead of 9000: the duration does not come from the arrival instants", got)
	}
}

// **The two tracks have to stay on the same clock**, and it is the invariant a
// clip does not let you check by eye: a slide between audio and video is not
// seen by opening the file, it is heard. A cap on a frame's duration is exactly
// the way to break it — it truncates the gap on one track and moves everything
// after it earlier, while the other track lived that gap in full.
//
// The scene is the bad one: the camera stops for three seconds, the microphone
// opens a second after it and in turn skips half a second.
func TestAGapKeepsTheTwoTracksOnTheSameClock(t *testing.T) {
	var video []Frame
	at := t0
	for i := 0; i < 20; i++ {
		video = append(video, Frame{At: at})
		if i == 9 {
			at = at.Add(3 * time.Second) // the camera stops
		} else {
			at = at.Add(100 * time.Millisecond)
		}
	}

	const delay = time.Second
	var audio []Packet
	at = t0.Add(delay)
	for i := 0; i < 60; i++ {
		audio = append(audio, Packet{At: at})
		if i == 29 {
			at = at.Add(500 * time.Millisecond) // the microphone reopens
		} else {
			at = at.Add(opusFrameDuration)
		}
	}

	// On the timeline a sample sits where the sum of the durations before it
	// puts it. That has to match the wall clock.
	var sum time.Duration
	for i := range video {
		if actual := video[i].At.Sub(video[0].At); sum != actual {
			t.Fatalf("frame %d falls at %v on the timeline but arrived at %v", i, sum, actual)
		}
		sum += videoDuration(video, i)
	}

	sum = delay
	for i := range audio {
		actual := audio[i].At.Sub(video[0].At)
		if gap := sum - actual; gap > opusFrameDuration || gap < -opusFrameDuration {
			t.Fatalf("packet %d falls at %v but arrived at %v: %v of slide", i, sum, actual, gap)
		}
		sum += audioDuration(audio, i)
	}
}

// Computing the audio's delay is not enough: it has to end up in the file,
// otherwise everything is heard that interval early.
func TestALateMicrophoneIsDeclaredInTheFile(t *testing.T) {
	const step = 100 * time.Millisecond
	r := NewRing(nil)
	feedGOP(t, r, sps720p, t0, 20, step)
	// The microphone arrives a second after the camera.
	for i := 0; i < 50; i++ {
		r.WriteAudio([]byte{0xfc, byte(i)}, t0.Add(time.Second+time.Duration(i)*opusFrameDuration))
	}

	var buf bytes.Buffer
	if err := WriteClip(&buf, r.Snapshot()); err != nil {
		t.Fatalf("WriteClip: %v", err)
	}
	if _, ok := boxes(t, buf.Bytes())["moov"]; !ok {
		t.Fatal("no moov")
	}

	var pres pmp4.Presentation
	if err := pres.Unmarshal(bytes.NewReader(buf.Bytes())); err != nil {
		t.Fatalf("the file does not read back: %v", err)
	}
	if len(pres.Tracks) != 2 {
		t.Fatalf("%d tracks instead of 2", len(pres.Tracks))
	}
	got := time.Duration(pres.Tracks[1].TimeOffset) * time.Second / time.Duration(pres.Tracks[1].TimeScale)
	if got < 900*time.Millisecond || got > 1100*time.Millisecond {
		t.Errorf("the file declares an audio delay of %v instead of about 1s", got)
	}
}

// Two frames delivered in the same instant — on AMD a third of the intervals
// are bursts — do not produce a sample of zero duration.
func TestTwoFramesInTheSameInstantStillHaveADuration(t *testing.T) {
	if got := videoDuration([]Frame{{At: t0}, {At: t0}}, 0); got < minSampleDuration {
		t.Errorf("two deliveries in the same instant gave a duration of %v", got)
	}
}

// Without parameter sets the track cannot be described, and a clip that does
// not open is worse than a clip that is not there: the first is discovered when
// it is needed.
func TestAClipWithoutVideoIsRefused(t *testing.T) {
	if err := WriteClip(&bytes.Buffer{}, Snapshot{}); err != ErrNoVideo {
		t.Errorf("an empty snapshot gave %v instead of ErrNoVideo", err)
	}
	s := Snapshot{Video: []Frame{{Data: []byte{0, 0, 0, 1, 0x65}, At: t0}}}
	if err := WriteClip(&bytes.Buffer{}, s); err == nil {
		t.Error("a snapshot without an SPS and PPS produced a file")
	}
}
