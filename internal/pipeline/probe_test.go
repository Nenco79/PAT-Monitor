package pipeline

import "testing"

// **The probe is an experiment, and an experiment only counts where the quantity
// is observable.**
//
// It halves the preset and weighs the drop, so it can only answer where the new
// request sits **below** what was already coming out — otherwise nothing was
// taken away and the scene decides the throughput. On a still scene that happens
// easily, because in CBR the encoder under-produces, and a negative verdict puts
// **the whole process** on the road that rebuilds the encoder: the one that on
// AMD has already interrupted the capture.
//
// It is the rule `bitrateSeen` has always had — either the new cap cuts what was
// coming out, or it is not a cap — in the second of the two places that need it.
// See TestAnEncoderThatIsNotBoundByTheRequestIsNotJudged, which is the same test
// from the other side.
func TestTheProbeDoesNotJudgeWhatItDidNotConstrain(t *testing.T) {
	cases := []struct {
		name                string
		start, asked, after int
		took, judgeable     bool
	}{
		{
			// The real sequence from a test machine: 1250 asked while 597 were
			// coming out. The drop to 228 was produced by the scene, not by the
			// order, and the probe concluded "the command takes" — with a still
			// scene it would have concluded the opposite, and for good.
			name: "the request sat above the throughput", start: 597, asked: 1250, after: 228,
			took: false, judgeable: false,
		},
		{
			// Quick Sync, the measurement the probe was born from: 1250 asked,
			// throughput from 2520 to 2512. Here the request did constrain, and
			// the verdict is the right one.
			name: "the encoder does not follow", start: 2520, asked: 1250, after: 2512,
			took: false, judgeable: true,
		},
		{
			name: "the encoder follows", start: 2500, asked: 1250, after: 1300,
			took: true, judgeable: true,
		},
		{
			// The expected drop is `start - asked`, and a fifth of it is enough:
			// with a starting throughput well above the request, a modest drop
			// is not an answer.
			name: "too small a drop is not enough", start: 2500, asked: 1250, after: 2400,
			took: false, judgeable: true,
		},
		{
			name: "without a measurement nothing is judged", start: 0, asked: 1250, after: 100,
			took: false, judgeable: false,
		},
		{
			name: "the measurement was interrupted", start: 2500, asked: 1250, after: -1,
			took: false, judgeable: false,
		},
	}

	for _, c := range cases {
		took, judgeable := probeVerdict(c.start, c.asked, c.after)
		if took != c.took || judgeable != c.judgeable {
			t.Errorf("%s (before %d, asked %d, after %d): took=%v judgeable=%v, "+
				"want %v and %v", c.name, c.start, c.asked, c.after,
				took, judgeable, c.took, c.judgeable)
		}
	}
}

// **The probe does not put the preset back on a link somebody has measured in
// the meantime.**
//
// It measures for eight seconds from the start of the capture, and in that time
// a viewer can have connected: from then on the loop is in command. Putting
// `base` back would mean sending the preset onto a link just measured at 600
// kbit/s — and it does not correct itself, because the loop only commands **when
// its own state changes**: it would believe it sits at 600 while the encoder sits
// at the top.
func TestTheProbeRestoresOnlyWhatItChanged(t *testing.T) {
	// Nobody else has commanded: what is in force is still the probe's own
	// request, and what was there before goes back.
	if kbps, restore := probeRestore(1250, 1250, 2500); !restore || kbps != 2500 {
		t.Errorf("ordinary restore: %d, %v", kbps, restore)
	}

	// The loop commanded during the measurement: the bitrate in force is its.
	if kbps, restore := probeRestore(600, 1250, 2500); restore {
		t.Errorf("it put %d back over somebody else's command", kbps)
	}

	// And what goes back is the value in force at the start, not the preset:
	// they are the same number only if nobody had commanded yet.
	if kbps, restore := probeRestore(1250, 1250, 900); !restore || kbps != 900 {
		t.Errorf("it put %d back instead of the 900 that were in force", kbps)
	}
}
