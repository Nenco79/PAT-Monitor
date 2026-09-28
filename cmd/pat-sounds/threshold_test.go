package main

import (
	"os"
	"regexp"
	"strconv"
	"testing"
)

// **The tool counts at the monitor's threshold.** The two are constants in two
// main packages, so one cannot name the other: this reads the monitor's source.
// A sweep rerun without -threshold measured the chain at 0.20 after the monitor
// had moved to 0.15.
func TestTheToolCountsAtTheMonitorsThreshold(t *testing.T) {
	src, err := os.ReadFile("../pat-monitor/recognise.go")
	if err != nil {
		t.Fatal(err)
	}
	m := regexp.MustCompile(`soundThreshold\s+float32\s*=\s*([0-9.]+)`).FindSubmatch(src)
	if m == nil {
		t.Fatal("soundThreshold not found in cmd/pat-monitor/recognise.go: this test is looking in the wrong place")
	}
	want, err := strconv.ParseFloat(string(m[1]), 64)
	if err != nil {
		t.Fatal(err)
	}
	if monitorThreshold != want {
		t.Errorf("pat-sounds counts at %.2f, the monitor decides at %.2f", monitorThreshold, want)
	}
}
