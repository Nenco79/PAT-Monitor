package media

import (
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"time"

	"github.com/bluenviron/mediacommon/v2/pkg/formats/mp4/codecs"
	"github.com/bluenviron/mediacommon/v2/pkg/formats/pmp4"
)

// MP4Clip collects samples and writes a **progressive** MP4, that is, one with
// the sample table all in the moov instead of scattered through the fragments.
//
// **It is not FMP4Muxer's twin, it is the other job.** The fragmented one is
// for where the file is not finished while it is being sent — streaming — and
// the price is that without an index (sidx or mfra) whoever opens it does not
// know how long it lasts or where to seek: measured on our first download,
// Explorer declared an empty duration, and players that insist on the index
// treat it as non-seekable. A clip on disk, on the other hand, is complete the
// instant it is written, so the index can really be written — same samples, a
// thousand bytes more, and the duration appears.
//
// The API is FMP4Muxer's on purpose, so moving from one to the other costs
// nothing. And like that one, it knows neither the network nor files, and it is
// not safe for concurrent use.
type MP4Clip struct {
	sps, pps      []byte
	audioChannels int
	audioDelay    time.Duration

	video, audio []*pmp4.Sample
}

// NewMP4Clip prepares a clip for the tracks described.
//
// audioChannels is the channel count declared for Opus: the SDP's warning
// applies, the stream is mono but players expect the declaration to say two.
func NewMP4Clip(sps, pps []byte, audioChannels int) (*MP4Clip, error) {
	sps, pps, audioChannels, err := trackSetup(sps, pps, audioChannels)
	if err != nil {
		return nil, err
	}
	return &MP4Clip{sps: sps, pps: pps, audioChannels: audioChannels}, nil
}

// AddVideo appends an Annex-B access unit with the declared duration.
func (c *MP4Clip) AddVideo(au []byte, dur time.Duration) error {
	avcc, key, err := avccSample(au)
	if err != nil {
		return err
	}
	if avcc == nil {
		return nil
	}
	c.video = append(c.video, &pmp4.Sample{
		Duration:        uint32(durToScale(dur, videoTimeScale)),
		IsNonSyncSample: !key,
		PayloadSize:     uint32(len(avcc)),
		// The payload is handed over when it is needed: pmp4 writes the index
		// first and the data after, so this way it does not keep a second copy
		// in memory while it does.
		GetPayload: func() ([]byte, error) { return avcc, nil },
	})
	return nil
}

// SetAudioDelay declares how much later the audio track starts than the video
// one.
//
// It is needed because the two captures have separate lives on purpose: the
// microphone may open after the camera, or reopen halfway through, and then the
// first audio packet is more recent than the first frame. Without declaring it
// the audio would be laid at the start of the clip and everything would be
// heard that interval early.
//
// It is declared rather than padded with silence: **only what there is gets
// written**. Underneath it becomes an edit list, which is the way the format
// provides for saying "this track starts later".
func (c *MP4Clip) SetAudioDelay(d time.Duration) {
	if d > 0 {
		c.audioDelay = d
	}
}

// AddAudio appends an Opus packet with the declared duration.
func (c *MP4Clip) AddAudio(pkt []byte, dur time.Duration) {
	if len(pkt) == 0 {
		return
	}
	p := append([]byte(nil), pkt...)
	c.audio = append(c.audio, &pmp4.Sample{
		Duration:    uint32(durToScale(dur, audioTimeScale)),
		PayloadSize: uint32(len(p)),
		GetPayload:  func() ([]byte, error) { return p, nil },
	})
}

// Marshal writes the clip. Empty tracks are not declared: an audio track with
// no samples makes some players say the file has silent audio, rather than no
// audio.
func (c *MP4Clip) Marshal(w io.Writer) error {
	if len(c.video) == 0 && len(c.audio) == 0 {
		return ErrNoSamples
	}
	var pres pmp4.Presentation
	if len(c.video) > 0 {
		pres.Tracks = append(pres.Tracks, &pmp4.Track{
			ID:        videoTrackID,
			TimeScale: videoTimeScale,
			Codec:     &codecs.H264{SPS: c.sps, PPS: c.pps},
			Samples:   c.video,
		})
	}
	if len(c.audio) > 0 {
		pres.Tracks = append(pres.Tracks, &pmp4.Track{
			ID:         audioTrackID,
			TimeScale:  audioTimeScale,
			TimeOffset: int32(durToScale(c.audioDelay, audioTimeScale)),
			Codec:      &codecs.Opus{ChannelCount: c.audioChannels},
			Samples:    c.audio,
		})
	}
	if err := pres.Marshal(w); err != nil {
		return fmt.Errorf("media: writing the clip: %w", err)
	}
	return nil
}

// ErrNoMovieHeader says the file does not carry the header with the duration.
var ErrNoMovieHeader = errors.New("media: mvhd not found")

// MP4Duration reads how long an MP4 lasts by walking its boxes as far as the
// mvhd.
//
// **It decodes nothing and reads no samples**: in a file written by MP4Clip the
// moov sits at offset 32, so the header arrives within the first sixteen
// kilobytes, and listing a folder of clips costs one short read per file. It is
// for whoever shows a list: the duration is not in the name, and writing it
// there would mean keeping two copies of the same thing aligned.
//
// The reader is derived from the specification rather than from the code that
// writes: recounting the fields with whoever produced them only proves that
// code is equal to itself.
func MP4Duration(r io.ReadSeeker) (time.Duration, error) {
	end, err := r.Seek(0, io.SeekEnd)
	if err != nil {
		return 0, err
	}
	moov, moovEnd, err := findBox(r, 0, end, "moov")
	if err != nil {
		return 0, err
	}
	mvhd, _, err := findBox(r, moov, moovEnd, "mvhd")
	if err != nil {
		return 0, ErrNoMovieHeader
	}

	// version(1) + flags(3), then creation and modification, then scale and
	// duration. The two dates are 32-bit in version 0 and 64-bit in version 1,
	// and everything after shifts accordingly: it is the same kind of field
	// that in the H.264 slice header yields a plausible and wrong number when
	// it is miscounted.
	head := make([]byte, 32)
	if _, err := readAt(r, mvhd, head); err != nil {
		return 0, err
	}
	var scale, dur uint64
	switch head[0] {
	case 0:
		scale = uint64(binary.BigEndian.Uint32(head[12:]))
		dur = uint64(binary.BigEndian.Uint32(head[16:]))
	case 1:
		scale = uint64(binary.BigEndian.Uint32(head[20:]))
		dur = binary.BigEndian.Uint64(head[24:])
	default:
		return 0, fmt.Errorf("media: mvhd version %d unknown", head[0])
	}
	if scale == 0 {
		return 0, errors.New("media: mvhd declares a zero timescale")
	}
	return time.Duration(dur) * time.Second / time.Duration(scale), nil
}

// MP4HasAudio says whether the file carries a sound track.
//
// **It is read from the file, which is the only place that knows.** A clip
// recorded while the microphone was absent is video only — clip.go adds the
// audio only if there is any — and from outside that is indistinguishable from
// a player that cannot decode the audio. The recordings page has to be able to
// tell them apart: without that, it blames the viewer's browser for a track
// that was never written.
//
// The road is the specification's: inside moov the traks are walked, and a
// track is a sound one if its hdlr declares soun. The type sits eight bytes
// from the start of the content — version and flags take four, pre_defined
// another four — and it is the same kind of field that in the H.264 slice
// header yields a plausible and wrong number when it is miscounted.
func MP4HasAudio(r io.ReadSeeker) (bool, error) {
	end, err := r.Seek(0, io.SeekEnd)
	if err != nil {
		return false, err
	}
	moov, moovEnd, err := findBox(r, 0, end, "moov")
	if err != nil {
		return false, err
	}
	// There is more than one track and findBox returns the first: the search
	// restarts from the end of the one just examined.
	for off := moov; off < moovEnd; {
		trak, trakEnd, err := findBox(r, off, moovEnd, "trak")
		if err != nil {
			// There are no more. A file without a trak is not a fault here: the
			// question was whether there was a sound one, and the answer is no.
			return false, nil
		}
		mdia, mdiaEnd, err := findBox(r, trak, trakEnd, "mdia")
		if err == nil {
			if hdlr, _, err := findBox(r, mdia, mdiaEnd, "hdlr"); err == nil {
				var b [12]byte
				if _, err := readAt(r, hdlr, b[:]); err == nil && string(b[8:12]) == "soun" {
					return true, nil
				}
			}
		}
		off = trakEnd
	}
	return false, nil
}

// findBox looks for a box between two offsets and returns the start of its
// content and its end.
func findBox(r io.ReadSeeker, from, to int64, want string) (payload, end int64, err error) {
	head := make([]byte, 16)
	for off := from; off+8 <= to; {
		if _, err := readAt(r, off, head[:8]); err != nil {
			return 0, 0, err
		}
		size, typ := int64(binary.BigEndian.Uint32(head[:4])), string(head[4:8])
		hdr := int64(8)
		switch size {
		case 0:
			// Zero means "to the end of the file", and it happens on the last
			// box.
			size = to - off
		case 1:
			// One means the real length sits in the eight bytes that follow.
			if _, err := readAt(r, off+8, head[8:16]); err != nil {
				return 0, 0, err
			}
			size, hdr = int64(binary.BigEndian.Uint64(head[8:16])), 16
		}
		if size < hdr || off+size > to {
			return 0, 0, fmt.Errorf("media: malformed box %q at %d: it declares %d bytes", typ, off, size)
		}
		if typ == want {
			return off + hdr, off + size, nil
		}
		off += size
	}
	return 0, 0, fmt.Errorf("media: box %q not found", want)
}

func readAt(r io.ReadSeeker, off int64, b []byte) (int, error) {
	if _, err := r.Seek(off, io.SeekStart); err != nil {
		return 0, err
	}
	return io.ReadFull(r, b)
}
