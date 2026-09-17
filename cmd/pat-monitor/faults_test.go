package main

import (
	"testing"
	"time"

	"patmonitor/internal/alerts"
	"patmonitor/internal/server"
	"patmonitor/internal/tunnel"
)

func contains(cs []alerts.Code, c alerts.Code) bool {
	for _, x := range cs {
		if x == c {
			return true
		}
	}
	return false
}

func TestAHealthyMonitorHasNothingToReport(t *testing.T) {
	now := time.Now()
	if got := activeFaults(healthyStatus(), now.Add(-time.Hour), now); len(got) != 0 {
		t.Errorf("faults = %v on a monitor that works", got)
	}
}

// **The defect the first live test found.** At startup the microphone is not
// open yet, and without the grace the log announced "microphone missing" at
// instant zero and its return a second later: with notifications, an alarm at
// every start of the program.
func TestNobodyIsBlamedDuringStartup(t *testing.T) {
	s := healthyStatus()
	s.MicrophoneActive = false
	s.Ready = false
	now := time.Now()

	if got := presentAlerts(s, false, false, false, now.Add(-2*time.Second), now); len(got) != 0 {
		t.Errorf("faults = %v two seconds from startup: it is still opening the devices", got)
	}
	if got := presentAlerts(s, false, false, false, now.Add(-time.Minute), now); len(got) == 0 {
		t.Error("once the grace has passed, the real faults have to appear")
	}
}

// **Motion is silent at startup for a reason of its own, and it is stronger than
// the faults':** whoever starts the monitor is in front of the camera and is
// moving. Without the grace, every single start would announce a movement.
func TestMotionIsSilentDuringStartup(t *testing.T) {
	now := time.Now()

	if got := presentAlerts(healthyStatus(), true, false, false, now.Add(-2*time.Second), now); len(got) != 0 {
		t.Errorf("announced %v two seconds from startup: that is whoever launched the program", got)
	}
	got := presentAlerts(healthyStatus(), true, false, false, now.Add(-time.Minute), now)
	if !contains(got, alerts.Motion) {
		t.Errorf("once the grace has passed, the motion has to appear: %v", got)
	}
}

func TestAStillRoomAddsNoEvent(t *testing.T) {
	now := time.Now()
	if got := presentAlerts(healthyStatus(), false, false, false, now.Add(-time.Minute), now); len(got) != 0 {
		t.Errorf("alerts = %v on a still room and a healthy monitor", got)
	}
}

// **The defect this predicate was written with, and the test that would have
// found it.** `captureStopped` was `!s.Ready`, and `Ready` closes on the first
// keyframe and never opens again: a monitor whose picture stopped after an hour
// satisfied it for ever. Measured on 17 September 2026 — frames stopped at
// 14:58 inside a rebuild of the encoder, the page stayed green, the
// notification area stayed green, and no alert was written in the three and a
// half minutes until the program was restarted by hand.
//
// **The defect was put back to watch this fail**: with `return !s.Ready && ...`
// in place, the case below reports no fault at all.
func TestAPictureThatStopsIsAFaultEvenWhenItStarted(t *testing.T) {
	now := time.Now()
	started := now.Add(-time.Hour)

	s := healthyStatus()
	s.LastFrameUnix = now.Add(-31 * time.Second).Unix()
	if got := activeFaults(s, started, now); !contains(got, alerts.CaptureStopped) {
		t.Errorf("faults = %v with the last frame half a minute old: the picture has stopped", got)
	}

	// And the other direction, which is what keeps the alert worth reading: a
	// gap of a few seconds is a camera being reopened, a step of the scale, a
	// machine that was busy. None of those is a fault.
	s.LastFrameUnix = now.Add(-3 * time.Second).Unix()
	if got := activeFaults(s, started, now); contains(got, alerts.CaptureStopped) {
		t.Errorf("faults = %v with a frame three seconds old: that is a monitor at work", got)
	}
}

// The grace holds for the stall as well, and for the same reason it holds for
// the microphone: in the first half minute the camera is being opened, and
// `LastFrameUnix` is legitimately zero.
func TestTheStallIsSilentDuringStartup(t *testing.T) {
	now := time.Now()
	s := healthyStatus()
	s.LastFrameUnix = 0

	if got := activeFaults(s, now.Add(-5*time.Second), now); contains(got, alerts.CaptureStopped) {
		t.Error("the capture was blamed five seconds in, while the camera is still opening")
	}
	if got := activeFaults(s, now.Add(-time.Minute), now); !contains(got, alerts.CaptureStopped) {
		t.Error("a minute in with no frame ever produced, the fault has to be there")
	}
}

func TestStalledCaptureBecomesAFault(t *testing.T) {
	s := healthyStatus()
	s.Ready = false
	now := time.Now()

	// In the first thirty seconds it is not a fault: the capture is starting.
	if got := activeFaults(s, now.Add(-5*time.Second), now); contains(got, alerts.CaptureStopped) {
		t.Error("the capture was blamed after five seconds: it is still opening the camera")
	}
	if got := activeFaults(s, now.Add(-time.Minute), now); !contains(got, alerts.CaptureStopped) {
		t.Errorf("faults = %v after a minute with no stream", got)
	}
}

// **The same fault is not announced twice from two points of view.** A
// microphone that is not there also delivers zeros, and saying both would fill
// the banner with two rows describing one thing.
func TestAMissingMicAbsorbsTheSilence(t *testing.T) {
	s := healthyStatus()
	s.MicrophoneActive = false
	s.MicHealth = "digital-silence"

	got := activeFaults(s, time.Now().Add(-time.Hour), time.Now())
	if !contains(got, alerts.MicMissing) {
		t.Errorf("faults = %v, expected mic-missing", got)
	}
	if contains(got, alerts.MicSilent) {
		t.Errorf("faults = %v: the same zeros were announced twice", got)
	}
}

func TestDigitalSilenceBecomesAFault(t *testing.T) {
	s := healthyStatus()
	s.MicHealth = "digital-silence"

	if got := activeFaults(s, time.Now().Add(-time.Hour), time.Now()); !contains(got, alerts.MicSilent) {
		t.Errorf("faults = %v, expected mic-silent", got)
	}
}

// A Funnel that is open but has no ingress is the worst of the cases that look
// fine: a valid address, and no way through from the Internet.
func TestATunnelWithoutIngressBecomesANotice(t *testing.T) {
	s := healthyStatus()
	s.Remote.Phase = tunnel.PhaseRunning
	s.Remote.PublicURL = "https://patmon.quercia-lieve.ts.net"
	s.Remote.Warning = "ingress not granted"

	got := activeFaults(s, time.Now().Add(-time.Hour), time.Now())
	if !contains(got, alerts.RemoteDown) {
		t.Errorf("faults = %v, expected remote-down", got)
	}
	if alerts.LevelOf(alerts.RemoteDown) != alerts.Notice {
		t.Error("remote access down is classified as a fault: at home the monitor is perfectly visible")
	}
}

// A public address that fails the probe is worth as much as a missing ingress:
// from outside, in both cases, there is no way through.
//
// **And the absence of a probe is not a fault**, which is the half that makes
// the mechanism usable: the public name appears in the DNS minutes after
// startup, and on a machine where the Tailscale client is also installed the
// probe cannot be run at all. Declaring "not responding" in those two cases
// would mean a red banner at every start, that is, an alert nobody reads any
// more — and this is the only alarm the program has.
func TestAPublicAddressThatFailsTheProbeBecomesANotice(t *testing.T) {
	withReach := func(r tunnel.ReachCode) server.Status {
		s := healthyStatus()
		s.Remote.Phase = tunnel.PhaseRunning
		s.Remote.PublicURL = "https://patmon.quercia-lieve.ts.net"
		s.Remote.Reach = r
		return s
	}

	got := activeFaults(withReach(tunnel.ReachFailed), time.Now().Add(-time.Hour), time.Now())
	if !contains(got, alerts.RemoteDown) {
		t.Errorf("faults = %v, expected remote-down", got)
	}

	for _, r := range []tunnel.ReachCode{tunnel.ReachUnknown, tunnel.ReachChecking, tunnel.ReachVerified} {
		got := activeFaults(withReach(r), time.Now().Add(-time.Hour), time.Now())
		if contains(got, alerts.RemoteDown) {
			t.Errorf("with outcome %q the faults are %v: with no evidence to the "+
				"contrary there is nothing to announce", r, got)
		}
	}
}

// The simulation alternates, otherwise it would test only half the round: the
// case one gets wrong when writing it is the return, not the appearance.
func TestSimulationAlternatesOnAndOff(t *testing.T) {
	previous := *simulateFault
	*simulateFault = "capture-stopped, mic-silent"
	defer func() { *simulateFault = previous }()

	on := time.Unix(0, 0)       // window 0: on
	off := time.Unix(20, 0)     // window 1: off
	onAgain := time.Unix(40, 0) // window 2: on

	if got := simulatedFaults(on); len(got) != 2 {
		t.Errorf("window on: %v, expected two codes", got)
	}
	if got := simulatedFaults(off); len(got) != 0 {
		t.Errorf("window off: %v, expected no code", got)
	}
	if got := simulatedFaults(onAgain); len(got) != 2 {
		t.Errorf("second window on: %v, expected two codes", got)
	}
}

func TestNothingIsSimulatedWithoutTheFlag(t *testing.T) {
	previous := *simulateFault
	*simulateFault = ""
	defer func() { *simulateFault = previous }()

	if got := simulatedFaults(time.Unix(0, 0)); got != nil {
		t.Errorf("simulated %v without being asked", got)
	}
}

// A badly written code does not complain on its own: the recorder would accept
// it and the page would show a banner with no words.
func TestABadSimulationCodeIsRejected(t *testing.T) {
	previous := *simulateFault
	defer func() { *simulateFault = previous }()

	*simulateFault = "mic-silente"
	if err := validateSimulation(); err == nil {
		t.Error("a code that does not exist was accepted")
	}
	*simulateFault = "mic-silent,remote-down"
	if err := validateSimulation(); err != nil {
		t.Errorf("valid codes were refused: %v", err)
	}
}

// The switches say what is in the room, and whoever has no dog must not hear a
// wrong bark: the event is not hidden, it is not produced.
func TestTheSwitchesKeepTheEventOut(t *testing.T) {
	now := time.Now()
	old := now.Add(-time.Minute)

	got := presentAlerts(healthyStatus(), false, true, true, old, now)
	if !contains(got, alerts.Cry) || !contains(got, alerts.Bark) {
		t.Fatalf("alerts = %v, expected both events", got)
	}
	if got := presentAlerts(healthyStatus(), false, false, true, old, now); contains(got, alerts.Cry) {
		t.Errorf("alerts = %v: the cry got through with detection off", got)
	}
}

// Motion has its switch like the other two: whoever keeps the monitor in a
// through room turns that one off and goes on hearing the rest.
func TestTheMotionSwitchKeepsTheEventOut(t *testing.T) {
	now := time.Now()
	old := now.Add(-time.Minute)

	if got := presentAlerts(healthyStatus(), true, false, false, old, now); !contains(got, alerts.Motion) {
		t.Fatalf("alerts = %v, expected the motion", got)
	}
	// The filter lives in cmd/pat-monitor, on the snapshot: here what is tested
	// is that presentAlerts reports what it is handed, and that handing it
	// "still" produces nothing.
	if got := presentAlerts(healthyStatus(), false, false, false, old, now); len(got) != 0 {
		t.Errorf("alerts = %v with everything off", got)
	}
}

// **A camera that is watching the wrong room is not a camera that has stopped.**
// The two say different things — one that there is no picture, the other that the
// picture may be of somewhere else — and whoever has both has to read both, so
// this alert is not conditioned on the capture being healthy the way mic-silent
// is conditioned on the microphone being there.
func TestTheChosenCameraGoneIsAnAlertOfItsOwn(t *testing.T) {
	s := healthyStatus()
	s.CameraFallback = true
	now := time.Now()

	got := activeFaults(s, now.Add(-time.Hour), now)
	if !contains(got, alerts.CameraOther) {
		t.Fatalf("faults = %v: the chosen camera is not connected and nothing says so", got)
	}
	if contains(got, alerts.CaptureStopped) {
		t.Errorf("faults = %v: a fallback camera is delivering pictures, it has not stopped", got)
	}
	// A Notice and not a Fault: there is a live picture. A fault here would also
	// turn the tray icon brick, that is, teach whoever passes the machine to
	// read a working monitor as a broken one.
	if lvl := alerts.LevelOf(alerts.CameraOther); lvl != alerts.Notice {
		t.Errorf("level %v: a monitor showing a live room is not a fault", lvl)
	}
}

// The alert is **read** from the field the capture publishes, and not worked out
// again from the two links: the chosen one comes out of a file and the open one
// out of the enumeration, and Windows gives the same symbolic link back in
// different cases depending on who is asked. Recomputed here, it would declare
// another camera about the right one.
func TestTheCameraFallbackIsNotWorkedOutFromTheNames(t *testing.T) {
	s := healthyStatus()
	s.CameraChosen = `\?\usb#VID_046D#{GUID}\global`
	s.Camera = "Integrated Camera"
	s.CameraFallback = false
	now := time.Now()

	if got := activeFaults(s, now.Add(-time.Hour), now); contains(got, alerts.CameraOther) {
		t.Errorf("faults = %v: the chosen camera is open and it was declared another one", got)
	}
}

// **The test instrument must not spend the one dump the process has.**
//
// The goroutine dump is raised while walking the alerts that appeared, and that
// set is `presentAlerts` plus `simulatedFaults`: asking for `-simulate-fault
// capture-stopped` therefore raises the code on a monitor whose capture is
// running, where `Ready` is true and the last frame arrived a moment ago. Both
// conditions that used to guard the dump were satisfied by construction, so the
// flag wrote sixty kilobytes of stacks of a healthy pipeline and `dumpOnce` was
// gone — and a real stall the same night would have written nothing, which is
// the silence the dump exists to break.
//
// **The defect was put back to watch this fail**: with the `captureStopped`
// call removed from the predicate, the first case below answers true.
func TestASimulatedStallIsNotWorthADump(t *testing.T) {
	now := time.Now()
	started := now.Add(-time.Hour)

	// What the flag produces: the alert is in the set, and the status under it
	// is of a monitor at work.
	if stalledAfterStarting(healthyStatus(), started, now) {
		t.Error("a healthy monitor was read as a stopped picture, so a flag can spend the dump")
	}

	// The real one, which is what the dump is for: the picture stopped after
	// having started, and there may be nothing else in the log at all.
	s := healthyStatus()
	s.LastFrameUnix = now.Add(-31 * time.Second).Unix()
	if !stalledAfterStarting(s, started, now) {
		t.Error("a picture stopped for half a minute raised no dump")
	}

	// And the capture that never started is excluded here rather than in the
	// alert: its diagnosis is the error on the line above it in the log, and
	// the stacks would show a camera being opened.
	s = healthyStatus()
	s.Ready = false
	s.LastFrameUnix = 0
	if stalledAfterStarting(s, started, now) {
		t.Error("a capture that never started took the dump meant for a silent stall")
	}

	// The startup grace holds here too, through captureStopped: half a minute
	// in, a capture with no frame yet is a camera being opened.
	s = healthyStatus()
	s.LastFrameUnix = now.Add(-31 * time.Second).Unix()
	if stalledAfterStarting(s, now.Add(-5*time.Second), now) {
		t.Error("the dump was raised five seconds from startup")
	}
}
