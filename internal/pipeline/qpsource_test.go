package pipeline

import (
	"sync/atomic"
	"testing"
)

// A reading that has not changed yet is not a measurement: it is the case of the
// slice quantiser on Quick Sync, 26 on every frame. Were it to enter the
// statistics, the quality loop would conclude "quality exceeds" on every turn and
// go down to the floor for ever.
func TestAConstantReadingDoesNotCount(t *testing.T) {
	var p Pipeline
	for range 500 {
		p.qpMeasured(26)
	}
	if qp := p.QP(); qp.Known {
		t.Fatalf("a reading stuck at 26 over 500 frames was taken for a measurement: %+v", qp)
	}
}

// As soon as it varies it is believed — and from then on repeated values count
// too, because there it is the subject standing still, not the instrument.
func TestAsSoonAsItVariesTheReadingCounts(t *testing.T) {
	var p Pipeline
	p.qpMeasured(23)
	p.qpMeasured(23)
	p.qpMeasured(24)
	for range 10 {
		p.qpMeasured(24)
	}
	qp := p.QP()
	if !qp.Known {
		t.Fatal("a reading that changed value is not believed")
	}
	if qp.Samples < 10 {
		t.Errorf("after the change every sample must count, instead there are %d", qp.Samples)
	}
}

// TestTheAttributeIsPreferredAndTheStreamJudgesIt: where both readings exist the
// attribute is the one used and the stream is what it is compared with; where
// the attribute is missing the stream is used and the absence is counted; where
// the stream is missing there is nothing to compare, so no divergence.
func TestTheAttributeIsPreferredAndTheStreamJudgesIt(t *testing.T) {
	var both Pipeline
	both.qpReadings(26, true, 30, true)
	if n, b, d := both.qpDivergences.Load(), both.qpAttrBias.Load(), both.qpAttrDeviation.Load(); n != 1 || b != 4 || d != 4 {
		t.Errorf("attribute 30, stream 26: divergences %d, bias %+d, deviation %d; want 1, +4, 4", n, b, d)
	}
	if ok, ko := both.qpAttrOK.Load(), both.qpAttrKO.Load(); ok != 1 || ko != 0 {
		t.Errorf("attribute 30, stream 26: attrOK %d, attrKO %d; want 1, 0", ok, ko)
	}
	if !both.qpFromAttr.Load() || both.qpUsedMax.Load() != 30 {
		t.Errorf("attribute 30, stream 26: the reading in use is %d (from attribute %v); want 30 from the attribute",
			both.qpUsedMax.Load(), both.qpFromAttr.Load())
	}

	var noAttr Pipeline
	noAttr.qpReadings(26, true, 0, false)
	if ok, ko := noAttr.qpAttrOK.Load(), noAttr.qpAttrKO.Load(); ok != 0 || ko != 1 {
		t.Errorf("no attribute: attrOK %d, attrKO %d; want 0, 1", ok, ko)
	}
	if noAttr.qpFromAttr.Load() || noAttr.qpUsedMax.Load() != 26 {
		t.Errorf("no attribute: the reading in use is %d (from attribute %v); want 26 from the stream",
			noAttr.qpUsedMax.Load(), noAttr.qpFromAttr.Load())
	}

	var noStream Pipeline
	noStream.qpReadings(0, false, 30, true)
	if n, ok, ko := noStream.qpDivergences.Load(), noStream.qpAttrOK.Load(), noStream.qpAttrKO.Load(); n != 0 || ok != 0 || ko != 0 {
		t.Errorf("no stream: divergences %d, attrOK %d, attrKO %d; want nothing counted", n, ok, ko)
	}
	if !noStream.qpFromAttr.Load() || noStream.qpUsedMax.Load() != 30 {
		t.Errorf("no stream: the reading in use is %d; want 30 from the attribute", noStream.qpUsedMax.Load())
	}
}

// TestWidenTakesTheFirstValueAsBothEnds: the zero in lo is "nothing seen yet",
// so the first value opens the range at both ends and the next ones stretch it
// only outward.
func TestWidenTakesTheFirstValueAsBothEnds(t *testing.T) {
	var lo, hi atomic.Int64
	for _, v := range []int64{30, 26, 42, 35} {
		widen(&lo, &hi, v)
	}
	if lo.Load() != 26 || hi.Load() != 42 {
		t.Errorf("range [%d, %d], want [26, 42]", lo.Load(), hi.Load())
	}
}
