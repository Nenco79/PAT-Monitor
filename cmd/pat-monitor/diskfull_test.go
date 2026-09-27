package main

import (
	"testing"
	"time"

	"patmonitor/internal/alerts"
	"patmonitor/internal/tray"
	"patmonitor/internal/update"
)

// **A disk too full for clips is an alert, and a Notice**: the monitor is
// watching and can be heard, and what has stopped is the record of it, which
// somebody is counting on finding. The tray says it too, since the remedy is on
// this machine — below the camera watching another room and below remote
// access, which are about watching now.
//
// **The defect was put back and this test fails with it**: without the case in
// activeFaults there is no alert, and without the one in trayStatus the icon
// says nothing.
func TestADiskTooFullForClipsIsSaidOnThePageAndInTheTray(t *testing.T) {
	s := healthyStatus()
	s.DiskFull = true
	now := time.Now()

	if got := activeFaults(s, now.Add(-time.Hour), now); !contains(got, alerts.DiskFull) {
		t.Fatalf("faults = %v: no clip is saved and nothing says so", got)
	}
	if l := alerts.LevelOf(alerts.DiskFull); l != alerts.Notice {
		t.Errorf("level %q: the monitor is still watching, it is a notice", l)
	}

	got := trayStatus(s, withPassword(), now, trayDict(t), update.State{})
	if got.Note != tray.NoteDiskFull || got.Fault != tray.FaultNone {
		t.Errorf("note %q, fault %q: the tray does not say the disk is full", got.Note, got.Fault)
	}

	s.CameraFallback = true
	if got := trayStatus(s, withPassword(), now, trayDict(t), update.State{}); got.Note != tray.NoteCameraOther {
		t.Errorf("note %q: another room on screen matters more than the clips", got.Note)
	}
}
