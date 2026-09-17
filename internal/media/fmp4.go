package media

import (
	"errors"
	"fmt"
	"time"

	"github.com/bluenviron/mediacommon/v2/pkg/codecs/h264"
	"github.com/bluenviron/mediacommon/v2/pkg/formats/fmp4"
	"github.com/bluenviron/mediacommon/v2/pkg/formats/fmp4/seekablebuffer"
	"github.com/bluenviron/mediacommon/v2/pkg/formats/mp4/codecs"
)

// The two tracks' time scales.
//
// 90 kHz is the conventional base for video in MPEG and it represents the usual
// cadences exactly. The audio uses its own sample rate, so the duration of a
// 20 ms Opus packet is a whole number of samples and accumulates no rounding
// error.
const (
	videoTimeScale = 90000
	audioTimeScale = 48000

	videoTrackID = 1
	audioTrackID = 2
)

// ErrNoParameterSets says the SPS or the PPS is missing, and without them the
// video track cannot be described.
var ErrNoParameterSets = errors.New("media: SPS or PPS missing")

// ErrNoSamples says there is nothing to write.
var ErrNoSamples = errors.New("media: no samples")

// FMP4Muxer produces fragmented MP4 from H.264 Annex-B and Opus packets.
//
// **Its only user is a measuring tool.** It was born for the Media Source
// Extensions fallback and for recording clips: the fallback is not done, and
// the clips are written by MP4Clip, which on disk can afford the index. Today
// only pat-capture instantiates it, to write a playable file out of what the
// encoder produced — which is a real job, but the job of a test bench.
//
// Fragmented stays the right shape for that job: it is written as it arrives,
// without knowing when it ends. That is why the muxer knows neither the network
// nor files — it accumulates samples and returns segments, and whoever uses it
// decides where to send them.
//
// It is not safe for concurrent use: every consumer has their own.
type FMP4Muxer struct {
	sps, pps      []byte
	audioChannels int

	seq uint32
	// baseTime is the start instant of the next segment, per track, in that
	// track's time scale. It has to carry on without a break from one segment
	// to the next, otherwise the player sees jumps and stops.
	videoBase, audioBase uint64

	videoSamples []*fmp4.Sample
	audioSamples []*fmp4.Sample
}

// NewFMP4Muxer prepares a muxer for the tracks described.
//
// audioChannels is the channel count declared for Opus. The SDP's warning
// applies: the stream is mono, but players expect the declaration to say two
// channels.
func NewFMP4Muxer(sps, pps []byte, audioChannels int) (*FMP4Muxer, error) {
	sps, pps, audioChannels, err := trackSetup(sps, pps, audioChannels)
	if err != nil {
		return nil, err
	}
	return &FMP4Muxer{sps: sps, pps: pps, audioChannels: audioChannels}, nil
}

// trackSetup validates the parameters common to the two writers and copies what
// they hold on to.
//
// **The rule inside is not a convenience, it is normative.** The two channels
// declared for Opus when none are asked for are the SDP's own warning — the
// stream is mono and players expect the declaration to say two, RFC 7587 — and
// written in two constructors it could change in one only, with the clip on
// disk and the stream declaring different channel counts. Covered by
// TestBothWritersDeclareTheSameOpusChannels.
//
// The defensive copy is here for the same reason: whoever passes the SPS and
// PPS may reuse those slices, and forgetting that in one of the two writers
// would give parameter sets that change under the feet of whoever has already
// written them.
func trackSetup(sps, pps []byte, audioChannels int) ([]byte, []byte, int, error) {
	if len(sps) == 0 || len(pps) == 0 {
		return nil, nil, 0, ErrNoParameterSets
	}
	if audioChannels <= 0 {
		audioChannels = 2
	}
	return append([]byte(nil), sps...), append([]byte(nil), pps...), audioChannels, nil
}

// Init returns the initialisation block, to be sent once before any segment.
func (m *FMP4Muxer) Init() ([]byte, error) {
	init := fmp4.Init{
		Tracks: []*fmp4.InitTrack{
			{
				ID:        videoTrackID,
				TimeScale: videoTimeScale,
				Codec:     &codecs.H264{SPS: m.sps, PPS: m.pps},
			},
			{
				ID:        audioTrackID,
				TimeScale: audioTimeScale,
				Codec:     &codecs.Opus{ChannelCount: m.audioChannels},
			},
		},
	}
	var buf seekablebuffer.Buffer
	if err := init.Marshal(&buf); err != nil {
		return nil, fmt.Errorf("fmp4: init: %w", err)
	}
	return buf.Bytes(), nil
}

// AddVideo appends an Annex-B access unit with the declared duration.
//
// The SPS, PPS and delimiters do not go into the sample: the parameter sets are
// already in the initialisation block, and repeating them on every keyframe
// would swell the segments without adding anything.
func (m *FMP4Muxer) AddVideo(au []byte, dur time.Duration) error {
	avcc, key, err := avccSample(au)
	if err != nil {
		return err
	}
	if avcc == nil {
		return nil
	}

	m.videoSamples = append(m.videoSamples, &fmp4.Sample{
		Duration:        uint32(durToScale(dur, videoTimeScale)),
		IsNonSyncSample: !key,
		Payload:         avcc,
	})
	return nil
}

// AddAudio appends an Opus packet with the declared duration.
func (m *FMP4Muxer) AddAudio(pkt []byte, dur time.Duration) {
	if len(pkt) == 0 {
		return
	}
	m.audioSamples = append(m.audioSamples, &fmp4.Sample{
		Duration: uint32(durToScale(dur, audioTimeScale)),
		Payload:  append([]byte(nil), pkt...),
	})
}

// Segment packs the accumulated samples and clears the accumulation.
//
// It returns nil when there is nothing to send, so the caller can invoke it at
// a fixed cadence without worrying about the empty moments.
func (m *FMP4Muxer) Segment() ([]byte, error) {
	if len(m.videoSamples) == 0 && len(m.audioSamples) == 0 {
		return nil, nil
	}

	part := fmp4.Part{SequenceNumber: m.seq}
	if len(m.videoSamples) > 0 {
		part.Tracks = append(part.Tracks, &fmp4.PartTrack{
			ID:       videoTrackID,
			BaseTime: m.videoBase,
			Samples:  m.videoSamples,
		})
		m.videoBase += totalDuration(m.videoSamples)
	}
	if len(m.audioSamples) > 0 {
		part.Tracks = append(part.Tracks, &fmp4.PartTrack{
			ID:       audioTrackID,
			BaseTime: m.audioBase,
			Samples:  m.audioSamples,
		})
		m.audioBase += totalDuration(m.audioSamples)
	}

	var buf seekablebuffer.Buffer
	if err := part.Marshal(&buf); err != nil {
		return nil, fmt.Errorf("fmp4: segment: %w", err)
	}

	m.seq++
	m.videoSamples = nil
	m.audioSamples = nil
	return buf.Bytes(), nil
}

func totalDuration(samples []*fmp4.Sample) uint64 {
	var t uint64
	for _, s := range samples {
		t += uint64(s.Duration)
	}
	return t
}

// durToScale converts a duration into the given time scale.
//
// The rounding is to nearest and not down: truncating systematically would lose
// a few units on every sample, and over a whole night the declared time would
// slip behind the real one.
func durToScale(d time.Duration, scale int64) int64 {
	if d <= 0 {
		return 0
	}
	return (int64(d)*scale + int64(time.Second)/2) / int64(time.Second)
}

// avccSample converts an access unit from Annex-B to AVCC — the length in front
// of every NAL instead of the start codes — and says whether it is a keyframe.
//
// It lives here and nowhere else because both muxers use it, the fragmented one
// for streaming and the progressive one for clips: written twice it would
// diverge, and the first thing to come out would be a file that opens and does
// not decode.
//
// The SPS, PPS and delimiters do not go into the sample: the parameter sets are
// already in the header, and repeating them on every keyframe swells the file
// without adding anything. It returns nil when no useful NAL is left.
func avccSample(au []byte) (payload []byte, keyframe bool, err error) {
	var nalus [][]byte
	IterateAnnexB(au, func(n NAL) bool {
		switch n.Type {
		case NALTypeSPS, NALTypePPS, NALTypeAUD:
			return true
		}
		if n.IsKeyframe() {
			keyframe = true
		}
		nalus = append(nalus, n.Data)
		return true
	})
	if len(nalus) == 0 {
		return nil, false, nil
	}
	// h264.AVCC(...).Marshal() is public API and not deprecated, unlike
	// fmp4.NewSampleH264, which beyond this did nothing we needed.
	payload, err = h264.AVCC(nalus).Marshal()
	if err != nil {
		return nil, false, fmt.Errorf("media: video sample: %w", err)
	}
	return payload, keyframe, nil
}
