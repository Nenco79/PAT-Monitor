package media

import (
	"testing"
	"time"
)

// bitWriter is the slice-header writer these tests need, and it exists because
// there was nothing to read from: this parser had no test of its own. It writes
// the three codings bitReader reads.
type bitWriter struct {
	b []byte
	n int
}

func (w *bitWriter) bit(v int) {
	if w.n%8 == 0 {
		w.b = append(w.b, 0)
	}
	if v != 0 {
		w.b[w.n/8] |= 1 << (7 - w.n%8)
	}
	w.n++
}

func (w *bitWriter) bits(v, n int) {
	for i := n - 1; i >= 0; i-- {
		w.bit((v >> i) & 1)
	}
}

// ue writes the exponential-Golomb code bitReader.ue reads: for a value v the
// code is v+1 in binary, preceded by as many zeros as it has bits after the
// first.
func (w *bitWriter) ue(v int) {
	code := v + 1
	n := 0
	for c := code; c > 1; c >>= 1 {
		n++
	}
	for i := 0; i < n; i++ {
		w.bit(0)
	}
	w.bits(code, n+1)
}

func (w *bitWriter) se(v int) {
	if v > 0 {
		w.ue(2*v - 1)
		return
	}
	w.ue(-2 * v)
}

// The picture-order type is 2 and the slices are references but not IDRs, so
// the fields that depend on the rest of the SPS stay out of the way.
func testPPS() ppsInfo {
	return ppsInfo{ID: 0, InitQPMinus26: 0, Log2MaxFrameNum: 4, PicOrderCntType: 2}
}

// close writes rbsp_trailing_bits and puts the NAL header in front:
// nal_ref_idc 2, type 1, a non-IDR slice that others reference.
func (w *bitWriter) close() NAL {
	w.bit(1)
	for w.n%8 != 0 {
		w.bit(0)
	}
	return NAL{Type: NALTypeNonIDR, Data: append([]byte{0x41}, w.b...)}
}

func pSliceNAL(sliceType, qpDelta int) NAL {
	w := &bitWriter{}
	w.ue(0)         // first_mb_in_slice
	w.ue(sliceType) // slice_type
	w.ue(0)         // pic_parameter_set_id
	w.bits(3, 4)    // frame_num
	t := sliceType % 5
	if t == 0 || t == 3 { // P, SP
		w.bit(0) // num_ref_idx_active_override_flag
	}
	if t != 2 && t != 4 { // everything that has references
		w.bit(0) // ref_pic_list_modification_flag_l0
	}
	w.bit(0) // adaptive_ref_pic_marking_mode_flag
	w.se(qpDelta)
	return w.close()
}

// bSliceNAL writes a B slice header **to the specification**, with the four
// fields that really vary between encoders. Three of them QPSlice does not know
// about — direct_spatial_mv_pred_flag, num_ref_idx_l1_active_minus1 and the
// second reference-list modification — and that is the point.
func bSliceNAL(direct, frame, override, modify, qpDelta int) NAL {
	w := &bitWriter{}
	w.ue(0)
	w.ue(1) // slice_type: B
	w.ue(0)
	w.bits(frame, 4)
	w.bit(direct)
	w.bit(override)
	if override == 1 {
		w.ue(1) // num_ref_idx_l0_active_minus1
		w.ue(0) // num_ref_idx_l1_active_minus1
	}
	w.bit(modify) // ref_pic_list_modification_flag_l0
	if modify == 1 {
		w.ue(0)
		w.ue(2)
		w.ue(3) // end of list
	}
	w.bit(modify) // ref_pic_list_modification_flag_l1
	if modify == 1 {
		w.ue(3)
	}
	w.bit(0) // adaptive_ref_pic_marking_mode_flag
	w.se(qpDelta)
	return w.close()
}

// The parser reads what it says it reads. This is the control, and without it
// the refusal below could pass for working while the function was broken.
func TestTheQuantiserIsReadFromAPSlice(t *testing.T) {
	for _, want := range []int{20, 26, 30, 38, 51} {
		got, ok := QPSlice(testPPS(), pSliceNAL(0, want-26))
		if !ok {
			t.Fatalf("a P slice carrying qp %d was refused", want)
		}
		if got != want {
			t.Errorf("P slice: read %d, wanted %d", got, want)
		}
	}
}

// **Every B slice is refused, and the sweep is the argument for refusing.**
//
// The first version of this test asserted the refusal on one hand-written
// header and **passed with the refusal removed**, because that particular
// header runs off the end of the buffer when misread: it was a test written by
// the same mistake it should catch. So the shape is swept instead.
//
// Measured with the refusal removed: of 3328 headers, **896 come back as a
// legitimate quantiser** and every one of them is wrong — a slice carrying 24
// read as 48, one carrying 20 read as 26. The first would tell the loop the
// picture is broken, ten points past the break threshold, and take a resolution
// step away for nothing; the second is the quiet kind. The other 2432 are
// refused **by accident**, which is not a protection: it is the same misreading
// that happened to fall out of range.
func TestEveryBSliceIsRefused(t *testing.T) {
	read := 0
	for _, direct := range []int{0, 1} {
		for frame := 0; frame < 16; frame++ {
			for _, override := range []int{0, 1} {
				for _, modify := range []int{0, 1} {
					for qp := 20; qp <= 45; qp++ {
						if _, ok := QPSlice(testPPS(), bSliceNAL(direct, frame, override, modify, qp-26)); ok {
							read++
						}
					}
				}
			}
		}
	}
	if read != 0 {
		t.Errorf("%d B slice headers were read: the parser does not know three of "+
			"their fields, so what comes back is a plausible and wrong quantiser", read)
	}
	// slice_type repeats every five: 6 is a B slice as much as 1 is.
	if _, ok := QPSlice(testPPS(), pSliceNAL(6, 4)); ok {
		t.Error("slice_type 6 was read, and it is a B slice too")
	}
	// And the neighbours must still go through, or the refusal would be a way
	// of switching the reading off altogether.
	for _, st := range []int{0, 2, 5, 7} {
		if _, ok := QPSlice(testPPS(), pSliceNAL(st, 4)); !ok {
			t.Errorf("slice_type %d was refused, and it is not a B slice", st)
		}
	}
}

// spsNAL and ppsNAL build the smallest parameter sets ParsePPS accepts, with
// the values pSliceNAL assumes: frame numbers on four bits and picture order
// type 2, which is the one that puts nothing in the slice header.
//
// **It is a whole SPS and it used to stop halfway.** The fixture ended after
// `pic_order_cnt_type`, which is exactly as far as ParsePPS used to walk — a
// fixture shaped to the parser rather than to the format, so the day the parser
// had to read one field further there was nothing there to read. It now carries
// the rest of the mandatory fields, which also lets SPSSize, the package's other
// reader of this same structure, be pointed at the same bytes.
func spsNAL() NAL { return spsNALWith(1) }

// spsNALWith writes frame_mbs_only_flag as given, so the refusal can be
// exercised: with it at 0 the slice header carries field_pic_flag between
// frame_num and idr_pic_id, and every field after it shifts.
func spsNALWith(frameMBsOnly int) NAL {
	w := &bitWriter{}
	w.bits(66, 8)      // profile_idc: baseline
	w.bits(0xE01F, 16) // constraint flags 0xE0, level 3.1
	w.ue(0)            // seq_parameter_set_id
	w.ue(0)            // log2_max_frame_num_minus4
	w.ue(2)            // pic_order_cnt_type
	w.ue(1)            // max_num_ref_frames
	w.bit(0)           // gaps_in_frame_num_value_allowed_flag
	w.ue(79)           // pic_width_in_mbs_minus1: 80 macroblocks, 1280
	w.ue(44)           // pic_height_in_map_units_minus1: 45, 720
	w.bit(frameMBsOnly)
	if frameMBsOnly == 0 {
		w.bit(0) // mb_adaptive_frame_field_flag
	}
	w.bit(1) // direct_8x8_inference_flag
	w.bit(0) // frame_cropping_flag
	w.bit(0) // vui_parameters_present_flag
	w.bit(1) // rbsp_stop_one_bit
	for w.n%8 != 0 {
		w.bit(0)
	}
	return NAL{Type: NALTypeSPS, Data: append([]byte{0x67}, w.b...)}
}

// **The refusal two comments promised and nobody had written.**
//
// With frame_mbs_only_flag at 0 the slice header carries field_pic_flag, and
// possibly bottom_field_flag, between frame_num and idr_pic_id — so
// slice_qp_delta is read one or two bits out of place and what comes back is a
// plausible and wrong quantiser, which is the number that commands the
// resolution scale. It is the forgotten num_ref_idx_active_override_flag and the
// unrefused B slice a third time.
//
// It has never fired on any machine here, because the encoder is told
// progressive in three places — and that is the argument this package already
// rejects one function down, where B slices are refused despite Baseline being
// asked for: a request is not a guarantee.
func TestAnInterlacedStreamIsRefused(t *testing.T) {
	if _, err := ParsePPS(spsNALWith(0), ppsNAL(0)); err == nil {
		t.Error("an interlaced SPS was accepted: every slice header after " +
			"frame_num is then read shifted, and the quantiser comes back plausible " +
			"and wrong")
	}
	// And the progressive one still goes through, or the refusal would be a way
	// of switching the reading off altogether.
	if _, err := ParsePPS(spsNALWith(1), ppsNAL(0)); err != nil {
		t.Errorf("a progressive SPS was refused: %v", err)
	}
}

// **And the package's two readers of the SPS agree about the same bytes.** One
// walks as far as the quantiser needs and the other walks the whole thing for
// the size; they are the "two lists of one thing" family, and until now nothing
// pointed them at the same fixture. What this catches is a field read in one
// order here and another there — which is exactly how the missing
// frame_mbs_only_flag survived.
func TestBothSPSReadersAgreeOnTheSameBytes(t *testing.T) {
	sps := spsNAL()
	w, h, err := SPSSize(sps.Data)
	if err != nil {
		t.Fatalf("the size reader refuses the fixture the quantiser reader accepts: %v", err)
	}
	if w != 1280 || h != 720 {
		t.Errorf("the size reader says %dx%d, the fixture declares 1280x720", w, h)
	}
	if _, err := ParsePPS(sps, ppsNAL(0)); err != nil {
		t.Errorf("the quantiser reader refuses the fixture the size reader accepts: %v", err)
	}
}

func ppsNAL(initQPMinus26 int) NAL {
	w := &bitWriter{}
	w.ue(0)      // pic_parameter_set_id
	w.ue(0)      // seq_parameter_set_id
	w.bit(0)     // entropy_coding_mode_flag: CAVLC
	w.bit(0)     // bottom_field_pic_order_in_frame_present_flag
	w.ue(0)      // num_slice_groups_minus1
	w.ue(0)      // num_ref_idx_l0_default_active_minus1
	w.ue(0)      // num_ref_idx_l1_default_active_minus1
	w.bit(0)     // weighted_pred_flag
	w.bits(0, 2) // weighted_bipred_idc
	w.se(initQPMinus26)
	w.se(0)  // pic_init_qs_minus26
	w.se(0)  // chroma_qp_index_offset
	w.bit(0) // deblocking_filter_control_present_flag
	w.bit(0) // constrained_intra_pred_flag
	w.bit(0) // redundant_pic_cnt_present_flag
	w.bit(1)
	for w.n%8 != 0 {
		w.bit(0)
	}
	return NAL{Type: NALTypePPS, Data: append([]byte{0x68}, w.b...)}
}

// annexB concatenates NALs the way both the stream and the sequence-header blob
// carry them: four-byte start codes.
func annexB(nals ...NAL) []byte {
	var out []byte
	for _, n := range nals {
		out = append(out, 0, 0, 0, 1)
		out = append(out, n.Data...)
	}
	return out
}

// **Without parameter sets the reader is mute, and the seed is what breaks the
// silence.** On an encoder that does not put them in the stream there is
// nothing else: no quantiser means the bitrate loop sits at the cap for the
// whole session, which costs the saving and says so nowhere.
func TestASeededReaderReadsAStreamWithNoParameterSets(t *testing.T) {
	slice := annexB(pSliceNAL(0, 4)) // carries qp 26 + initQP + 4

	mute := &QPReader{}
	if _, ok := mute.Feed(slice); ok {
		t.Fatal("a stream with no SPS or PPS was read: the reader has nothing to read it with")
	}

	seeded := &QPReader{}
	seeded.Seed(annexB(spsNAL(), ppsNAL(0)))
	got, ok := seeded.Feed(slice)
	if !ok {
		t.Fatal("the seeded reader still cannot read the stream")
	}
	if got != 30 {
		t.Errorf("read %d, wanted 30", got)
	}
}

// **The stream stays authoritative.** The seed is what the encoder declares;
// what the decoder actually reads is the stream, and where they differ the
// stream is right. The reader compares the bytes, so this costs nothing on the
// machines where the two agree — which is every one seen here.
func TestInBandParameterSetsReplaceASeedThatDisagrees(t *testing.T) {
	q := &QPReader{}
	// A seed claiming the picture quantiser starts eight points higher.
	q.Seed(annexB(spsNAL(), ppsNAL(8)))
	if got, ok := q.Feed(annexB(pSliceNAL(0, 0))); !ok || got != 34 {
		t.Fatalf("with the seed alone: read %d ok=%v, wanted 34", got, ok)
	}
	// The same slice, now preceded by the real sets.
	got, ok := q.Feed(annexB(spsNAL(), ppsNAL(0), pSliceNAL(0, 0)))
	if !ok {
		t.Fatal("the stream carrying its own parameter sets was refused")
	}
	if got != 26 {
		t.Errorf("read %d, wanted 26: the in-band sets did not replace the seed", got)
	}
}

// A seed that carries no parameter sets, or nothing at all, must leave the
// reader as it was: an encoder that declares nothing is the normal case for
// this call, not a fault.
func TestAnEmptySeedChangesNothing(t *testing.T) {
	q := &QPReader{}
	q.Seed(nil)
	q.Seed([]byte{0, 0, 0, 1, 0x09, 0x10}) // an access unit delimiter, no sets
	if _, ok := q.Feed(annexB(pSliceNAL(0, 4))); ok {
		t.Error("an empty seed made the reader believe it had parameter sets")
	}
	// And it must still accept a real one afterwards.
	q.Seed(annexB(spsNAL(), ppsNAL(0)))
	if _, ok := q.Feed(annexB(pSliceNAL(0, 4))); !ok {
		t.Error("after an empty seed a real one no longer takes")
	}
}

// **Once the stream has spoken, a seed cannot take it back.** The reserve is
// offered on every frame, so without this rule the declared sets would replace
// the real ones a few dozen times a second — and on Quick Sync the two are not
// the same bytes: `42e01f` in the stream against `424020` declared, the
// constraint flags and the level.
func TestASeedCannotOverrideWhatTheStreamSaid(t *testing.T) {
	q := &QPReader{}
	if got, ok := q.Feed(annexB(spsNAL(), ppsNAL(0), pSliceNAL(0, 0))); !ok || got != 26 {
		t.Fatalf("from the stream: read %d ok=%v, wanted 26", got, ok)
	}
	// The encoder declares something else, over and over, as the pipeline does.
	for i := 0; i < 5; i++ {
		q.Seed(annexB(spsNAL(), ppsNAL(8)))
	}
	got, ok := q.Feed(annexB(pSliceNAL(0, 0)))
	if !ok {
		t.Fatal("the reader stopped reading after being seeded")
	}
	if got != 26 {
		t.Errorf("read %d, wanted 26: the seed replaced what the stream said", got)
	}
}

// **The real bytes, from the real encoder.**
//
// These two are what Quick Sync produced on the development laptop: the SPS and
// PPS it puts in the stream, and the blob it declares in
// MF_MT_MPEG_SEQUENCE_HEADER once the first frame is out. They are here because
// on a machine whose encoder does emit the sets in the stream the reserve is
// never actually taken, and a reserve nobody exercises is the least tested part
// of a program. Fed by hand, it is exercised.
//
// They also carry the emulation-prevention bytes a synthetic set does not:
// three `000003` sequences that rbsp has to strip.
func TestTheDeclaredHeaderOfARealEncoderIsAsGoodForReading(t *testing.T) {
	unhex := func(s string) []byte {
		out := make([]byte, len(s)/2)
		for i := range out {
			var v int
			for j := 0; j < 2; j++ {
				c := s[i*2+j]
				switch {
				case c >= '0' && c <= '9':
					v = v<<4 | int(c-'0')
				case c >= 'a' && c <= 'f':
					v = v<<4 | int(c-'a'+10)
				default:
					t.Fatalf("bad hex %q", s)
				}
			}
			out[i] = byte(v)
		}
		return out
	}

	inSPS := unhex("2742e01f95b014016ec044000003000400000300f3a100098900002625a6f7be0ed0e197")
	inPPS := unhex("28ce3c80")
	declared := unhex("0000012742402095b014016ec044000003000400000300f3a1000989000026" +
		"25a6f7be0ed0e1970000000128ce3c8000")

	fromStream, err := ParsePPS(NAL{Type: NALTypeSPS, Data: inSPS}, NAL{Type: NALTypePPS, Data: inPPS})
	if err != nil {
		t.Fatalf("the real in-band sets do not parse: %v", err)
	}

	q := &QPReader{}
	q.Seed(declared)
	if !q.ready {
		t.Fatal("the declared header did not make the reader ready: " + q.why)
	}
	if q.info != fromStream {
		t.Errorf("the declared header parses to %+v, the stream to %+v: for reading the "+
			"quantiser the two must be the same", q.info, fromStream)
	}

	// And the difference that does exist is the one measured: two bytes of the
	// SPS, the constraint flags and the level. It is nothing for this parser and
	// everything for anything that announces a profile — the SDP is refused
	// without the 0xE0 flags — so it is nailed down here rather than remembered.
	if inSPS[1] != declared[4] {
		t.Errorf("profile_idc differs: stream %d, declared %d", inSPS[1], declared[4])
	}
	if inSPS[2] == declared[5] && inSPS[3] == declared[6] {
		t.Error("the constraint flags and the level now agree: the measurement this " +
			"test carries is out of date, and the comment above it with it")
	}
}

// markedSliceNAL writes a P slice whose dec_ref_pic_marking carries the memory
// management operations given, each with the arguments 7.3.3.3 gives it — and
// **operation 5 with none**, which is the shape this fixture exists for.
func markedSliceNAL(ops []int, qpDelta int) NAL {
	w := &bitWriter{}
	w.ue(0)      // first_mb_in_slice
	w.ue(0)      // slice_type: P
	w.ue(0)      // pic_parameter_set_id
	w.bits(3, 4) // frame_num
	w.bit(0)     // num_ref_idx_active_override_flag
	w.bit(0)     // ref_pic_list_modification_flag_l0
	w.bit(1)     // adaptive_ref_pic_marking_mode_flag
	for _, op := range ops {
		w.ue(op)
		switch op {
		case 1:
			w.ue(7) // difference_of_pic_nums_minus1
		case 2:
			w.ue(3) // long_term_pic_num
		case 3:
			w.ue(7) // difference_of_pic_nums_minus1
			w.ue(1) // long_term_frame_idx
		case 4:
			w.ue(2) // max_long_term_frame_idx_plus1
		case 5:
			// Nothing. MMCO 5 marks everything unused for reference and takes
			// no argument, and a reader that takes one for it eats the next
			// field instead.
		case 6:
			w.ue(1) // long_term_frame_idx
		}
	}
	w.ue(0) // memory_management_control_operation: end of the list
	w.se(qpDelta)
	return w.close()
}

// **The marking operations are read from the table, and 5 has no argument.**
//
// The parser used to read a ue(v) for MMCO 5 alongside MMCO 4, and from there
// every bit of the header shifted: the terminator of the marking list was eaten
// as an argument, the loop went on reading slice_qp_delta as though it were
// another operation, and the quantiser that came out — where one came out at all
// — was the plausible and wrong number this file's whole contract is written
// against.
//
// **The defect was put back and this test fails with it**: with `op == 5`
// restored beside `op == 4` the single-operation case is refused outright, and
// the combinations that end in a 5 come back with a quantiser that is not the
// one written.
//
// The sweep is over shapes rather than one header, because the first version of
// the B-slice test in this file passed with its own refusal removed: a marking
// list with one operation in it is the case least likely to notice a shift.
func TestTheMarkingOperationsAreReadToTheTable(t *testing.T) {
	lists := [][]int{
		{1}, {2}, {3}, {4}, {5}, {6},
		{5, 1}, {1, 5}, {4, 5}, {5, 4}, {3, 5, 6}, {5, 5},
		{1, 2, 3, 4, 5, 6},
	}
	for _, ops := range lists {
		for _, want := range []int{20, 26, 30, 38, 51} {
			got, ok := QPSlice(testPPS(), markedSliceNAL(ops, want-26))
			if !ok {
				t.Errorf("marking %v, qp %d: the slice was refused", ops, want)
				continue
			}
			if got != want {
				t.Errorf("marking %v: read %d, wanted %d", ops, got, want)
			}
		}
	}
}

// A marking code the table does not define means the offset is already lost, so
// the read ends there rather than skipping an argument it cannot know the size
// of. Same for a list that never terminates: sixty-four entries in, the answer
// is that this is not a header we are reading.
func TestAMarkingCodeThatDoesNotExistIsRefused(t *testing.T) {
	w := &bitWriter{}
	w.ue(0)
	w.ue(0) // P
	w.ue(0)
	w.bits(3, 4)
	w.bit(0)
	w.bit(0)
	w.bit(1) // adaptive_ref_pic_marking_mode_flag
	w.ue(7)  // no such operation
	w.ue(0)
	w.se(4)
	if _, ok := QPSlice(testPPS(), w.close()); ok {
		t.Error("a marking code outside 0..6 was accepted: everything after it is " +
			"read at an offset nobody knows")
	}

	n := &bitWriter{}
	n.ue(0)
	n.ue(0)
	n.ue(0)
	n.bits(3, 4)
	n.bit(0)
	n.bit(0)
	n.bit(1)
	for i := 0; i < 80; i++ {
		n.ue(5) // never terminates
	}
	n.se(4)
	if _, ok := QPSlice(testPPS(), n.close()); ok {
		t.Error("a marking list with no terminator was accepted")
	}
}

// **A bit count out of range is refused, because it is a length and not a
// value.**
//
// log2_max_frame_num_minus4 and log2_max_pic_order_cnt_lsb_minus4 are handed
// straight to bits() by QPSlice, once per frame. The specification puts both in
// 0 to 12; an exp-Golomb value reaches about 4.29e9, and the difference between
// those two numbers is the difference between reading a header and stopping the
// frame loop.
func TestAnAbsurdBitCountInTheSPSIsRefused(t *testing.T) {
	sps := func(frameNumMinus4, pocType, pocLsbMinus4 int) NAL {
		w := &bitWriter{}
		w.bits(66, 8)
		w.bits(0xE01F, 16)
		w.ue(0) // seq_parameter_set_id
		w.ue(frameNumMinus4)
		w.ue(pocType)
		if pocType == 0 {
			w.ue(pocLsbMinus4)
		}
		w.ue(1)
		w.bit(0)
		w.ue(79)
		w.ue(44)
		w.bit(1) // frame_mbs_only_flag
		w.bit(1) // direct_8x8_inference_flag
		w.bit(0) // frame_cropping_flag
		w.bit(0) // vui_parameters_present_flag
		w.bit(1)
		for w.n%8 != 0 {
			w.bit(0)
		}
		return NAL{Type: NALTypeSPS, Data: append([]byte{0x67}, w.b...)}
	}

	if _, err := ParsePPS(sps(13, 2, 0), ppsNAL(0)); err == nil {
		t.Error("log2_max_frame_num_minus4 = 13 was accepted")
	}
	if _, err := ParsePPS(sps(1_000_000, 2, 0), ppsNAL(0)); err == nil {
		t.Error("a frame_num of a million bits was accepted: QPSlice then asks " +
			"bits() for a million bits on every frame")
	}
	if _, err := ParsePPS(sps(0, 0, 13), ppsNAL(0)); err == nil {
		t.Error("log2_max_pic_order_cnt_lsb_minus4 = 13 was accepted")
	}
	// And the legal end of the range still goes through, or the refusal would
	// be a way of switching the reading off.
	if _, err := ParsePPS(sps(12, 0, 12), ppsNAL(0)); err != nil {
		t.Errorf("the top of the legal range was refused: %v", err)
	}
}

// **A reader with nothing left stops.**
//
// bits() ran to n whatever happened, which is harmless while n is a literal and
// is not harmless where n comes out of the stream: Log2MaxFrameNum is an
// exp-Golomb value plus four, so the worst case ParsePPS could hand over is
// **4 294 967 298**, once a frame. The defect was put back and this test fails
// with it, in **8.3 seconds** against the bound of one.
//
// **The stopwatch is the instrument here, and that is not a free choice.** The
// work the repaired form avoids is a comparison and an increment, so there is
// nothing to count; the bound is therefore set two orders of magnitude above
// what the repaired call costs rather than beside it, which is what keeps it
// from failing on a busy machine.
//
// **And there is no second assertion, though there was one for a while.** `bit`
// builds its error afresh at every call past the end, so the old loop
// allocated once per bit — a million of them at n = 1<<20, measured. A guard
// was written for that and **passed with the defect back in**, because once the
// loop stops there is no second call to make: the two defects are one, and a
// test that cannot fail is the kind this repository throws away rather than
// keeps for the green.
func TestBitsStopsWhenThereIsNothingLeftToRead(t *testing.T) {
	start := time.Now()
	r := &bitReader{b: []byte{0x80}}
	r.bits(1 << 32)
	if d := time.Since(start); d > time.Second {
		t.Errorf("%v to read four billion bits that are not there: the loop is "+
			"still running to the end, and this happens once a frame", d)
	}
	if r.err == nil {
		t.Error("the reader ran off the end and says nothing")
	}
}
