package main

import "testing"

// The rows are real measurements, and the first two are the ones that made this
// function exist: on an NVIDIA RTX 4080 fed by a camera delivering half its
// nominal cadence the encoder produces about half of any bitrate asked of it, so
// a command to descend **overshoots** the target. The old criterion measured the
// distance from the target and failed both, printing "congestion control on this
// machine is a fiction" about an encoder that had just obeyed.
func TestABitrateCommandIsJudgedOnWhetherItWasFollowed(t *testing.T) {
	for _, c := range []struct {
		name                 string
		before, after, asked float64
		held, judgeable      bool
	}{
		{"NVIDIA, by command, overshoots the target", 1113, 444, 800, true, true},
		{"NVIDIA, by reconfiguration, the same", 1101, 571, 800, true, true},

		// Quick Sync accepts the command and does not execute it: the throughput
		// does not move, which is the case the check exists for.
		{"an encoder that does not follow", 2418, 2375, 1250, false, true},

		// Landing near the target is obedience too, and the old criterion did
		// get this one right — it is kept so the correction is not a swap of one
		// blind spot for another.
		{"an encoder that lands on the number", 2413, 829, 800, true, true},

		// Asking for more than is coming out takes nothing away, so the
		// throughput is still the scene's: no answer is possible.
		{"a request that constrains nothing", 114, 235, 309, false, false},
		{"a request equal to the throughput", 800, 700, 800, false, false},
	} {
		t.Run(c.name, func(t *testing.T) {
			held, judgeable := bitrateTookHold(c.before, c.after, c.asked)
			if judgeable != c.judgeable {
				t.Fatalf("judgeable = %v, want %v", judgeable, c.judgeable)
			}
			if judgeable && held != c.held {
				t.Errorf("held = %v, want %v", held, c.held)
			}
		})
	}
}

// Putting the defect back: the criterion the tool carried until an NVIDIA
// machine walked into it. It must disagree on the row that was measured there,
// otherwise this correction changed nothing.
func TestTheOldCriterionFailsAnEncoderThatObeyed(t *testing.T) {
	const before, after, asked = 1113.0, 444.0, 800.0
	oldHeld := abs(after-asked) < abs(before-asked)/2
	newHeld, _ := bitrateTookHold(before, after, asked)
	if oldHeld {
		t.Fatal("the old criterion passed: the fixture no longer reproduces the defect")
	}
	if !newHeld {
		t.Error("the new criterion fails an encoder that cut its throughput by 60%")
	}
}

func abs(f float64) float64 {
	if f < 0 {
		return -f
	}
	return f
}
