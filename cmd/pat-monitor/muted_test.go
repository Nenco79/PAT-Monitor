package main

import (
	"testing"
	"time"

	"patmonitor/internal/alerts"
	"patmonitor/internal/detect"
	"patmonitor/internal/server"
	"patmonitor/internal/tray"
	"patmonitor/internal/update"
)

// mutedStatus is a monitor whose microphone opens, captures, and is muted in
// Windows — which is what the endpoint's own mute produces: the samples are
// zeros, so the health reads digital silence at the same time.
func mutedStatus() server.Status {
	s := healthyStatus()
	s.MicrophoneMuted = true
	s.MicHealth = detect.MicCodeDigitalSilence
	return s
}

// **The cause is announced and the consequence is not.** Both predicates answer
// yes about a muted microphone, and of the two only one says what to do about
// it: *the microphone cannot be heard* sends whoever reads it after a cable,
// a driver or a dead device, and the thing that is wrong is a switch on the
// machine they are standing in front of. It is the same rule as the refused
// permission sitting above the missing picture.
//
// **Verified to catch**: with the `micMuted` branch taken out of activeFaults,
// this fails saying `mic-silent` came out on its own.
func TestAMutedMicrophoneIsNamedAsMutedAndNotAsSilent(t *testing.T) {
	now := time.Now()
	got := activeFaults(mutedStatus(), now.Add(-time.Hour), now)

	if !contains(got, alerts.MicMuted) {
		t.Errorf("faults = %v: the mute is what is wrong and it is not said", got)
	}
	if contains(got, alerts.MicSilent) {
		t.Errorf("faults = %v: the cause and the consequence are both announced, "+
			"in a product that shows one banner", got)
	}
}

// **And a microphone that is not there is not described as muted.** The three
// are a chain and the order is refused, absent, muted, silent: a mute read off
// an endpoint nobody has opened is last night's answer, and the row it would
// produce offers a slider for a device that is not in the machine.
func TestWhatIsNotThereIsNotDescribedAsMuted(t *testing.T) {
	now := time.Now()

	absent := mutedStatus()
	absent.MicrophoneActive = false
	got := activeFaults(absent, now.Add(-time.Hour), now)
	if !contains(got, alerts.MicMissing) || contains(got, alerts.MicMuted) {
		t.Errorf("faults = %v on a microphone that is not there", got)
	}

	denied := mutedStatus()
	denied.MicrophoneDenied = true
	got = activeFaults(denied, now.Add(-time.Hour), now)
	if !contains(got, alerts.MicDenied) || contains(got, alerts.MicMuted) {
		t.Errorf("faults = %v with the permission off", got)
	}
}

// **An endpoint being opened says nothing yet**, which is the guard the whole
// microphone family carries: packaged, that wait is Windows asking the person
// at the machine whether this program may capture at all, and the mute field
// still holds whatever the last open found.
func TestNothingIsSaidAboutAMuteWhileTheMicrophoneIsOpening(t *testing.T) {
	s := mutedStatus()
	s.MicrophoneOpening = true
	if micMuted(s) {
		t.Error("a mute is claimed while the endpoint is still being opened")
	}
}

// **The icon and the banner agree about which of the three wins.** They are two
// readers of the same predicates — one reduces everything to a colour, the
// other picks one line out of a set — and written twice they can disagree,
// which is the icon saying one thing and the page another about one monitor.
//
// **Verified to catch**: with `micMuted` left out of the tray's switch, this
// fails saying the icon reports the silence.
func TestTheIconAndTheBannerNameTheSameMicrophoneFault(t *testing.T) {
	for _, c := range []struct {
		name  string
		build func() server.Status
		fault tray.Fault
	}{
		{"muted", mutedStatus, tray.FaultMicMuted},
		{"absent", func() server.Status {
			s := mutedStatus()
			s.MicrophoneActive = false
			return s
		}, tray.FaultMicMissing},
		{"silent, and not muted", func() server.Status {
			s := mutedStatus()
			s.MicrophoneMuted = false
			return s
		}, tray.FaultMicSilent},
	} {
		s := c.build()
		got := trayStatus(s, withPassword(), time.Now().Add(-time.Hour), trayDict(t), update.State{})
		if got.Fault != c.fault {
			t.Errorf("%s: the icon says %q, wanted %q", c.name, got.Fault, c.fault)
		}
	}
}
