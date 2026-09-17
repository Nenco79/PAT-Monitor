package media

import "fmt"

// The quantiser is read from the stream, not asked of the encoder.
//
// **Why asking is not enough.** MFSampleExtension_VideoEncodeQP is an
// **optional** attribute: the documentation promises "the H.264 Video Encoder"
// sets it, and that link leads to Microsoft's software encoder. No clause binds
// the hardware transforms, and HLK certification covers ICodecAPI properties,
// not sample attributes. Measured: Quick Sync sets it, the AMD encoder sets
// **no** attribute at all on its samples. Chromium knows this and has a
// recovery branch for Intel only.
//
// Keeping the attribute as the source would mean that everything resting on the
// quantiser — the window percentile, the learned reference, the scale coming
// down on quality — works on one vendor and not on the other. Here it is read
// from the bitstream instead, which is **the same thing the decoder reads**:
// the check is taken as close as possible to whoever consumes the data.
//
// It is also what libwebrtc does, with the very same formula.

// QPSlice derives a slice's quantiser from the PPS and from its own header.
//
//	QP = 26 + pic_init_qp_minus26 + slice_qp_delta
//
// The two addends sit in two different places: the first in the PPS, which
// arrives once and holds for every slice citing it; the second in the slice
// header. That is why the PPS has to be kept as it goes past — see QPReader.
//
// **It is the slice quantiser, not the average of the macroblocks.** With the
// per-macroblock limits free, individual blocks can deviate, so this number
// says with what approximation the encoder set the slice, not the one it
// finished each of its pieces with. It is the compromise libwebrtc accepts too,
// and it is the number that governs the scale in any case.
func QPSlice(pps ppsInfo, slice NAL) (qp int, ok bool) {
	if !slice.IsVCL() || len(slice.Data) < 2 {
		return 0, false
	}
	r := &bitReader{b: rbsp(slice.Data[1:])}

	r.ue() // first_mb_in_slice
	sliceType := r.ue()

	// **A B slice is refused, not read.** We ask the encoder for Baseline,
	// which forbids them — but a profile is a request like any other, and the
	// rule here is the one this codebase repeats: weigh what comes out, not
	// what was answered.
	//
	// Reading one would not fail. A B slice header carries three fields this
	// function does not know about — direct_spatial_mv_pred_flag,
	// num_ref_idx_l1_active_minus1, and a second reference-list modification —
	// so every field after them shifts and what comes back is a **plausible and
	// wrong** quantiser. It is exactly the fault this parser has already paid
	// for once, with num_ref_idx_active_override_flag.
	//
	// And nothing else was catching it: ParsePPS refuses CABAC and the High
	// profiles, while B frames are allowed from Main upwards.
	if sliceType%5 == 1 {
		return 0, false
	}
	if r.ue() != pps.ID { // pic_parameter_set_id
		// The slice cites a PPS other than the one we have: reading it with
		// this one would give a plausible and wrong number.
		return 0, false
	}
	if pps.SeparateColourPlane {
		r.bits(2) // colour_plane_id
	}
	r.bits(pps.Log2MaxFrameNum) // frame_num

	// Without frame_mbs_only_flag there would be field_pic_flag and
	// bottom_field_flag. We do not know how to read those — see the refusal in
	// ParsePPS.
	if slice.Type == NALTypeIDR {
		r.ue() // idr_pic_id
	}
	if pps.PicOrderCntType == 0 {
		r.bits(pps.Log2MaxPicOrderCntLsb) // pic_order_cnt_lsb
		if pps.BottomFieldPicOrderInFrame {
			r.se() // delta_pic_order_cnt_bottom
		}
	}
	if pps.RedundantPicCntPresent {
		r.ue() // redundant_pic_cnt
	}

	// **num_ref_idx_active_override_flag is only in P slices, and forgetting it
	// costs one bit that shifts everything after it.**
	//
	// The symptom was exactly what this file warns about: a quantiser that is
	// plausible and wrong. With the floor at 30, the IDR slices — which do not
	// go through this branch — read 30, and the P slices read 18. Mean 18.2 and
	// maximum 30: two populations, and the larger one was the misread one. Had
	// the floor been closer to the truth, nobody would have noticed.
	if t := sliceType % 5; t == 0 || t == 3 { // P or SP
		if r.bit() == 1 { // num_ref_idx_active_override_flag
			r.ue() // num_ref_idx_l0_active_minus1
		}
	}

	// ref_pic_list_modification, only for slices that have references.
	//
	// **A code this list does not define ends the read, it does not get
	// skipped.** modification_of_pic_nums_idc is 0 to 3, and anything else means
	// the bit offset is already lost: carrying on would consume one more ue(v)
	// for every iteration of a loop that no longer knows what it is reading, and
	// arrive at slice_qp_delta with a number to hand out. Same for running out
	// of entries without meeting the terminator.
	if t := sliceType % 5; t != 2 && t != 4 {
		if r.bit() == 1 { // ref_pic_list_modification_flag_l0
			done := false
			for i := 0; i < 64 && !done; i++ {
				switch op := r.ue(); op {
				case 3:
					done = true
				case 0, 1, 2:
					r.ue() // abs_diff_pic_num_minus1 or long_term_pic_num
				default:
					return 0, false
				}
			}
			if !done {
				return 0, false
			}
		}
	}

	// dec_ref_pic_marking, only if the NAL is a reference.
	//
	// **Operation 5 carries no argument, and reading one for it was the same
	// defect a third time.** The table in 7.3.3.3 gives an argument to 1, 2, 3
	// (two of them), 4 and 6; MMCO 5 — "mark everything unused for reference" —
	// has none, and the ue(v) that used to be read for it came out of
	// slice_qp_delta's bits. What followed was the outcome this file exists to
	// refuse: a quantiser that is **plausible and wrong**, out of a header that
	// was read to the letter everywhere else.
	//
	// It is latent rather than seen: our encoders mark with the sliding window,
	// so the flag below is zero and the loop is never entered. That is the same
	// argument the B-slice refusal above already rejects — the marking mode is
	// the encoder's to choose, and a mode we do not use is not a mode we cannot
	// be given.
	if slice.RefIDC() != 0 {
		if slice.Type == NALTypeIDR {
			r.bits(2) // no_output_of_prior_pics_flag, long_term_reference_flag
		} else if r.bit() == 1 { // adaptive_ref_pic_marking_mode_flag
			done := false
			for i := 0; i < 64 && !done; i++ {
				switch op := r.ue(); op {
				case 0:
					done = true
				case 1:
					r.ue() // difference_of_pic_nums_minus1
				case 2:
					r.ue() // long_term_pic_num
				case 3:
					r.ue() // difference_of_pic_nums_minus1
					r.ue() // long_term_frame_idx
				case 4:
					r.ue() // max_long_term_frame_idx_plus1
				case 5:
					// Nothing: it is the whole point of this repair.
				case 6:
					r.ue() // long_term_frame_idx
				default:
					return 0, false
				}
			}
			if !done {
				return 0, false
			}
		}
	}

	// cabac_init_idc would fall here, but it cannot be present: ParsePPS
	// refuses PPSs with CABAC on, and for the same reason there is no skipping
	// of prediction weights.
	qp = 26 + pps.InitQPMinus26 + r.se()
	if r.err != nil || qp < 0 || qp > 51 {
		return 0, false
	}
	return qp, true
}

// ppsInfo is the little of the PPS needed to read a slice header.
type ppsInfo struct {
	ID                         int
	InitQPMinus26              int
	RedundantPicCntPresent     bool
	BottomFieldPicOrderInFrame bool
	// These three come from the SPS the PPS cites, not from the PPS itself.
	Log2MaxFrameNum       int
	Log2MaxPicOrderCntLsb int
	PicOrderCntType       int
	SeparateColourPlane   bool
}

// rbsp strips the anti-emulation bytes: a 00 00 03 sequence is a 00 00 with an
// 03 pushed in between so it does not look like a start code.
func rbsp(b []byte) []byte {
	out := make([]byte, 0, len(b))
	zeros := 0
	for _, x := range b {
		if zeros >= 2 && x == 3 {
			zeros = 0
			continue
		}
		if x == 0 {
			zeros++
		} else {
			zeros = 0
		}
		out = append(out, x)
	}
	return out
}

// ParsePPS reads the PPS, together with the little of the SPS needed to
// interpret it.
//
// **It refuses rather than guessing.** Our stream is constrained baseline by
// construction, and there a slice header is short: no CABAC, no prediction
// weights, no interlaced fields. If the stream contradicted one of those
// assumptions, the fields after it would shift by a few bits and out would come
// a plausible and wrong quantiser — which is far worse than no quantiser,
// because it would command the resolution scale with nobody able to notice. A
// zero means "I do not know", never "zero".
func ParsePPS(sps, pps NAL) (ppsInfo, error) {
	var out ppsInfo
	if sps.Type != NALTypeSPS || pps.Type != NALTypePPS {
		return out, fmt.Errorf("h264: an SPS and a PPS are required")
	}

	s := &bitReader{b: rbsp(sps.Data[1:])}
	profile := s.bits(8)
	s.bits(16) // constraints and level
	s.ue()     // seq_parameter_set_id
	switch profile {
	case 100, 110, 122, 244, 44, 83, 86, 118, 128, 138, 139, 134, 135:
		// The high-fidelity profiles slip in fields the others do not have, and
		// they bring B-frames and CABAC too: this is not our stream.
		return out, fmt.Errorf("h264: profile %d not supported for QP reading", profile)
	}
	// **These two are bit counts, so an absurd one is not an absurd number: it
	// is a length.** Both are `x + 4` of an exp-Golomb value, and an exp-Golomb
	// value reaches about 4.29e9; QPSlice hands each of them straight to
	// `bits`, which used to loop that many times, on every frame, for as long
	// as the parameter sets stood. The specification puts both minus4 fields in
	// 0 to 12, so anything outside that is not a stream we misread — it is a
	// stream we never had, and the answer is the one this file gives to
	// everything it cannot account for.
	const maxLog2Minus4 = 12
	frameNumBits := s.ue()
	if frameNumBits > maxLog2Minus4 {
		return out, fmt.Errorf("h264: log2_max_frame_num_minus4 is %d, out of range", frameNumBits)
	}
	out.Log2MaxFrameNum = frameNumBits + 4
	out.PicOrderCntType = s.ue()
	switch out.PicOrderCntType {
	case 0:
		n := s.ue()
		if n > maxLog2Minus4 {
			return out, fmt.Errorf("h264: log2_max_pic_order_cnt_lsb_minus4 is %d, out of range", n)
		}
		out.Log2MaxPicOrderCntLsb = n + 4
	case 2:
		// Nothing to read: presentation order follows coding order.
	default:
		// Type 1 puts deltas in the slice header whose number depends on SPS
		// fields. It can be done, but none of our encoders uses it, and writing
		// it blind would mean writing code that cannot be tested — better to
		// say "I do not know" than to guess.
		return out, fmt.Errorf("h264: pic_order_cnt_type %d not supported", out.PicOrderCntType)
	}
	// **The interlaced-field refusal, which two comments promised and nobody
	// had written.** With `frame_mbs_only_flag == 0` the slice header carries
	// `field_pic_flag`, and possibly `bottom_field_flag`, **between `frame_num`
	// and `idr_pic_id`** — so every field after them shifts by one or two bits
	// and `slice_qp_delta` comes back plausible and wrong, which is the one
	// outcome this parser exists to refuse. It is the forgotten
	// `num_ref_idx_active_override_flag` and the unrefused B slice a third time:
	// an unread flag is not a value that can be corrected afterwards, it is a
	// bit offset.
	//
	// It is read here, four fields further on than this function used to walk,
	// because the flag lives after `max_num_ref_frames`,
	// `gaps_in_frame_num_value_allowed_flag`, `pic_width_in_mbs_minus1` and
	// `pic_height_in_map_units_minus1`. The order is the one `SPSSize` in
	// h264.go already walks — that reader knows the flag and **compensates**
	// with it, doubling the height, which is the right answer where a size can
	// be recovered and the wrong one here.
	//
	// It has never fired: the encoder is told progressive in three places, in
	// mf/encoder_windows.go and mf/source_windows.go. That is not the argument
	// — it is the same argument the B-slice refusal three functions down already
	// rejects, because a profile, like an interlace mode, is a request and this
	// file is a monument to requests accepted and not executed.
	s.ue()  // max_num_ref_frames
	s.bit() // gaps_in_frame_num_value_allowed_flag
	s.ue()  // pic_width_in_mbs_minus1
	s.ue()  // pic_height_in_map_units_minus1
	frameMBsOnly := s.bit()
	// **The exhaustion is asked about first.** `bit` answers 0 and sets `err`
	// when the buffer runs out, so a truncated SPS — or one whose Golomb code
	// overruns — reaches the test below looking exactly like an interlaced
	// stream. Both refuse, so the quantiser is safe either way; what is not safe
	// is the sentence, which would send whoever reads the log looking for an
	// interlaced encoder on a stream that is progressive. In a parser whose
	// contract is "a zero means I do not know", a plausible wrong cause is the
	// one thing it must not produce.
	if s.err != nil {
		return out, fmt.Errorf("h264: unreadable SPS")
	}
	if frameMBsOnly == 0 {
		return out, fmt.Errorf("h264: interlaced fields, the slice header is not the expected one")
	}

	p := &bitReader{b: rbsp(pps.Data[1:])}
	out.ID = p.ue()
	p.ue() // seq_parameter_set_id
	if p.bit() == 1 {
		return out, fmt.Errorf("h264: CABAC on, the slice header is not the expected one")
	}
	out.BottomFieldPicOrderInFrame = p.bit() == 1
	if n := p.ue(); n != 0 { // num_slice_groups_minus1
		return out, fmt.Errorf("h264: %d slice groups, not supported", n+1)
	}
	p.ue() // num_ref_idx_l0_default_active_minus1
	p.ue() // num_ref_idx_l1_default_active_minus1
	if p.bit() == 1 {
		return out, fmt.Errorf("h264: weighted prediction on, not supported")
	}
	if n := p.bits(2); n != 0 { // weighted_bipred_idc
		return out, fmt.Errorf("h264: weighted bipredictive prediction (%d), not supported", n)
	}
	out.InitQPMinus26 = p.se()
	p.se() // pic_init_qs_minus26
	p.se() // chroma_qp_index_offset
	p.bit()
	p.bit()
	out.RedundantPicCntPresent = p.bit() == 1
	if p.err != nil {
		return out, fmt.Errorf("h264: unreadable PPS")
	}
	if q := 26 + out.InitQPMinus26; q < 0 || q > 51 {
		return out, fmt.Errorf("h264: pic_init_qp out of range (%d)", q)
	}
	return out, nil
}

// QPReader derives the quantiser from the access units as they go past.
//
// It keeps the SPS and PPS because they arrive **before** the slices that cite
// them — with a two-second GOP, once every two seconds — while the quantiser is
// needed on every frame. It copies the bytes rather than holding on to them:
// the buffer they come from is the encoder's and gets reused straight after.
//
// It reparses the parameters **when they really change**, comparing the bytes.
// The resolution scale rebuilds the encoder and a new SPS arrives from there;
// without the comparison it would reparse on every keyframe, and without
// reparsing it would read the new stream with the old parameters.
type QPReader struct {
	sps, pps []byte
	info     ppsInfo
	ready    bool
	// fromStream says the parameters in hand came out of the stream. Once that
	// is true a seed can no longer replace them: **the stream is what the
	// decoder reads**, and where the two disagree it is the one that is right.
	// Measured on Quick Sync, and they do disagree — see Seed.
	fromStream bool
	// why explains what stops it reading, for whoever is looking at the
	// diagnosis: "I do not know" is information only if it is known about what.
	why string
}

// Seed hands the reader the parameter sets from outside the stream: the blob
// MF_MT_MPEG_SEQUENCE_HEADER carries, which the documentation defines as the
// SPS and PPS in Annex-B form with their start codes.
//
// **In-band parameter sets are not guaranteed.** On an encoder that does not
// emit them this reader would stay mute for the whole session, and with no
// quantiser the bitrate loop sits at the cap: the saving is lost, the picture
// never, and nothing anywhere says why.
//
// **It is not a dormant branch, and that is deliberate.** The seed is handed
// over at every build of the encoder — that is, at every step of the scale —
// so on the machines where the stream does carry the sets, this road is walked
// every time and its result is checked against them by the very next
// keyframe: adopt compares the bytes, so an in-band set that differs replaces
// the seeded one, and one that agrees costs nothing. A reserve nobody executes
// is the least tested part of a program.
func (q *QPReader) Seed(header []byte) {
	var sps, pps NAL
	IterateAnnexB(header, func(n NAL) bool {
		switch n.Type {
		case NALTypeSPS:
			sps = n
		case NALTypePPS:
			pps = n
		}
		return true
	})
	if sps.Data == nil || pps.Data == nil {
		return
	}
	// **A seed never replaces what the stream said.** Measured on Quick Sync,
	// the declared header and the in-band sets differ in exactly two bytes of
	// thirty-five — the constraint flags and the level, `42e01f` in the stream
	// against `424020` declared. For reading the quantiser the difference is
	// nothing, both being profile 66 and identical everywhere else; for anything
	// that announces a profile it is everything, since the SDP is refused
	// without the `0xE0` flags. The rule is cheap and it removes the question.
	if q.fromStream {
		return
	}
	q.adopt(sps, pps, false)
}

// adopt takes a parameter set pair, from wherever it came, and reparses **only
// if the bytes really changed**: the scale rebuilds the encoder and a new SPS
// arrives from there, while without the comparison this would reparse at every
// keyframe. It copies the bytes because the buffer they come from is the
// encoder's and gets reused straight after.
func (q *QPReader) adopt(sps, pps NAL, fromStream bool) {
	if equal(q.sps, sps.Data) && equal(q.pps, pps.Data) {
		q.fromStream = q.fromStream || fromStream
		return
	}
	q.fromStream = fromStream
	q.sps = append(q.sps[:0], sps.Data...)
	q.pps = append(q.pps[:0], pps.Data...)
	info, err := ParsePPS(NAL{Type: NALTypeSPS, Data: q.sps}, NAL{Type: NALTypePPS, Data: q.pps})
	q.info, q.ready = info, err == nil
	if err != nil {
		q.why = err.Error()
	} else {
		q.why = ""
	}
}

// Feed passes an access unit in and returns the quantiser of its first slice.
// The second value is false until an SPS and a PPS have been seen, and on a
// stream we do not know how to interpret it stays false forever.
func (q *QPReader) Feed(au []byte) (qp int, ok bool) {
	var sps, pps, slice NAL
	IterateAnnexB(au, func(n NAL) bool {
		switch {
		case n.Type == NALTypeSPS:
			sps = n
		case n.Type == NALTypePPS:
			pps = n
		case n.IsVCL() && slice.Data == nil:
			slice = n
		}
		return true
	})

	if sps.Data != nil && pps.Data != nil {
		q.adopt(sps, pps, true)
	}

	if !q.ready || slice.Data == nil {
		return 0, false
	}
	return QPSlice(q.info, slice)
}

func equal(a, b []byte) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
