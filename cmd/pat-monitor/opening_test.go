package main

import (
	"testing"
	"time"

	"patmonitor/internal/alerts"
	"patmonitor/internal/config"
	"patmonitor/internal/detect"
	"patmonitor/internal/i18n"
	"patmonitor/internal/server"
	"patmonitor/internal/tray"
	"patmonitor/internal/update"
)

// **"Not yet" is not "not there", and under a package the difference is as long
// as a person takes to answer.**
//
// Measured on 21 September 2026 with the monitor packaged as MSIX: Windows asks
// for the camera and the microphone, one consent per package, at the first use,
// and the open call waits. Unpackaged that window is 0.5 s and nobody sees it;
// packaged it lasted forty seconds, and the monitor spent thirty of them
// announcing `capture-stopped` and `mic-missing` — the second of which asserts
// there is no microphone while the person is being asked whether this program
// may use one.
//
// **Verified to catch**: with `!s.CameraOpening` and `!s.MicrophoneOpening`
// taken back out of the two predicates, both subtests fail with the codes they
// refuse.
func TestWhileWindowsIsAskingNothingIsBroken(t *testing.T) {
	started := time.Date(2026, 9, 21, 22, 0, 0, 0, time.UTC)
	// Well past the start-up grace: the thirty seconds are exactly what ran out
	// while the dialogue was on the screen.
	now := started.Add(5 * time.Minute)

	t.Run("the camera", func(t *testing.T) {
		got := activeFaults(server.Status{
			CameraOpening:    true,
			MicrophoneActive: true,
			MicHealth:        detect.MicCodeOK,
		}, started, now)
		if has(got, alerts.CaptureStopped) {
			t.Errorf("%v: a camera being opened is not a capture that stopped", got)
		}
	})

	t.Run("the microphone", func(t *testing.T) {
		got := activeFaults(server.Status{
			Ready:             true,
			LastFrameUnix:     now.Unix(),
			MicrophoneOpening: true,
			MicHealth:         detect.MicCodeOK,
		}, started, now)
		if has(got, alerts.MicMissing) {
			t.Errorf("%v: it says there is no microphone while Windows is asking "+
				"whether this program may use one", got)
		}
	})

	// **The one the unit tests missed and the machine found.** A microphone
	// that never opened has no level, so the health reads as digital silence:
	// waiving `micMissing` alone drops the chain onto `micSilent`, and the page
	// gets *the microphone delivers zeros* — the worst fault this product has,
	// in place of one that was merely wrong. It passed here with
	// `MicHealth: MicCodeOK`, which is precisely the value a microphone that
	// has not opened does not have.
	t.Run("the microphone, with no level at all", func(t *testing.T) {
		got := activeFaults(server.Status{
			Ready:             true,
			LastFrameUnix:     now.Unix(),
			MicrophoneOpening: true,
			MicHealth:         detect.MicCodeDigitalSilence,
		}, started, now)
		if has(got, alerts.MicSilent) {
			t.Errorf("%v: zeros from a microphone that was never opened are not "+
				"a microphone delivering zeros", got)
		}
		if has(got, alerts.MicMissing) {
			t.Errorf("%v: and it is not absent either", got)
		}
	})
}

// **The wait must not swallow the answer.** A refusal comes back — measured
// under the package too, `E_ACCESSDENIED` from `ActivateObject` and from
// `IAudioClient.Initialize` — and from that moment the opening is over and the
// denied pair is what speaks. A flag left standing would turn the one state the
// program can do something about into silence.
func TestAnAnsweredRefusalIsStillAnnounced(t *testing.T) {
	started := time.Date(2026, 9, 21, 22, 0, 0, 0, time.UTC)
	now := started.Add(5 * time.Minute)

	got := activeFaults(server.Status{
		CameraDenied:     true,
		MicrophoneDenied: true,
		MicHealth:        detect.MicCodeOK,
	}, started, now)

	if !has(got, alerts.CameraDenied) || !has(got, alerts.MicDenied) {
		t.Errorf("%v: the refusal answered, and nobody says so", got)
	}
}

// **The picture that stopped stays reachable.** On 17 September 2026 the
// encoder's rebuild did not come back and nothing complained for three and a
// half minutes; the stall half of `captureStopped` is what was written for it.
// Waiving the "never started" half must not waive that one, and a rebuild is
// precisely a moment when the camera is being opened again.
func TestACaptureThatStoppedIsAnnouncedEvenWhileReopening(t *testing.T) {
	started := time.Date(2026, 9, 21, 22, 0, 0, 0, time.UTC)
	now := started.Add(5 * time.Minute)

	got := activeFaults(server.Status{
		Ready:            true,
		LastFrameUnix:    now.Add(-2 * time.Minute).Unix(),
		CameraOpening:    true,
		MicrophoneActive: true,
		MicHealth:        detect.MicCodeOK,
	}, started, now)

	if !has(got, alerts.CaptureStopped) {
		t.Errorf("%v: the picture stopped two minutes ago and reopening hides it", got)
	}
}

// **The second between the device opening and the picture starting is not a
// capture that stopped**, and it only exists once the open has waited: measured
// under the package, `capture-stopped` up at 23:23:33.307 and off at
// 23:23:34.306, in the second between `camOpening` going down and the first
// keyframe arriving. Unpackaged the camera opens half a second in, well inside
// the grace, and the window cannot occur at all — which is why a whole evening
// of running it had never shown this.
//
// **Verified to catch**: with the anchor taken back out of `captureStopped`,
// the first subtest fails carrying `capture-stopped`.
func TestTheGraceIsCountedFromTheOpenAndNotFromTheProcess(t *testing.T) {
	started := time.Date(2026, 9, 21, 22, 0, 0, 0, time.UTC)
	// Two minutes in: the process's own grace ran out long ago, which is the
	// whole point — the person was reading a dialogue.
	now := started.Add(2 * time.Minute)

	t.Run("the camera has just come open", func(t *testing.T) {
		got := activeFaults(server.Status{
			CameraOpenedUnix: now.Add(-1 * time.Second).Unix(),
			MicrophoneActive: true,
			MicHealth:        detect.MicCodeOK,
		}, started, now)
		if has(got, alerts.CaptureStopped) {
			t.Errorf("%v: one second after the device opened the picture is still coming", got)
		}
	})

	// And the anchor is a grace, not an excuse: a camera that opened and never
	// delivered is the fault it always was, thirty seconds later.
	t.Run("and then never delivered", func(t *testing.T) {
		got := activeFaults(server.Status{
			CameraOpenedUnix: now.Add(-2 * time.Minute).Unix(),
			MicrophoneActive: true,
			MicHealth:        detect.MicCodeOK,
		}, started, now)
		if !has(got, alerts.CaptureStopped) {
			t.Errorf("%v: open two minutes ago and not a frame, and nobody says so", got)
		}
	})
}

// And in front of the machine the icon says the thing that is true: it is
// starting. There is no new word for it — `PhaseStarting` was already there,
// and what kept it out was `mic-missing` taking precedence over it.
func TestTheIconSaysStartingWhileTheDeviceIsBeingOpened(t *testing.T) {
	cfg := config.Default()
	cfg.PasswordHash = "$argon2id$v=19$m=65536,t=3,p=4$c2FsdA$aGFzaA"
	dict := i18n.Open([]string{"en"})
	started := time.Now().Add(-5 * time.Minute)

	st := trayStatus(server.Status{
		CameraOpening:     true,
		MicrophoneOpening: true,
		MicHealth:         detect.MicCodeOK,
		RawAudio:          true,
	}, cfg, started, dict, update.State{})

	if st.Fault == tray.FaultMicMissing {
		t.Error("the icon says there is no microphone while one is being asked for")
	}
	if st.Phase != tray.PhaseStarting {
		t.Errorf("the icon is %q, wanted %q", st.Phase, tray.PhaseStarting)
	}
}
