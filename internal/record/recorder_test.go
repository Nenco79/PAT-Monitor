package record

import (
	"bytes"
	"testing"
	"time"

	"patmonitor/internal/media"
)

const step = 100 * time.Millisecond

// feedRec delivers a GOP of video and the audio that goes with it to the
// recorder, as the capture's two sinks would, and returns the instant after the
// last frame.
func feedRec(t *testing.T, r *Recorder, spsHex string, at time.Time, frames int) time.Time {
	t.Helper()
	for i := range frames {
		if i == 0 {
			r.WriteVideo(keyframe(t, spsHex, 400), at)
		} else {
			r.WriteVideo(inter(100), at)
		}
		// Five 20 ms audio packets for every 100 ms frame.
		for j := range 5 {
			r.WriteAudio([]byte{0xfc, byte(j)}, at.Add(time.Duration(j)*opusFrameDuration))
		}
		at = at.Add(step)
	}
	return at
}

// takeClip takes the clip if there is one, and says what is missing if there is
// not.
func takeClip(t *testing.T, r *Recorder) Clip {
	t.Helper()
	select {
	case c := <-r.Clips():
		return c
	default:
		t.Fatal("no clip delivered")
		return Clip{}
	}
}

// **The reason all of this exists.** A clip starting when the alert arrives
// shows the consequences and not the cause; one ending there shows the cause
// and not the consequences. Both have to be there, and the event's instant has
// to fall in the middle.
func TestAClipCarriesTheSecondsBeforeAndAfter(t *testing.T) {
	r := NewRecorder(RecorderConfig{PostRoll: 2 * time.Second})

	at := t0
	for range 3 {
		at = feedRec(t, r, sps720p, at, 20)
	}
	event := at
	r.Trigger("motion", event)

	// Three seconds afterwards: the finish line is at two.
	for range 30 {
		at = feedRec(t, r, sps720p, at, 1)
	}

	c := takeClip(t, r)
	if c.Code != "motion" {
		t.Errorf("code %q instead of motion", c.Code)
	}
	if !c.At.Equal(event) {
		t.Errorf("event instant %v instead of %v", c.At, event)
	}
	if !c.Video[0].Keyframe {
		t.Error("the clip does not start with a keyframe")
	}
	if before := event.Sub(c.Video[0].At); before < 4*time.Second {
		t.Errorf("there is %v before the event instead of the four seconds the ring guarantees", before)
	}
	after := c.Video[len(c.Video)-1].At.Sub(event)
	if after < 2*time.Second {
		t.Errorf("there is %v after the event instead of the two asked for", after)
	}
	if after > 2*time.Second+step {
		t.Errorf("there is %v after the event: the clip did not close at the finish line", after)
	}
	if len(c.Audio) == 0 {
		t.Error("the clip carries no audio")
	}
}

// An episode that continues is one episode, not two: a second event moves the
// finish line instead of opening an overlapping clip, which would give two
// files holding the same seconds.
func TestASecondEventExtendsTheClipInsteadOfStartingAnother(t *testing.T) {
	r := NewRecorder(RecorderConfig{PostRoll: 2 * time.Second})

	at := feedRec(t, r, sps720p, t0, 20)
	at = feedRec(t, r, sps720p, at, 20)
	event := at
	r.Trigger("motion", event)

	// A second later the cry arrives: the finish line moves to event+3s.
	for range 10 {
		at = feedRec(t, r, sps720p, at, 1)
	}
	r.Trigger("cry", at)
	second := at

	for range 25 {
		at = feedRec(t, r, sps720p, at, 1)
	}

	c := takeClip(t, r)
	if c.Code != "motion" {
		t.Errorf("the clip carries the code %q: the name has to stay that of the event that opened it", c.Code)
	}
	after := c.Video[len(c.Video)-1].At.Sub(second)
	if after < 2*time.Second {
		t.Errorf("there is %v after the second event instead of two seconds: the finish line did not move", after)
	}
	// And one clip, not two.
	select {
	case other := <-r.Clips():
		t.Errorf("a second clip was delivered (%s): overlapping events have to live in the same file", other.Code)
	default:
	}
}

// A rebuilt encoder emits new parameter sets, and an MP4 carries one pair:
// better a clip that ends early than one that does not decode halfway.
func TestARebuiltEncoderCutsTheClipShort(t *testing.T) {
	r := NewRecorder(RecorderConfig{PostRoll: 5 * time.Second})

	at := feedRec(t, r, sps720p, t0, 20)
	at = feedRec(t, r, sps720p, at, 20)
	event := at
	r.Trigger("motion", event)

	at = feedRec(t, r, sps720p, at, 10)
	// Resolution scale: 1280x720 -> 960x540.
	feedRec(t, r, sps540p, at, 5)

	c := takeClip(t, r)
	if after := c.Video[len(c.Video)-1].At.Sub(event); after >= 5*time.Second {
		t.Errorf("the clip covers %v after the event: it should have closed at the format change", after)
	}
	// A keyframe of the same format is an ordinary GOP boundary and belongs;
	// the fault would be a sample carrying parameter sets other than the
	// header's, that is, a piece that player cannot decode.
	for i, f := range c.Video {
		if !f.Keyframe {
			continue
		}
		if sps, _ := parameterSets(f.Data); !bytes.Equal(sps, c.SPS) {
			t.Fatalf("frame %d carries an SPS other than the header's", i)
		}
	}
	if st := r.Stats(); st.Cut != 1 {
		t.Errorf("%d cuts counted instead of 1", st.Cut)
	}
}

// **The finish line is checked by the frames arriving**, so if the camera stops
// the clip would stay open in memory forever and never be written. The tick
// closes it.
func TestAClipClosesEvenIfNothingElseArrives(t *testing.T) {
	r := NewRecorder(RecorderConfig{PostRoll: 2 * time.Second})

	at := feedRec(t, r, sps720p, t0, 20)
	at = feedRec(t, r, sps720p, at, 20)
	r.Trigger("motion", at)

	// No more frames, no more audio: only time passing.
	r.Tick(at.Add(time.Second))
	if !r.Recording() {
		t.Fatal("the clip closed before the finish line")
	}
	r.Tick(at.Add(3 * time.Second))
	if r.Recording() {
		t.Error("the clip is still open past the finish line")
	}
	if c := takeClip(t, r); len(c.Video) == 0 {
		t.Error("the clip delivered is empty")
	}
}

// At start-up the ring is empty for a few seconds. An event in there does not
// produce a zero-byte file: it produces nothing, and says so.
func TestNoClipBeforeTheFirstKeyframe(t *testing.T) {
	r := NewRecorder(RecorderConfig{})
	r.WriteVideo(media.AccessUnit{Data: inter(100).Data}, t0)
	r.Trigger("motion", t0)

	if r.Recording() {
		t.Error("recording opened with an empty ring")
	}
	select {
	case c := <-r.Clips():
		t.Errorf("a clip with %d frames was delivered", len(c.Video))
	default:
	}
}

// A finish line that moves on every event is also a way never to finish: an
// afternoon of continuous movement would give one file, as big as the
// afternoon.
func TestTheLengthCeilingClosesTheClip(t *testing.T) {
	r := NewRecorder(RecorderConfig{PostRoll: 2 * time.Second})

	at := feedRec(t, r, sps720p, t0, 20)
	at = feedRec(t, r, sps720p, at, 20)
	event := at
	r.Trigger("motion", event)

	// Somebody moving without a break: one event a second for two minutes.
	for i := 0; i < 120 && r.Recording(); i++ {
		at = feedRec(t, r, sps720p, at, 10)
		r.Trigger("motion", at)
	}

	c := takeClip(t, r)
	if length := c.Video[len(c.Video)-1].At.Sub(event); length > maxClipDuration+step {
		t.Errorf("the clip covers %v after the event, past the ceiling of %v", length, maxClipDuration)
	}
	if st := r.Stats(); st.Cut == 0 {
		t.Error("the cut at the ceiling was not counted")
	}
}

// **The same rule holds for the clip in progress**, and without it the
// recording lasts as long as the interval between two rebuilds of the encoder.
//
// It is the defect reported by whoever uses it: five-second clips. On a machine
// where the cadence chases the light the encoder rebuilds continuously — four
// times in twenty-six seconds, measured — and every rebuild truncated the clip.
func TestARebuildAtTheSameSizeDoesNotCutTheClip(t *testing.T) {
	r := NewRecorder(RecorderConfig{PostRoll: 4 * time.Second})

	at := feedRec(t, r, sps720p, t0, 20)
	at = feedRec(t, r, sps720p, at, 20)
	event := at
	r.Trigger("motion", event)

	// After a second the encoder rebuilds at the same size, and then it carries
	// on to the finish line.
	at = feedRec(t, r, sps720p, at, 10)
	other := sameSizeOtherBytes(t, sps720p)
	r.WriteVideo(media.AccessUnit{
		Data:     annexB(other, mustHex(t, ppsHex), append([]byte{0x65, 0x88}, make([]byte, 400)...)),
		Keyframe: true,
	}, at)
	at = at.Add(step)
	for range 40 {
		at = feedRec(t, r, sps720p, at, 1)
	}

	c := takeClip(t, r)
	if after := c.Video[len(c.Video)-1].At.Sub(event); after < 4*time.Second {
		t.Errorf("the clip covers %v after the event instead of the four asked for: the rebuild truncated it", after)
	}
	if before := event.Sub(c.Video[0].At); before < 4*time.Second {
		t.Errorf("there is %v before the event: the pre-roll was thrown away", before)
	}
	if st := r.Stats(); st.Cut != 0 {
		t.Errorf("%d cuts counted for a rebuild at the same size", st.Cut)
	}
}

// **A clip asked for by hand carries its lock all the way to the file.**
//
// The field is read by Save, which decides the prefix: if it were lost on the
// way, the clip somebody expressly asked for would be the first to expire, with
// nothing to say so.
func TestAClipAskedForByHandArrivesWithItsLock(t *testing.T) {
	r := NewRecorder(RecorderConfig{PostRoll: time.Second})

	at := feedRec(t, r, sps720p, t0, 20)
	if !r.TriggerKept(CodeManual, at) {
		t.Fatal("with the ring full the request was not accepted")
	}
	feedRec(t, r, sps720p, at, 15)

	c := takeClip(t, r)
	if !c.Keep {
		t.Error("the clip arrived without its lock: the retention would delete it")
	}
	if c.Code != CodeManual {
		t.Errorf("code %q instead of %q", c.Code, CodeManual)
	}
	// And the recorder goes back to rest without the lock in hand: if it stayed,
	// the first event clip after a manual request would be born held.
	at = feedRec(t, r, sps720p, at, 20)
	r.Trigger("motion", at)
	feedRec(t, r, sps720p, at, 15)
	if c := takeClip(t, r); c.Keep {
		t.Error("an event clip inherited the previous one's lock")
	}
}

// **Pressing "Record" during an event does not open a second clip: it gives
// that one the lock.**
//
// The code stays that of the event — what happened in the room is that — but
// the request to keep it is not lost: whoever pressed wanted **that** clip,
// which is also the likeliest one to want to keep.
func TestPressingRecordDuringAnEventKeepsThatClip(t *testing.T) {
	r := NewRecorder(RecorderConfig{PostRoll: time.Second})

	at := feedRec(t, r, sps720p, t0, 20)
	r.Trigger("motion", at)
	at = feedRec(t, r, sps720p, at, 3)
	if !r.TriggerKept(CodeManual, at) {
		t.Fatal("the manual request was not accepted on a clip in progress")
	}
	feedRec(t, r, sps720p, at, 15)

	c := takeClip(t, r)
	if !c.Keep {
		t.Error("the clip in progress did not take the lock")
	}
	if c.Code != "motion" {
		t.Errorf("code %q: the event is what happened in the room", c.Code)
	}

	// **And it does not hold the other way round**: an event arriving on a clip
	// asked for by hand does not take its lock away. Between whoever asks for
	// it and whoever does not mention it, whoever asks wins.
	at = feedRec(t, r, sps720p, at, 20)
	r.TriggerKept(CodeManual, at)
	at = feedRec(t, r, sps720p, at, 3)
	r.Trigger("cry", at)
	feedRec(t, r, sps720p, at, 15)
	if c := takeClip(t, r); !c.Keep {
		t.Error("an event that came along took the lock off a clip asked for by hand")
	}
}

// **With an empty ring the request says no, and that no is for whoever
// pressed.**
//
// It lasts a few seconds after start-up and after every restart of the capture.
// A command that does nothing and does not say so is a knob that moves nothing:
// whoever turns it concludes the program is not responding and goes looking for
// the fault where it is not. Events ignore the value — nobody is waiting for an
// answer — but the button does not.
func TestAnEmptyRingRefusesTheRequestInsteadOfSayingNothing(t *testing.T) {
	r := NewRecorder(RecorderConfig{})
	r.WriteVideo(media.AccessUnit{Data: inter(100).Data}, t0)

	if r.TriggerKept(CodeManual, t0) {
		t.Error("request accepted with an empty ring: there is no clip to save")
	}
	if r.Trigger("motion", t0) {
		t.Error("as above, by the events road")
	}

	// And with the ring full the answer changes, otherwise a recorder that
	// always says no would pass too.
	feedRec(t, r, sps720p, t0, 20)
	if !r.TriggerKept(CodeManual, t0.Add(2*time.Second)) {
		t.Error("request refused with the ring full")
	}
}
