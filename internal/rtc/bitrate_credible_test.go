package rtc

import "testing"

// The vicious circle this rule exists to break, measured live on this monitor: a
// still dark room compresses beautifully, the encoder produces ~1600 kbit/s
// against 2500 asked for, gcc cannot estimate more than what goes past and its
// estimate comes down towards that value. Taken for a limit, it lowers the
// target, which makes even less get produced, and so on — in the ordinary
// condition of the night.
func TestAnEstimateThatEchoesOurOwnThroughput(t *testing.T) {
	// Observed: estimate 1537 of transport, that is ~1399 of video, with ~1600
	// produced. The estimate does not sit below what we managed to send.
	if estimateIsCredible(1399, 1600) {
		t.Error("an estimate grazing the produced throughput was taken for a network limit")
	}
}

func TestARealDropGoesThroughAnyway(t *testing.T) {
	// Measured on a cellular network: the estimate came down to ~300 while the
	// encoder was producing 2500. There the network really did refuse.
	if !estimateIsCredible(300, 2500) {
		t.Error("a real drop was mistaken for an echo")
	}
}

func TestWithoutAThroughputTheEstimateCounts(t *testing.T) {
	// At start-up, or while the capture is restarting, there is nothing to
	// compare with: there the estimate is all we have and it is not discarded.
	if !estimateIsCredible(500, 0) {
		t.Error("with no measured throughput the estimate was discarded")
	}
}

// The threshold has to sit between the two cases, otherwise it solves one by
// breaking the other.
func TestTheThresholdSitsBetweenTheTwoCases(t *testing.T) {
	const produced = 1600
	// Just below the throughput: an echo.
	if estimateIsCredible(produced-1, produced) {
		t.Error("just below the throughput ought to be an echo")
	}
	// Half the throughput: a real drop.
	if !estimateIsCredible(produced/2, produced) {
		t.Error("half the throughput ought to be a real drop")
	}
}
