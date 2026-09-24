package pipeline

import "testing"

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
