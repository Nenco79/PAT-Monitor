package record

import (
	"encoding/hex"
	"testing"
	"time"

	"patmonitor/internal/media"
)

var t0 = time.Date(2026, 9, 1, 3, 0, 0, 0, time.UTC)

// The SPSs are not invented: they are the two this machine's Quick Sync encoder
// produced before and after a size change, the same ones as in
// media/h264_test.go. They have to be real because the muxer reads the size out
// of them, and an SPS written by hand would only prove we can read back what we
// wrote ourselves.
const (
	sps720p = "2742e01f95b014016ec044000003000400000300f3a100098900002625a6f7be0ed0e197"
	sps540p = "2742e01f95b03c045fbc0440000003004000000f3a100098900002625a6f7be0ed0e1970"
	ppsHex  = "28ee3cb0"
)

// sameSizeOtherBytes gives an SPS describing the same picture with different
// bytes, which is what a rebuild of the encoder at the same resolution
// produces: the timing in the VUI changes, that is, the declared cadence.
//
// The last byte is touched, which sits in the VUI's tail, and **the size read
// back is checked not to change**: if that byte ever became significant, the
// test would say so instead of testing something else.
func sameSizeOtherBytes(t *testing.T, spsHex string) []byte {
	t.Helper()
	sps := append([]byte(nil), mustHex(t, spsHex)...)
	sps[len(sps)-1] ^= 0x01
	w1, h1, err1 := media.SPSSize(mustHex(t, spsHex))
	w2, h2, err2 := media.SPSSize(sps)
	if err1 != nil || err2 != nil || w1 != w2 || h1 != h2 {
		t.Fatalf("the modified SPS no longer describes the same picture: %dx%d against %dx%d", w2, h2, w1, h1)
	}
	return sps
}

func mustHex(t *testing.T, s string) []byte {
	t.Helper()
	b, err := hex.DecodeString(s)
	if err != nil {
		t.Fatalf("unreadable test bytes: %v", err)
	}
	return b
}

// annexB glues the NALs together with start codes, the way the assembler hands
// them over.
func annexB(nals ...[]byte) []byte {
	var out []byte
	for _, n := range nals {
		out = append(out, 0, 0, 0, 1)
		out = append(out, n...)
	}
	return out
}

// keyframe builds an IDR complete with parameter sets, which is how a real
// encoder repeats them on every key.
func keyframe(t *testing.T, spsHex string, size int) media.AccessUnit {
	t.Helper()
	slice := append([]byte{0x65, 0x88}, make([]byte, size)...)
	return media.AccessUnit{
		Data:     annexB(mustHex(t, spsHex), mustHex(t, ppsHex), slice),
		Keyframe: true,
	}
}

// inter builds a predicted frame.
func inter(size int) media.AccessUnit {
	slice := append([]byte{0x41, 0x9a}, make([]byte, size)...)
	return media.AccessUnit{Data: annexB(slice)}
}

// feedGOP delivers a keyframe and the frames that follow it, one per step, and
// returns the instant after the last one.
func feedGOP(t *testing.T, r *Ring, spsHex string, at time.Time, frames int, step time.Duration) time.Time {
	t.Helper()
	r.WriteVideo(keyframe(t, spsHex, 400), at)
	for i := 1; i < frames; i++ {
		at = at.Add(step)
		r.WriteVideo(inter(100), at)
	}
	return at.Add(step)
}

// **The property everything rests on.** A piece beginning in the middle of a
// GOP does not decode: if the ring accepted the frames arriving before the
// first keyframe, the clip would start with a few seconds of broken picture and
// there would be nothing to trim to make up for it.
func TestTheRingAlwaysStartsAtAKeyframe(t *testing.T) {
	r := NewRing(nil)

	at := t0
	for range 10 {
		r.WriteVideo(inter(100), at)
		at = at.Add(100 * time.Millisecond)
	}
	if s := r.Snapshot(); len(s.Video) != 0 {
		t.Fatalf("before a keyframe the ring accepted %d frames: the clip would start in the middle of a GOP", len(s.Video))
	}
	if st := r.Stats(); st.Refused != 10 {
		t.Errorf("%d frames refused instead of 10: the counter is the only way to tell \"no keyframes yet\" from \"nothing is arriving\"", st.Refused)
	}

	feedGOP(t, r, sps720p, at, 20, 100*time.Millisecond)
	s := r.Snapshot()
	if len(s.Video) == 0 {
		t.Fatal("after a keyframe the ring is still empty")
	}
	if !s.Video[0].Keyframe {
		t.Error("the snapshot does not start with a keyframe")
	}
}

// **Two whole groups mean three entries in the queue, not two.** With the GOP
// at two seconds the pre-roll has to guarantee four: keeping only two entries
// would give one whole one plus one still growing, that is, two seconds
// guaranteed. It is the wrong reading of the four seconds of pre-roll, and this
// test is what tells it from the right one.
func TestTwoWholeGopsSurviveTheGrowingOne(t *testing.T) {
	r := NewRing(nil)

	const (
		step = 100 * time.Millisecond
		perG = 20 // twenty 100 ms frames: a two-second GOP
	)
	at := t0
	for range 3 {
		at = feedGOP(t, r, sps720p, at, perG, step)
	}
	// The keyframe opening the fourth group: from here on the oldest one goes,
	// and this is the instant the pre-roll is at its guaranteed minimum.
	r.WriteVideo(keyframe(t, sps720p, 400), at)

	s := r.Snapshot()
	if !s.Video[0].Keyframe {
		t.Fatal("the snapshot does not start with a keyframe")
	}
	if got, want := s.Span(), 4*time.Second; got < want {
		t.Errorf("the pre-roll covers %v instead of at least %v: with only two entries in the queue two seconds are left, which is half of what is needed", got, want)
	}
	if st := r.Stats(); st.Gops != wholeGops+1 {
		t.Errorf("%d groups queued instead of %d (two whole ones plus the one in progress)", st.Gops, wholeGops+1)
	}
}

// A rebuilt encoder emits a new SPS, and the earlier GOPs do not belong in the
// same file: an fMP4's header carries one SPS only. Keeping them would give a
// clip whose samples contradict the parameters the player decodes them with,
// which is the fault that protests nowhere.
func TestNewParameterSetsEmptyTheRing(t *testing.T) {
	r := NewRing(nil)

	const step = 100 * time.Millisecond
	at := feedGOP(t, r, sps720p, t0, 20, step)
	at = feedGOP(t, r, sps720p, at, 20, step)
	if s := r.Snapshot(); len(s.Video) != 40 {
		t.Fatalf("before the size change there are %d frames instead of 40", len(s.Video))
	}

	// Resolution scale: 1280x720 -> 960x540, encoder rebuilt.
	feedGOP(t, r, sps540p, at, 5, step)

	s := r.Snapshot()
	if len(s.Video) != 5 {
		t.Errorf("after the rebuild %d frames are left instead of the 5 of the new format", len(s.Video))
	}
	if want := mustHex(t, sps540p); string(s.SPS) != string(want) {
		t.Error("the snapshot still carries the old SPS: the clip would declare the wrong size")
	}
	if st := r.Stats(); st.Resets != 1 {
		t.Errorf("%d rebuilds counted instead of 1: it is the first thing to look at when a pre-roll comes out short", st.Resets)
	}
}

// **The case the ceiling exists for.** If the encoder stops producing keyframes
// the group in progress never closes: without a ceiling this ring is the one
// part of the monitor that can eat the machine, and it would do it at night,
// slowly, with nothing to say so.
func TestAnEncoderThatStopsSendingKeyframesCannotGrowForever(t *testing.T) {
	r := NewRing(nil)

	at := t0
	r.WriteVideo(keyframe(t, sps720p, 400), at)
	for i := range 4000 {
		at = at.Add(33 * time.Millisecond)
		r.WriteVideo(inter(8000), at)
		if b := r.Stats().Bytes; b > maxBytes {
			t.Fatalf("after %d frames without a keyframe the ring holds %d bytes, past the ceiling of %d", i, b, maxBytes)
		}
	}

	// And what is left is usable anyway: either it starts on a keyframe, or
	// there is nothing. Half a GOP is not a pre-roll.
	if s := r.Snapshot(); len(s.Video) > 0 && !s.Video[0].Keyframe {
		t.Error("the ceiling left a piece that starts in the middle of a GOP")
	}
}

// Audio older than the first frame would give a clip starting with sound over
// nothing; audio more recent than the last would stretch the audio track past
// the video one.
func TestAudioIsTrimmedToTheVideoSpan(t *testing.T) {
	r := NewRing(nil)

	// The video covers the two seconds from t0: twenty 100 ms frames.
	const step = 100 * time.Millisecond
	videoEnd := t0.Add(19 * step)
	feedGOP(t, r, sps720p, t0, 20, step)

	// The audio covers six of them, three seconds before and one after.
	for i := -150; i < 200; i++ {
		r.WriteAudio([]byte{0xfc, byte(i)}, t0.Add(time.Duration(i)*opusFrameDuration))
	}

	s := r.Snapshot()
	if len(s.Audio) == 0 {
		t.Fatal("no audio in the snapshot")
	}
	for _, p := range s.Audio {
		if p.At.Before(t0) {
			t.Fatalf("an audio packet at %v precedes the first frame: the clip would start with sound over nothing", p.At)
		}
		if p.At.After(videoEnd) {
			t.Fatalf("an audio packet at %v follows the last frame: the audio track would be longer than the video one", p.At)
		}
	}
	// And the test has to have something to trim: without this, a ring keeping
	// no audio at all would pass too.
	if want := int(videoEnd.Sub(t0)/opusFrameDuration) + 1; len(s.Audio) != want {
		t.Errorf("%d audio packets kept instead of the %d that fall inside the video's window", len(s.Audio), want)
	}
}

// Audio and video have separate life cycles on purpose: the camera can stop
// while the microphone keeps going, and then there is no video to prune the
// audio against. Without the age criterion that queue grows until morning.
func TestAudioAloneDoesNotGrowForever(t *testing.T) {
	r := NewRing(nil)

	at := t0
	for i := range 10000 { // two hundred seconds of voice alone
		r.WriteAudio([]byte{0xfc, byte(i)}, at)
		at = at.Add(opusFrameDuration)
	}
	st := r.Stats()
	if want := int(maxAudioAge/opusFrameDuration) + 1; st.AudioPackets > want {
		t.Errorf("%d audio packets queued, that is, more than the %v the age ceiling allows", st.AudioPackets, maxAudioAge)
	}
}

// **The invariant that lets the writing happen elsewhere.** The snapshot is
// taken on the goroutine that must not block and written on another: if the
// ring could change it under the feet of whoever is writing it, the clip would
// come out with a mixture of two instants inside, and it would be a race that
// shows up one night in a hundred.
func TestTheSnapshotSurvivesLaterWrites(t *testing.T) {
	r := NewRing(nil)

	const step = 100 * time.Millisecond
	at := feedGOP(t, r, sps720p, t0, 20, step)
	for i := range 50 {
		r.WriteAudio([]byte{0xfc, byte(i)}, t0.Add(time.Duration(i)*opusFrameDuration))
	}

	s := r.Snapshot()
	frames, packets := len(s.Video), len(s.Audio)
	first := s.Video[0].At
	var firstAudio time.Time
	if packets > 0 {
		firstAudio = s.Audio[0].At
	}

	// The ring carries on: more GOPs, more pruning, the oldest thrown away.
	for range 5 {
		at = feedGOP(t, r, sps720p, at, 20, step)
		for j := range 100 {
			r.WriteAudio([]byte{0xfd, byte(j)}, at.Add(time.Duration(j)*opusFrameDuration))
		}
	}

	if len(s.Video) != frames || !s.Video[0].At.Equal(first) {
		t.Errorf("the snapshot changed: %d frames from %v instead of %d from %v", len(s.Video), s.Video[0].At, frames, first)
	}
	if len(s.Audio) != packets {
		t.Errorf("the snapshot carries %d audio packets instead of %d", len(s.Audio), packets)
	}
	if packets > 0 && !s.Audio[0].At.Equal(firstAudio) {
		t.Errorf("the snapshot's first audio packet is at %v instead of %v: the array was reused underneath", s.Audio[0].At, firstAudio)
	}
}

// **A rebuild of the encoder at the same size must not empty the ring**, and
// comparing the bytes is what makes it.
//
// On a machine where the cadence chases the light the rebuilds are continuous —
// four in twenty-six seconds is on record — so the pre-roll would be reset over
// and over. The symptom reported by whoever uses it is exactly that: short
// clips that start at the event, with not an instant of what came before.
func TestARebuildAtTheSameSizeKeepsThePreroll(t *testing.T) {
	r := NewRing(nil)

	const step = 100 * time.Millisecond
	at := feedGOP(t, r, sps720p, t0, 20, step)
	at = feedGOP(t, r, sps720p, at, 20, step)
	before := r.Snapshot()
	if len(before.Video) != 40 {
		t.Fatalf("before the rebuild there are %d frames", len(before.Video))
	}

	// The encoder is rebuilt: same picture, different SPS.
	other := sameSizeOtherBytes(t, sps720p)
	r.WriteVideo(media.AccessUnit{
		Data:     annexB(other, mustHex(t, ppsHex), append([]byte{0x65, 0x88}, make([]byte, 400)...)),
		Keyframe: true,
	}, at)

	after := r.Snapshot()
	if len(after.Video) < 40 {
		t.Errorf("after the rebuild %d frames are left instead of at least 40: the pre-roll was thrown away", len(after.Video))
	}
	if st := r.Stats(); st.Resets != 0 {
		t.Errorf("%d resets counted for a rebuild at the same size", st.Resets)
	}
	// And the header stays the first one: it is the one everything the ring
	// already had was coded with.
	if string(after.SPS) != string(mustHex(t, sps720p)) {
		t.Error("the snapshot carries the new SPS: the old frames would contradict it")
	}
}

// A change of size, on the other hand, does empty it, and it has to: the old
// samples do not decode with the new header.
func TestAChangeOfSizeStillEmptiesTheRing(t *testing.T) {
	r := NewRing(nil)

	const step = 100 * time.Millisecond
	at := feedGOP(t, r, sps720p, t0, 20, step)
	at = feedGOP(t, r, sps720p, at, 20, step)
	feedGOP(t, r, sps540p, at, 5, step)

	if s := r.Snapshot(); len(s.Video) != 5 {
		t.Errorf("after the size change %d frames are left instead of 5", len(s.Video))
	}
	if st := r.Stats(); st.Resets != 1 {
		t.Errorf("%d resets counted instead of 1", st.Resets)
	}
}

// And a different PPS empties it even at the same size: it carries
// pic_init_qp_minus26, and the old frames read with the new one would give the
// wrong quantiser — a corrupt picture with no error anywhere.
func TestADifferentPPSEmptiesTheRing(t *testing.T) {
	r := NewRing(nil)

	const step = 100 * time.Millisecond
	at := feedGOP(t, r, sps720p, t0, 20, step)
	otherPPS := append([]byte(nil), mustHex(t, ppsHex)...)
	otherPPS[len(otherPPS)-1] ^= 0x01
	r.WriteVideo(media.AccessUnit{
		Data:     annexB(mustHex(t, sps720p), otherPPS, append([]byte{0x65, 0x88}, make([]byte, 400)...)),
		Keyframe: true,
	}, at)

	if s := r.Snapshot(); len(s.Video) != 1 {
		t.Errorf("with a different PPS %d frames are left instead of 1", len(s.Video))
	}
}
