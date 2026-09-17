package pipeline

import (
	"bytes"
	"log/slog"
	"strings"
	"testing"
	"time"
)

// watched prepares a pipeline whose log can be read back.
func watched(t *testing.T) (*Pipeline, *bytes.Buffer) {
	t.Helper()
	var out bytes.Buffer
	p := New(Config{Log: slog.New(slog.NewTextHandler(&out, nil))})
	return p, &out
}

// bytesFor returns how many bytes kbps kbit/s are worth over a window of d.
func bytesFor(kbps int, d time.Duration) int {
	return int(float64(kbps) * 1000 / 8 * d.Seconds())
}

// window ages the measurement and hands over the period's bytes in one go.
func window(p *Pipeline, asked, produced int) {
	const d = 5 * time.Second
	p.bitrateAsked(asked)
	p.brMu.Lock()
	p.brSince = time.Now().Add(-d)
	p.brMu.Unlock()
	p.bitrateSeen(bytesFor(produced, d))
}

func deaf(p *Pipeline, out *bytes.Buffer) bool {
	return p.BitrateNeedsReconfig() || strings.Contains(out.String(), "does not apply it")
}

// TestAWideCalibrationIsNotDeafness is the real sequence of the AMD encoder,
// which produces about 40% more than it is asked for while **following** every
// command: halve the request and the throughput halves.
//
// That chip's gain is worth 1.3-1.5, which falls right on top of the two ratio
// thresholds (1.4 and 1.15): no fixed value can separate it from deafness, and
// that is why the criterion now looks at whether the throughput **follows**.
func TestAWideCalibrationIsNotDeafness(t *testing.T) {
	p, out := watched(t)

	asked := []int{1024, 926, 748, 814, 974, 1072, 1204, 1083}
	produced := []int{1535, 1167, 943, 968, 1378, 1516, 1703, 1622}
	for i := range asked {
		window(p, asked[i], produced[i])
	}

	if deaf(p, out) {
		t.Errorf("an encoder that follows with a wide calibration was taken for deaf:\n%s", out.String())
	}
}

// But the real stall is caught: it is the observed case where the target slid
// away and the throughput stayed nailed down. There the signature is not "it
// sits above", it is **it does not follow**.
func TestAThroughputThatDoesNotFollowIsCaught(t *testing.T) {
	p, out := watched(t)

	// The descent is the real one of the quality loop, which takes about a
	// fifth away each time: from a start of 1988 it gets below half in four
	// steps, that is twenty seconds or so. The watch concludes when the proof
	// is there, not before — that is the price of not accusing whoever obeys.
	for _, asked := range []int{1988, 1570, 1240, 980, 774, 611} {
		window(p, asked, 2400) // the encoder does not move by one kbit
	}

	if !deaf(p, out) {
		t.Errorf("the request more than halved and the throughput did not move, "+
			"and nobody said anything:\n%s", out.String())
	}
}

// And a blatantly deaf encoder is caught at once, with no need for an extra
// rule: it ignores everything and stays where it was while the request collapses.
func TestABlatantlyDeafEncoderIsCaughtAtOnce(t *testing.T) {
	p, out := watched(t)

	window(p, 2500, 2400)
	window(p, 800, 2400) // here the request in force is still 2500
	window(p, 800, 2400)
	window(p, 800, 2400)

	if !deaf(p, out) {
		t.Errorf("2400 produced against 800 asked, repeatedly, and no warning:\n%s", out.String())
	}
}

// Without ever having asked for appreciably less there is no proof, and nobody
// is accused: an encoder calibrated wide and a deaf one are indistinguishable
// until the request moves. A zero means "I do not know", never "zero".
func TestWithoutAskingForLessNobodyIsAccused(t *testing.T) {
	p, out := watched(t)

	for i := 0; i < 10; i++ {
		window(p, 1000, 1500) // always 50% more, but we never asked for less
	}

	if deaf(p, out) {
		t.Errorf("accused without proof: the request never went down\n%s", out.String())
	}
}

// A monotonic descent must not produce false verdicts even when the window's
// bytes were produced by the previous request: that is the defect that would
// condemn an obedient encoder on every descent of the quality loop.
func TestADescentDoesNotMakeAnObedientEncoderLookDeaf(t *testing.T) {
	p, out := watched(t)

	// The throughput follows one turn behind, as it really does.
	asked := []int{2500, 1650, 1062, 721, 551}
	produced := []int{2472, 2400, 1591, 1081, 826}
	for i := range asked {
		window(p, asked[i], produced[i])
	}

	if deaf(p, out) {
		t.Errorf("an encoder that followed one turn behind was declared deaf:\n%s", out.String())
	}
}

// TestMeasurementNoiseDoesNotAccuse is the real sequence read on AMD once the
// reference was put in the log: 24% less asked for and 6% more produced, with
// the ratio going from 0.89 to 1.24 between two four-second windows.
//
// Formally "it does not follow", in fact noise: the throughput is measured on a
// scene that changes, and between nearby windows it swings by 20-30%. The proof
// has to sit well above that noise, and it can afford to because the phenomenon
// to be recognised is enormous.
func TestMeasurementNoiseDoesNotAccuse(t *testing.T) {
	p, out := watched(t)

	asked := []int{816, 816, 620, 620, 700, 620}
	produced := []int{724, 724, 766, 700, 780, 742}
	for i := range asked {
		window(p, asked[i], produced[i])
	}

	if deaf(p, out) {
		t.Errorf("accused over the noise of two nearby windows:\n%s", out.String())
	}
}

// TestADescentInOneGoIsCaught is the Quick Sync case, and the one the watch
// used to let slip.
//
// The quality loop does not always come down in steps: when the target is far
// away it gets there **in one go** and then sits oscillating around that value.
// Observed live: from the preset of 2500 to ~1100, and for two minutes requests
// between 1000 and 1200 with the encoder producing 2400.
//
// The reference would form on the first window observed — which already carries
// the reduced request — so the jump that constitutes the proof fell before there
// was anything to compare it with, and from then on no request halved again. The
// remedy is to seed the reference with the encoder's starting bitrate, which is
// a request like any other.
// The two halves stand together on purpose: the sequence is the same, only the
// seed changes, and the outcome flips. It is the demonstration that the seed
// **is** the mechanism, not one more precaution.
func TestADescentInOneGoIsCaught(t *testing.T) {
	// The loop gets to ~1100 straight away and stays there; the encoder does
	// not move.
	descent := []int{1098, 1014, 1180, 1115}

	t.Run("with the seed it is caught", func(t *testing.T) {
		p, out := watched(t)
		// This is what runVideo does as soon as the encoder is built: the
		// starting bitrate is a request like any other.
		p.bitrateAsked(2500)
		for _, asked := range descent {
			window(p, asked, 2400)
		}
		if !deaf(p, out) {
			t.Errorf("less than half the preset was asked for and the throughput did "+
				"not move, and nobody said anything:\n%s", out.String())
		}
	})

	t.Run("without the seed it slips through", func(t *testing.T) {
		p, out := watched(t)
		for _, asked := range descent {
			window(p, asked, 2400)
		}
		if deaf(p, out) {
			t.Skip("the criterion has changed: it is now caught without the seed too, " +
				"and this half of the test has finished its work")
		}
		// Without the seed the reference is born at 1098 and no later request
		// halves it: the proof never arrives. It is the defect seen on Quick
		// Sync — two minutes at 1100 asked and 2400 produced, in silence.
	})
}

// And the seed must not turn into an easy accusation: an encoder that follows
// that same descent in one go must not be touched.
func TestTheSeedDoesNotAccuseWhoeverFollows(t *testing.T) {
	p, out := watched(t)

	p.bitrateAsked(2500)
	// The same sequence of requests, but the throughput chases them one turn
	// behind, as an encoder that obeys does.
	asked := []int{1098, 1014, 1180, 1115}
	produced := []int{2450, 1120, 1050, 1200}
	for i := range asked {
		window(p, asked[i], produced[i])
	}

	if deaf(p, out) {
		t.Errorf("an encoder that followed was declared deaf:\n%s", out.String())
	}
}

// The real sequence from the log of a test machine with a 640x480 webcam and a
// still scene.
//
// The encoder was producing far less than it was being allowed — the scene asked
// for no more — and the watch read "I asked for less and the output did not go
// down". Those were two numbers dictated by the scene: between the two the
// request never touched the output, because it always sat above it.
//
// The cost of the false alarm is not a line of log: that machine went over to
// rebuilding the encoder **for the whole session**, which is the risky
// manoeuvre, and thirty-four seconds earlier the probe had established the
// opposite with a real experiment.
func TestAnEncoderThatIsNotBoundByTheRequestIsNotJudged(t *testing.T) {
	p, out := watched(t)

	asked := []int{775, 309, 309, 309, 309}
	produced := []int{114, 235, 228, 240, 231}
	for i := range asked {
		window(p, asked[i], produced[i])
	}
	if deaf(p, out) {
		t.Errorf("an encoder that had nothing taken away from it was accused:\n%s", out.String())
	}
}

// And the real case has to keep firing: there the ceiling **cuts** what was
// coming out, and the output does not notice.
func TestAnEncoderThatIgnoresACeilingBelowItsOutputIsStillCaught(t *testing.T) {
	p, out := watched(t)

	// 2500 asked and 2500 produced: the request was the constraint. Then it
	// halves, that is it goes well below the output, and the output does not
	// move.
	for _, c := range []struct{ asked, produced int }{
		{2500, 2480}, {1200, 2470}, {1200, 2490}, {1200, 2475},
	} {
		window(p, c.asked, c.produced)
	}
	if !deaf(p, out) {
		t.Errorf("an encoder ignoring a ceiling below its own output was not caught:\n%s",
			out.String())
	}
}

// TestARiseNeverCountsAsAVerification nails down the property the motion branch
// in `internal/rtc/quality.go` rests on.
//
// That branch, when the room moves, gives the bitrate discount back in one go
// and **skips the wait** of five seconds the loop otherwise imposes on itself.
// One of the two reasons for that wait is not to put two commands inside one
// window of this watch, and the answer is that for a request that **rises** the
// problem does not exist: the first branch of the switch clears the reference as
// soon as more is asked for than before, so there is no verification to count.
//
// The scene is the real one: the loop has walked all the way down with the
// encoder obeying, then the motion takes the request back to the cap and for a
// whole window the encoder still produces the old value — which is exactly what
// "I changed the command and nothing happened" looks like.
func TestARiseNeverCountsAsAVerification(t *testing.T) {
	p, out := watched(t)

	for _, asked := range []int{2500, 1200, 600, 300} {
		window(p, asked, asked) // the encoder follows
	}

	// The room moves: back to the cap, and the frames already in flight come
	// out with the old bitrate.
	window(p, 2500, 300)

	p.brMu.Lock()
	ref, streak := p.brWantRef, p.brStreak
	p.brMu.Unlock()

	if streak != 0 {
		t.Errorf("a rising request counted %d verifications: the watch can only "+
			"accuse after asking for appreciably **less**", streak)
	}
	if ref != 2500 {
		t.Errorf("the reference stayed at %d instead of restarting from 2500: "+
			"asking for more restarts the proof", ref)
	}
	if deaf(p, out) {
		t.Errorf("an encoder that was only asked for more was taken for deaf:\n%s",
			out.String())
	}
}
