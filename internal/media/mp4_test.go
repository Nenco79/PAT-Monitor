package media

import (
	"bytes"
	"encoding/binary"
	"errors"
	"testing"
	"time"
)

// box builds an MP4 box: four bytes of length, four of type, and the content.
func box(typ string, payload ...[]byte) []byte {
	var body []byte
	for _, p := range payload {
		body = append(body, p...)
	}
	out := make([]byte, 8, 8+len(body))
	binary.BigEndian.PutUint32(out, uint32(8+len(body)))
	copy(out[4:], typ)
	return append(out, body...)
}

// bigBox builds a box with a 64-bit length, that is, the one declared by
// putting 1 in the 32-bit field.
func bigBox(typ string, size int) []byte {
	out := make([]byte, 16)
	binary.BigEndian.PutUint32(out, 1)
	copy(out[4:], typ)
	binary.BigEndian.PutUint64(out[8:], uint64(size))
	return append(out, make([]byte, size-16)...)
}

func mvhdV0(scale, dur uint32) []byte {
	b := make([]byte, 100)
	b[0] = 0
	binary.BigEndian.PutUint32(b[12:], scale)
	binary.BigEndian.PutUint32(b[16:], dur)
	return box("mvhd", b)
}

func mvhdV1(scale uint32, dur uint64) []byte {
	b := make([]byte, 112)
	b[0] = 1
	binary.BigEndian.PutUint32(b[20:], scale)
	binary.BigEndian.PutUint64(b[24:], dur)
	return box("mvhd", b)
}

// **The two versions of the mvhd put the scale in two different places**,
// because the dates in front of it are 32 or 64 bits. Our clips use version 0,
// so nobody exercises version 1: if a file from elsewhere ever arrived and the
// field were read at the wrong offset, out would come a duration that is
// **plausible and false**, which is the fault this project fears more than an
// error.
func TestMP4DurationReadsBothHeaderVersions(t *testing.T) {
	cases := []struct {
		name string
		file []byte
		want time.Duration
	}{
		{
			name: "version 0",
			file: append(box("ftyp", []byte("mp42")), box("moov", mvhdV0(1000, 15040))...),
			want: 15040 * time.Millisecond,
		},
		{
			name: "version 1",
			file: append(box("ftyp", []byte("mp42")), box("moov", mvhdV1(90000, 1353600))...),
			want: 15040 * time.Millisecond,
		},
		{
			// A big box in front of the moov: if the 64-bit length were not
			// read, the jump would land in the middle of its bytes and the moov
			// would not be found at all.
			name: "after a box with a 64-bit length",
			file: append(bigBox("free", 4096), box("moov", mvhdV0(90000, 450000))...),
			want: 5 * time.Second,
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, err := MP4Duration(bytes.NewReader(c.file))
			if err != nil {
				t.Fatalf("MP4Duration: %v", err)
			}
			if got != c.want {
				t.Errorf("duration read %v instead of %v", got, c.want)
			}
		})
	}
}

// A file that does not declare its duration is refused, not estimated: a list
// showing "0 s" next to a fifteen-second clip sends people looking for a fault
// in the recording, where there is none.
func TestMP4DurationRefusesWhatItCannotRead(t *testing.T) {
	cases := []struct {
		name string
		file []byte
	}{
		{"no moov", box("ftyp", []byte("mp42"))},
		{"moov without mvhd", append(box("ftyp", []byte("mp42")), box("moov", box("trak"))...)},
		{"truncated", box("ftyp", []byte("mp42"))[:6]},
		{"impossible length", []byte{0, 0, 0, 3, 'm', 'o', 'o', 'v'}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if d, err := MP4Duration(bytes.NewReader(c.file)); err == nil {
				t.Errorf("it answered %v instead of an error", d)
			}
		})
	}
	// And the "there is a moov but no header" case is told apart from the
	// others, because whoever receives it can say something more precise.
	noMvhd := append(box("ftyp", []byte("mp42")), box("moov", box("trak"))...)
	if _, err := MP4Duration(bytes.NewReader(noMvhd)); !errors.Is(err, ErrNoMovieHeader) {
		t.Errorf("error %v instead of ErrNoMovieHeader", err)
	}
}

// A zero scale would give a division by zero, and a file from elsewhere can
// carry one.
func TestMP4DurationRefusesAZeroTimescale(t *testing.T) {
	file := append(box("ftyp", []byte("mp42")), box("moov", mvhdV0(0, 15040))...)
	if _, err := MP4Duration(bytes.NewReader(file)); err == nil {
		t.Error("a zero scale did not produce an error")
	}
}

// The two writers have two jobs and one constraint in common, and that
// constraint is not ours: RFC 7587 wants Opus declared with two channels even
// when the stream is mono. Written in two constructors it could change in one
// only, and the symptom would be a clip on disk declaring different channels
// from the stream that produced it — plausible on both sides, wrong on one.
func TestBothWritersDeclareTheSameOpusChannels(t *testing.T) {
	sps, pps := []byte{0x67, 0x42, 0xE0, 0x1F}, []byte{0x68, 0xCE}

	for _, asked := range []int{0, -1, 1, 2} {
		clip, err := NewMP4Clip(sps, pps, asked)
		if err != nil {
			t.Fatalf("NewMP4Clip(%d): %v", asked, err)
		}
		mux, err := NewFMP4Muxer(sps, pps, asked)
		if err != nil {
			t.Fatalf("NewFMP4Muxer(%d): %v", asked, err)
		}
		if clip.audioChannels != mux.audioChannels {
			t.Errorf("with %d asked for: the clip declares %d channels, the muxer %d",
				asked, clip.audioChannels, mux.audioChannels)
		}
		if asked <= 0 && clip.audioChannels != 2 {
			t.Errorf("with no channels asked for it declares %d instead of 2", clip.audioChannels)
		}
	}
}

// The defensive copy holds for both: whoever passes the parameter sets may
// reuse those slices, and a writer keeping them by reference would write bytes
// into the header that changed afterwards.
func TestNeitherWriterKeepsTheCallersSlices(t *testing.T) {
	sps, pps := []byte{0x67, 0x42, 0xE0, 0x1F}, []byte{0x68, 0xCE}
	clip, err := NewMP4Clip(sps, pps, 2)
	if err != nil {
		t.Fatalf("NewMP4Clip: %v", err)
	}
	mux, err := NewFMP4Muxer(sps, pps, 2)
	if err != nil {
		t.Fatalf("NewFMP4Muxer: %v", err)
	}

	sps[1], pps[1] = 0xFF, 0xFF // the caller reuses their buffer

	if clip.sps[1] == 0xFF || clip.pps[1] == 0xFF {
		t.Error("the clip kept the caller's slices")
	}
	if mux.sps[1] == 0xFF || mux.pps[1] == 0xFF {
		t.Error("the muxer kept the caller's slices")
	}
}

// hdlr builds a handler box declaring the given type.
//
// The type sits eight bytes from the start of the content: version and flags
// take four, pre_defined another four.
func hdlr(kind string) []byte {
	b := make([]byte, 24)
	copy(b[8:], kind)
	return box("hdlr", b)
}

// trak builds a track with its handler.
func trak(kind string) []byte {
	return box("trak", box("mdia", hdlr(kind)))
}

// **A clip with no sound track and a player that cannot decode audio look alike
// from outside, and the page has to be able to tell them apart.**
//
// record/clip.go adds the audio only if there is any: a clip recorded while the
// microphone was absent is video only. Without this question, the recordings
// page blamed the viewer's browser for a track that was never written — and on
// iOS, where the suspicion is born switched on, that happened on the first
// silent clip.
func TestMP4HasAudioFindsTheSoundTrack(t *testing.T) {
	cases := []struct {
		name string
		file []byte
		want bool
	}{
		{
			name: "video and audio",
			file: append(box("ftyp", []byte("mp42")),
				box("moov", mvhdV0(1000, 15040), trak("vide"), trak("soun"))...),
			want: true,
		},
		{
			// The absent microphone case: there is no sound track.
			name: "video only",
			file: append(box("ftyp", []byte("mp42")),
				box("moov", mvhdV0(1000, 15040), trak("vide"))...),
			want: false,
		},
		{
			// **The sound track is not the first one**, and whoever stops at
			// the first answers no on every clip we have ever written: the
			// muxer puts the video in front.
			name: "sound track in second place",
			file: append(box("ftyp", []byte("mp42")),
				box("moov", mvhdV0(1000, 15040), trak("vide"), trak("hint"), trak("soun"))...),
			want: true,
		},
		{
			name: "no tracks at all",
			file: append(box("ftyp", []byte("mp42")), box("moov", mvhdV0(1000, 15040))...),
			want: false,
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			has, err := MP4HasAudio(bytes.NewReader(c.file))
			if err != nil {
				t.Fatalf("error: %v", err)
			}
			if has != c.want {
				t.Errorf("MP4HasAudio = %v, wanted %v", has, c.want)
			}
		})
	}
}

// Without a moov there is no answering, and none is given: a file truncated by
// a power cut is not a clip without audio.
func TestMP4HasAudioRefusesWhatItCannotRead(t *testing.T) {
	if _, err := MP4HasAudio(bytes.NewReader(box("ftyp", []byte("mp42")))); err == nil {
		t.Error("with no moov it answered instead of saying it does not know")
	}
}
