package main

import (
	"log/slog"
	"os"
	"testing"
	"time"
)

// **With both switches off the model does not wake up.** The verdict has a
// single reader — unlike motion, which also has the bitrate discount — so
// classifying with both off is half a second of CPU for an answer nobody will
// look at.
//
// **The stream is delivered all the same**: turning a switch on, the window has
// to be full already, otherwise the first verdict would arrive ten seconds later
// with the previous silence inside it.
//
// **It is also the guardian of `watchedClasses`**, which is the only way the
// recogniser can really fail to start: the four names are written by hand and
// have to exist **under those exact names** among the 527 classes.
// `newRecogniser` resolves them all, so calling it here checks them all — a list
// of names copied into a test would be the second list, and it diverges.
func TestNothingIsAskedWithBothSwitchesOff(t *testing.T) {
	r := newRecogniser(slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError})))
	if r == nil {
		t.Fatal("newRecogniser answered nil: either the embedded model did not load, or one of the watchedClasses names is not among the classes, and the log says which")
	}
	defer r.Close()

	// A whole window of noise, so that the stream is ready in either case.
	win := make([]byte, 2*16000)
	x := uint32(7)
	for i := 0; i+1 < len(win); i += 2 {
		x = x*1664525 + 1013904223
		v := int16(x >> 20)
		win[i], win[i+1] = byte(uint16(v)), byte(uint16(v)>>8)
	}
	for i := 0; i < 12; i++ {
		r.Feed(win, 16000)
	}

	r.Wanted(false, false)
	r.Ask()
	// **It waits for more than a whole classification**, which is ~500 ms:
	// waiting a hundred milliseconds finds `nil` in both cases — that is, it
	// absolves the defect by measuring its own haste instead of the remedy.
	time.Sleep(2 * time.Second)
	if res := r.stream.Last(); res != nil {
		t.Errorf("it classified with both switches off: %.2f s", res.Seconds)
	}

	// And with one on, the window is already there: the verdict arrives at once.
	r.Wanted(false, true)
	r.Ask()
	for i := 0; i < 100 && r.stream.Last() == nil; i++ {
		time.Sleep(20 * time.Millisecond)
	}
	res := r.stream.Last()
	if res == nil {
		t.Fatal("no verdict")
	}
	if res.Seconds < 9 {
		t.Errorf("the window classified was %.2f s: the stream was not ready", res.Seconds)
	}
}
