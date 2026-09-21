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

// has says whether the snapshot carries a code.
func has(codes []alerts.Code, want alerts.Code) bool {
	for _, c := range codes {
		if c == want {
			return true
		}
	}
	return false
}

// **A refused permission is announced instead of its consequence, not beside
// it.**
//
// With the camera switched off in Windows there are no frames, so
// `capture-stopped` is true as well — and of the two only one says what to do
// about it. The page shows one banner: putting *no images from the camera*
// beside *the camera permission is off* would be the same fault seen from two
// points, which is the rule the microphone's own pair already follows a few
// lines below in `activeFaults`.
//
// **Verified to catch**: with the `else` taken off, the first subtest fails
// carrying both codes.
func TestTheRefusalIsAnnouncedInsteadOfItsConsequence(t *testing.T) {
	started := time.Date(2026, 9, 21, 22, 0, 0, 0, time.UTC)
	// Well past the start-up grace: what is not ready yet is not yet broken,
	// and a snapshot taken inside it announces nothing at all.
	now := started.Add(5 * time.Minute)

	t.Run("the camera", func(t *testing.T) {
		got := activeFaults(server.Status{
			CameraDenied:     true,
			MicrophoneActive: true,
			MicHealth:        detect.MicCodeOK,
		}, started, now)
		if !has(got, alerts.CameraDenied) {
			t.Fatalf("the refusal is not announced: %v", got)
		}
		if has(got, alerts.CaptureStopped) {
			t.Errorf("%v: the consequence is announced beside the cause, and the "+
				"page has one banner", got)
		}
	})

	t.Run("the microphone", func(t *testing.T) {
		got := activeFaults(server.Status{
			Ready:            true,
			LastFrameUnix:    now.Unix(),
			MicrophoneDenied: true,
		}, started, now)
		if !has(got, alerts.MicDenied) {
			t.Fatalf("the refusal is not announced: %v", got)
		}
		if has(got, alerts.MicMissing) || has(got, alerts.MicSilent) {
			t.Errorf("%v: a microphone the user has taken away is announced as a "+
				"missing one, which sends them to look at the hardware", got)
		}
	})

	// And the half that must not be lost: with nothing refused, the ordinary
	// faults still come out. A precedence written the wrong way round would
	// silence them and this test would be the only thing that noticed.
	t.Run("nothing is refused", func(t *testing.T) {
		got := activeFaults(server.Status{MicHealth: detect.MicCodeOK}, started, now)
		if !has(got, alerts.CaptureStopped) || !has(got, alerts.MicMissing) {
			t.Errorf("%v: the ordinary faults stopped being announced", got)
		}
	})
}

// **The notification area names the refusal too, and before the silence it
// causes.**
//
// It is the one surface where the remedy exists — the switch is in this
// machine's Settings — so a colour that said "no images" would send whoever
// walks past the computer to look at a cable. The order of the cases in
// `trayStatus` is the severity scale, and the refusal sits where its
// consequence sat.
//
// **Verified to catch**: moving the `cameraDenied` case below
// `captureStopped`, the first subtest reads `capture-stopped`.
func TestTheIconNamesTheRefusalAndNotTheSilence(t *testing.T) {
	cfg := config.Default()
	cfg.PasswordHash = "$argon2id$v=19$m=65536,t=3,p=4$c2FsdA$aGFzaA" // set: no password is the most urgent thing the icon can say
	dict := i18n.Open([]string{"en"})
	started := time.Now().Add(-5 * time.Minute)

	t.Run("the camera", func(t *testing.T) {
		st := trayStatus(server.Status{
			CameraDenied:     true,
			MicrophoneActive: true,
			MicHealth:        detect.MicCodeOK,
			RawAudio:         true,
		}, cfg, started, dict, update.State{})
		if st.Fault != tray.FaultCameraDenied {
			t.Errorf("the icon says %q, wanted %q", st.Fault, tray.FaultCameraDenied)
		}
		// Brick and not amber: there is no picture at all, which is the
		// boundary `PhaseFault` is defined by. That it is somebody's decision
		// rather than a breakage does not change what the room gets.
		if st.Phase != tray.PhaseFault {
			t.Errorf("the icon is %q: with no picture at all it is a fault", st.Phase)
		}
	})

	t.Run("the microphone", func(t *testing.T) {
		st := trayStatus(server.Status{
			Ready:            true,
			LastFrameUnix:    time.Now().Unix(),
			MicrophoneDenied: true,
			RawAudio:         true,
		}, cfg, started, dict, update.State{})
		if st.Fault != tray.FaultMicDenied {
			t.Errorf("the icon says %q, wanted %q", st.Fault, tray.FaultMicDenied)
		}
	})
}
