package media

import (
	"encoding/hex"
	"fmt"
)

// H.264 NAL unit types (RFC 6184 / ITU-T H.264 table 7-1).
const (
	NALTypeNonIDR byte = 1
	NALTypeIDR    byte = 5
	NALTypeSEI    byte = 6
	NALTypeSPS    byte = 7
	NALTypePPS    byte = 8
	NALTypeAUD    byte = 9
)

// NAL is a NAL unit without its start code.
type NAL struct {
	Type byte
	Data []byte
}

// IsVCL says whether the NAL carries picture data (a slice).
func (n NAL) IsVCL() bool { return n.Type >= 1 && n.Type <= 5 }

// IsKeyframe says whether the NAL is an IDR slice.
func (n NAL) IsKeyframe() bool { return n.Type == NALTypeIDR }

// RefIDC is nal_ref_idc, that is, whether and how much this NAL serves as a
// reference for others. Zero means it can be dropped without breaking anything
// after it.
//
// It sits in the two bits below the forbidden bit, inside the same header byte
// Type comes from. It is needed to read a slice header: whether
// dec_ref_pic_marking is present depends on it, and getting it wrong shifts
// every field after.
func (n NAL) RefIDC() int {
	if len(n.Data) == 0 {
		return 0
	}
	return int(n.Data[0]>>5) & 3
}

// StartsNewFrame says whether this slice opens a new picture.
//
// The first field of the slice header is first_mb_in_slice, coded ue(v): it is
// 0 if and only if the first bit of the RBSP is 1. An encoder emitting several
// slices per frame produces later slices with first_mb_in_slice > 0, so this
// test finds the frame boundaries correctly with multi-slice too.
func (n NAL) StartsNewFrame() bool {
	if !n.IsVCL() || len(n.Data) < 2 {
		return false
	}
	return n.Data[1]&0x80 != 0
}

// IterateAnnexB calls fn for every NAL unit found in b, which has to be an
// Annex-B bitstream (NALs separated by 00 00 01 or 00 00 00 01 start codes). If
// fn returns false the iteration stops.
func IterateAnnexB(b []byte, fn func(NAL) bool) {
	starts := findStartCodes(b)
	for i, s := range starts {
		end := len(b)
		if i+1 < len(starts) {
			end = starts[i+1].offset
		}
		data := b[s.payload:end]
		if len(data) == 0 {
			continue
		}
		if !fn(NAL{Type: data[0] & 0x1F, Data: data}) {
			return
		}
	}
}

type startCode struct {
	offset  int // where the start code begins
	payload int // first byte of the NAL
}

func findStartCodes(b []byte) []startCode {
	var out []startCode
	for i := 0; i+2 < len(b); i++ {
		if b[i] != 0 || b[i+1] != 0 {
			continue
		}
		switch {
		case b[i+2] == 1:
			out = append(out, startCode{offset: i, payload: i + 3})
			i += 2
		case i+3 < len(b) && b[i+2] == 0 && b[i+3] == 1:
			out = append(out, startCode{offset: i, payload: i + 4})
			i += 3
		}
	}
	return out
}

// The profile_idc values that matter (ITU-T H.264 Annex A).
const (
	profileIDCBaseline = 66  // 0x42
	profileIDCMain     = 77  // 0x4d
	profileIDCHigh     = 100 // 0x64
)

// constrainedBaselineFlags are constraint_set0/1/2_flag switched on, that is,
// the 0xE0 byte that appears in every "42e0xx" profile-level-id.
const constrainedBaselineFlags = 0xE0

// PatchConstrainedBaseline marks a Baseline SPS as Constrained Baseline,
// changing the constraint flag byte in place. It returns true if it acted.
//
// It is there for browser compatibility. Asking for the Baseline profile gives
// "pure" Baseline: profile_idc=66 with the constraint flags at zero, so a
// profile-level-id of 4200xx. That is not an oddity of one encoder: it is what
// "baseline" means. Browsers, however, implement Constrained Baseline, 42e0xx,
// and WebRTC's codec comparison looks at exactly those bits: a 4200xx offer is
// refused with "codec is not supported by remote". Paradoxically, pure Baseline
// is less compatible than Main.
//
// The assertion is truthful, not a workaround: constraint_set0/1/2_flag declare
// conformance to the Baseline, Main and Extended profiles, and a stream
// produced by these encoders without B-frames does conform (it uses no FMO, no
// ASO and no redundant slices, which are the only things Baseline allows and
// Main forbids).
//
// The change is safe without reinterpreting the bitstream: profile_idc, the
// constraint flags and level_idc are the three bytes right after the NAL
// header, in the clear. They cannot be moved by an emulation prevention byte,
// which is only inserted after two consecutive 0x00, and here profile_idc and
// level_idc are never zero.
func PatchConstrainedBaseline(sps []byte) bool {
	if len(sps) < 4 || sps[0]&0x1F != NALTypeSPS {
		return false
	}
	if sps[1] != profileIDCBaseline {
		return false // Main and High are already announced correctly
	}
	if sps[2] == constrainedBaselineFlags {
		return false // already fine
	}
	sps[2] = constrainedBaselineFlags
	return true
}

// levelLimits are the limits of the H.264 levels (ITU-T H.264 table A-1),
// ordered from lowest to highest.
//
// MaxBR is the one for the Baseline, Constrained Baseline, Main and Extended
// profiles.
var levelLimits = []struct {
	idc      byte // level_idc value
	maxMBPS  int  // macroblocks per second
	maxFS    int  // macroblocks per picture
	maxBRKbs int  // maximum bitrate in kbit/s
}{
	{10, 1485, 99, 64},
	{11, 3000, 396, 192},
	{12, 6000, 396, 384},
	{13, 11880, 396, 768},
	{20, 11880, 396, 2000},
	{21, 19800, 792, 4000},
	{22, 20250, 1620, 4000},
	{30, 40500, 1620, 10000},
	{31, 108000, 3600, 14000},
	{32, 216000, 5120, 20000},
	{40, 245760, 8192, 20000},
	{41, 245760, 8192, 50000},
	{42, 522240, 8704, 50000},
	{50, 589824, 22080, 135000},
	{51, 983040, 36864, 240000},
}

// MinLevelIDC works out the lowest H.264 level that satisfies the given
// parameters.
//
// It is needed because hardware encoders choose the level conservatively:
// 1280x720 at 30fps occupies exactly 3600 macroblocks per picture and 108000 a
// second, which is precisely the limits of level 3.1, and those are inclusive.
// The slightest fluctuation in the webcam's framerate is enough for QSV to
// declare 3.2 while the stream still conforms to 3.1. Browsers announce 3.1, so
// negotiation fails over a level declared higher than necessary.
func MinLevelIDC(width, height, fps, bitrateKbps int) byte {
	// Macroblocks are 16x16, rounding up.
	mbW := (width + 15) / 16
	mbH := (height + 15) / 16
	frameSize := mbW * mbH
	mbps := frameSize * fps

	for _, l := range levelLimits {
		if frameSize <= l.maxFS && mbps <= l.maxMBPS && bitrateKbps <= l.maxBRKbs {
			return l.idc
		}
	}
	// Beyond the tabulated levels: the highest known one comes back.
	return levelLimits[len(levelLimits)-1].idc
}

// PatchLevel lowers an SPS's level_idc to the given value.
//
// It only acts downwards: raising the declared level without the stream
// respecting it would make the decoder accept a stream it then cannot handle,
// that is, a silent fault. Lowering it to the level really needed is instead a
// correction of an over-cautious choice by the encoder.
func PatchLevel(sps []byte, level byte) bool {
	if len(sps) < 4 || sps[0]&0x1F != NALTypeSPS || level == 0 {
		return false
	}
	if sps[3] <= level {
		return false
	}
	sps[3] = level
	return true
}

// ProfileLevelID derives the "profile-level-id" fmtp value to use in the SDP
// from an SPS.
//
// It makes what the encoder produces match what is announced to the browser: if
// constrained baseline is declared but Main is sent, some browsers cannot
// decode. The three bytes are, in order, profile_idc, constraint_set_flags and
// level_idc, that is, exactly bytes 1..3 of the SPS.
func ProfileLevelID(sps []byte) (string, error) {
	if len(sps) < 4 {
		return "", fmt.Errorf("h264: SPS too short (%d bytes)", len(sps))
	}
	return hex.EncodeToString(sps[1:4]), nil
}

// SPSSize reads the frame size, in pixels, out of the SPS.
//
// It is the only way to know what the decoder sees. When the resolution scale
// changes size, the Source Reader declares it has resized and the encoder
// declares it has accepted: those are two answers, and this project has already
// paid enough to know that an answer is not an effect. The real pixels are
// written here, in the stream, and there is no way for them to be a
// declaration.
//
// It is needed outside this package too: the header of a fragmented MP4 carries
// the size in the track, and taking it from elsewhere means taking it from
// somewhere that might disagree with the stream.
func SPSSize(sps []byte) (width, height int, err error) {
	// The payload starts after the NAL header byte, and it has to be cleaned of
	// the anti-emulation bytes: inside the SPS a 00 00 03 sequence is a 00 00
	// with an 03 pushed in between so it does not look like a start code.
	if len(sps) < 5 {
		return 0, 0, fmt.Errorf("h264: SPS too short (%d bytes)", len(sps))
	}
	rbsp := make([]byte, 0, len(sps))
	zeros := 0
	for _, b := range sps[1:] {
		if zeros >= 2 && b == 3 {
			zeros = 0
			continue
		}
		if b == 0 {
			zeros++
		} else {
			zeros = 0
		}
		rbsp = append(rbsp, b)
	}

	r := &bitReader{b: rbsp}
	profile := r.bits(8)
	r.bits(8) // constraint flags and reserved
	r.bits(8) // level_idc
	r.ue()    // seq_parameter_set_id

	// The high-fidelity profiles slip in fields here that the others do not
	// have. Skipping them is compulsory: without that, every later read shifts
	// and the size that comes out is plausible and wrong.
	switch profile {
	case 100, 110, 122, 244, 44, 83, 86, 118, 128, 138, 139, 134, 135:
		if chroma := r.ue(); chroma == 3 {
			r.bits(1) // separate_colour_plane_flag
		}
		r.ue() // bit_depth_luma_minus8
		r.ue() // bit_depth_chroma_minus8
		r.bits(1)
		if r.bits(1) == 1 { // seq_scaling_matrix_present_flag
			return 0, 0, fmt.Errorf("h264: SPS with scaling matrices, not handled")
		}
	}

	r.ue() // log2_max_frame_num_minus4
	switch r.ue() {
	case 0:
		r.ue() // log2_max_pic_order_cnt_lsb_minus4
	case 1:
		r.bits(1)
		r.se()
		r.se()
		for n := r.ue(); n > 0; n-- {
			r.se()
		}
	}
	r.ue()    // max_num_ref_frames
	r.bits(1) // gaps_in_frame_num_value_allowed_flag

	widthMBs := r.ue() + 1
	heightMapUnits := r.ue() + 1
	frameMBsOnly := r.bits(1)
	if frameMBsOnly == 0 {
		r.bits(1) // mb_adaptive_frame_field_flag
	}
	r.bits(1) // direct_8x8_inference_flag

	width = widthMBs * 16
	height = heightMapUnits * 16
	if frameMBsOnly == 0 {
		height *= 2
	}

	// The cropping is not a detail: 1080 is not a multiple of 16, so a 1920x1080
	// is coded 1920x1088 and cropped. Without this, eight rows nobody sees would
	// be reported.
	if r.bits(1) == 1 { // frame_cropping_flag
		left, right, top, bottom := r.ue(), r.ue(), r.ue(), r.ue()
		// The cropping units depend on the chroma subsampling: for 4:2:0, which
		// is what we produce, they are 2 horizontally and 2 vertically on
		// progressive frames.
		width -= (left + right) * 2
		height -= (top + bottom) * 2
	}
	if r.err != nil {
		return 0, 0, r.err
	}
	if width <= 0 || height <= 0 {
		return 0, 0, fmt.Errorf("h264: absurd size in the SPS (%dx%d)", width, height)
	}
	return width, height, nil
}

// bitReader reads the SPS bit by bit, with the variable-length codes H.264 uses
// everywhere. Once the supply runs out it stops and says so, rather than
// returning zeros that would be read as real values.
type bitReader struct {
	b   []byte
	pos int
	err error
}

func (r *bitReader) bit() int {
	if r.pos >= len(r.b)*8 {
		r.err = fmt.Errorf("h264: SPS ended sooner than expected")
		return 0
	}
	v := int(r.b[r.pos/8]>>(7-r.pos%8)) & 1
	r.pos++
	return v
}

// bits reads n bits, and **stops as soon as there is nothing left to read**.
//
// The loop used to run to n whatever happened, which is harmless while n is a
// literal — every call in this file asks for 1, 2, 8 or 16 — and is not
// harmless at all where n comes out of the stream. QPSlice asks for
// `Log2MaxFrameNum` and `Log2MaxPicOrderCntLsb`, both of them an exp-Golomb
// value the SPS supplied plus four, so the largest a malformed SPS could hand
// over is **4 294 967 298** — and QPSlice runs once a frame. Measured with the
// old loop restored: **7.2 seconds for one call** at n = 1<<32. The quantiser
// was never wrong, which is why nothing pointed here; the frame loop simply
// stopped.
//
// The count is validated where it is read, in ParsePPS, and this is the belt
// underneath it.
//
// **Only the loop was changed, and that is the whole of it.** `bit` builds its
// error afresh at every call past the end, which made the old loop an
// allocation per bit as well — a million of them for n = 1<<20, measured. With
// the loop stopping there is no second call to save: every reader in this file
// exits on `err`, so the repeated error is unreachable and recording it once
// would be a change with nothing behind it and a test that could not fail.
func (r *bitReader) bits(n int) int {
	v := 0
	for i := 0; i < n && r.err == nil; i++ {
		v = v<<1 | r.bit()
	}
	return v
}

// ue reads an unsigned integer in exponential-Golomb coding.
func (r *bitReader) ue() int {
	zeros := 0
	for r.err == nil && r.bit() == 0 {
		zeros++
		if zeros > 31 {
			r.err = fmt.Errorf("h264: Golomb code too long")
			return 0
		}
	}
	if r.err != nil {
		return 0
	}
	return (1 << zeros) - 1 + r.bits(zeros)
}

// se reads the signed variant.
func (r *bitReader) se() int {
	v := r.ue()
	if v%2 == 0 {
		return -v / 2
	}
	return (v + 1) / 2
}

// AUAssembler reassembles the access units (complete frames) from an Annex-B
// stream that arrives in pieces from a pipe.
//
// It is needed because Pion's TrackLocalStaticSample wants one sample per frame
// with its duration: passing single NALs would produce wrong RTP timestamps.
type AUAssembler struct {
	buf     []byte
	pending []byte // access unit under construction, in Annex-B form
	seenVCL bool

	sps []byte
	pps []byte

	// PatchedSPS reports that a Baseline SPS has been marked as Constrained
	// Baseline. Useful for diagnosis: it explains why the announced
	// profile-level-id differs from the one the encoder had produced.
	PatchedSPS bool

	// TargetLevelIDC, when non-zero, is the level_idc to bring back the SPSs
	// that declare a level higher than necessary. It is computed with
	// MinLevelIDC from the real encoding parameters.
	TargetLevelIDC byte

	// preFlushed remembers that the access unit has already been closed by
	// looking at the header of the next NAL, still incomplete. When that NAL
	// arrives whole it must not be judged as a boundary again.
	preFlushed bool
}

// AccessUnit is one complete coded frame.
type AccessUnit struct {
	Data     []byte // Annex-B, start codes included
	Keyframe bool
}

// Write adds raw data and returns the access units it completed.
//
// An access unit counts as complete when the next one begins: so the last frame
// stays pending until more data arrives. For a live stream that is the right
// behaviour (one frame of delay).
func (a *AUAssembler) Write(chunk []byte) []AccessUnit {
	a.buf = append(a.buf, chunk...)

	starts := findStartCodes(a.buf)
	if len(starts) < 2 {
		return nil // there is not even one complete NAL yet
	}

	var out []AccessUnit
	// Everything up to the second-to-last start code is processed: the last NAL
	// may be truncated and has to be left in the buffer.
	for i := 0; i < len(starts)-1; i++ {
		nalStart, nalEnd := starts[i].payload, starts[i+1].offset
		data := a.buf[nalStart:nalEnd]
		if len(data) == 0 {
			continue
		}
		n := NAL{Type: data[0] & 0x1F, Data: data}

		// This NAL's boundary may already have been judged on the previous
		// turn, by looking at its header while it was still incomplete.
		if a.preFlushed {
			a.preFlushed = false
		} else if isAUBoundary(n, a.seenVCL) {
			if au := a.flush(); au != nil {
				out = append(out, *au)
			}
		}

		switch n.Type {
		case NALTypeSPS:
			// The patch happens here, before the NAL is copied into the access
			// unit: that way the SPS announced in the SDP and the one actually
			// sent over RTP stay the same one. A browser receiving an SPS other
			// than the negotiated one may refuse it or decode badly.
			if PatchConstrainedBaseline(data) {
				a.PatchedSPS = true
			}
			if PatchLevel(data, a.TargetLevelIDC) {
				a.PatchedSPS = true
			}
			a.sps = append(a.sps[:0], data...)
		case NALTypePPS:
			a.pps = append(a.pps[:0], data...)
		}

		a.pending = append(a.pending, 0, 0, 0, 1)
		a.pending = append(a.pending, data...)
		if n.IsVCL() {
			a.seenVCL = true
		}
	}

	// Peek at the last NAL, still incomplete, to close the pending access unit
	// straight away.
	//
	// Without this, an access unit would only be emitted after the next NAL had
	// been processed whole, which in turn needs the one after that to arrive:
	// two frames of delay instead of one. To decide whether a NAL opens a new
	// picture, though, its first two bytes are enough — the type and the first
	// bit of the slice header.
	lastStart := starts[len(starts)-1]
	if !a.preFlushed && lastStart.payload+2 <= len(a.buf) {
		head := a.buf[lastStart.payload:]
		n := NAL{Type: head[0] & 0x1F, Data: head}
		if isAUBoundary(n, a.seenVCL) {
			if au := a.flush(); au != nil {
				out = append(out, *au)
			}
			a.preFlushed = true
		}
	}

	// Keep the last (possibly incomplete) NAL.
	a.buf = append(a.buf[:0], a.buf[lastStart.offset:]...)
	return out
}

// isAUBoundary says whether a NAL opens a new access unit.
//
// One does if it is a slice starting a new picture, or a parameter set, an SEI
// or a delimiter met after a VCL has already been seen.
func isAUBoundary(n NAL, seenVCL bool) bool {
	if !seenVCL {
		return false
	}
	if n.IsVCL() {
		return n.StartsNewFrame()
	}
	return true
}

// flush closes the access unit under construction.
func (a *AUAssembler) flush() *AccessUnit {
	if len(a.pending) == 0 {
		return nil
	}
	data := make([]byte, len(a.pending))
	copy(data, a.pending)

	keyframe := false
	IterateAnnexB(data, func(n NAL) bool {
		if n.IsKeyframe() {
			keyframe = true
			return false
		}
		return true
	})

	a.pending = a.pending[:0]
	a.seenVCL = false
	return &AccessUnit{Data: data, Keyframe: keyframe}
}

// Stats sums up the make-up of an Annex-B bitstream. It is there to check that
// what comes out of the encoder is valid H.264, and not merely that the encoder
// did not protest: it is the same distinction between answer and effect that
// holds for its commands.
type Stats struct {
	SPS, PPS, IDR, NonIDR, SEI, Other int
	ProfileLevelID                    string
}

// Frames estimates the number of coded pictures.
func (s Stats) Frames() int { return s.IDR + s.NonIDR }

// Analyze counts the NALs of a complete Annex-B bitstream.
func Analyze(b []byte) Stats {
	var st Stats
	IterateAnnexB(b, func(n NAL) bool {
		switch n.Type {
		case NALTypeSPS:
			st.SPS++
			if st.ProfileLevelID == "" {
				if id, err := ProfileLevelID(n.Data); err == nil {
					st.ProfileLevelID = id
				}
			}
		case NALTypePPS:
			st.PPS++
		case NALTypeIDR:
			st.IDR++
		case NALTypeNonIDR:
			st.NonIDR++
		case NALTypeSEI:
			st.SEI++
		default:
			st.Other++
		}
		return true
	})
	return st
}
