package audio

import "testing"

// TestSensitivityGain checks that sensitivity always ends up at the endpoint's
// maximum, by both routes into the device.
//
// This is the part that cannot be measured without moving the Windows slider,
// and moving it to run a test would mean changing the PC of whoever uses the
// program.
func TestSensitivityGain(t *testing.T) {
	cases := []struct {
		name           string
		current, max   float64
		alreadyApplied bool
		want           float64
	}{
		{"exclusive, slider at maximum", 30, 30, false, 30},
		{"exclusive, slider halfway", 10, 30, false, 30},
		{"exclusive, slider at zero", 0, 30, false, 30},
		{"shared, slider at maximum", 30, 30, true, 0},
		{"shared, slider halfway", 10, 30, true, 20},
		{"shared, slider below zero", -20, 30, true, 50},
		// An endpoint with no gain available must not produce anything odd: a
		// maximum of zero means there is nothing to recover.
		{"endpoint with no scale", 0, 0, true, 0},
		{"endpoint with no scale, exclusive", 0, 0, false, 0},
		// A slider past the declared maximum must not attenuate: that would take
		// away signal the user asked for.
		{"slider past the maximum", 40, 30, true, 0},
	}

	for _, c := range cases {
		if got := sensitivityGain(c.current, c.max, c.alreadyApplied); got != c.want {
			t.Errorf("%s: sensitivityGain(%.0f, %.0f, %v) = %.0f, want %.0f",
				c.name, c.current, c.max, c.alreadyApplied, got, c.want)
		}
	}
}
