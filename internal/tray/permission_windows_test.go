//go:build windows

package tray

import (
	"testing"

	"patmonitor/internal/i18n"
)

// **Each fault with a switch behind it opens its own page**, and there is one
// word for all of them, because the line above already names the device: the
// notice says *microphone: permission is off* and the button says what pressing
// does.
//
// The first draft put the device name on the button too, which read as the same
// word twice on two adjacent rows, and made that button the widest thing in the
// panel.
//
// **They open three different pages, and that is what the second half asks.**
// Two faults sharing an address would send whoever presses one of them to a
// switch that is not the one the line above just named — a button that goes
// somewhere plausible and wrong, which is worse than a panel with no button.
func TestEachRefusalOpensItsOwnPage(t *testing.T) {
	want := map[Fault]string{
		FaultCameraDenied: "ms-settings:privacy-webcam",
		FaultMicDenied:    "ms-settings:privacy-microphone",
		FaultMicMuted:     "ms-settings:sound",
	}
	seen := map[string]Fault{}
	for f, url := range want {
		got := settingsPage(f)
		if got != url {
			t.Errorf("%q opens %q, wanted %q", f, got, url)
		}
		if other, twice := seen[got]; twice {
			t.Errorf("%q and %q open the same page: one of the two sends whoever "+
				"presses it to the wrong switch", f, other)
		}
		seen[got] = f
	}

	// **And every other fault answers nothing**, which is what keeps the command
	// out of the panel when there is nothing to grant. The list is the
	// authoritative one, so a fault added tomorrow falls under this with nobody
	// having to remember it.
	for _, f := range append(AllFaults(), FaultNone) {
		if _, taken := want[f]; taken {
			continue
		}
		if url := settingsPage(f); url != "" {
			t.Errorf("%q offers %q, and there is no switch behind it", f, url)
		}
	}
}

// **The command answers the line above it, so it is drawn under it.**
//
// Down in the column it would sit four rows from the sentence it belongs to,
// with the QR code and the address in between, which is where the first version
// of it landed, and there it reads as belonging to the address.
//
// **Verified to catch**: with `lead` left false, the first subtest fails saying
// the command is below the code.
func TestThePermissionIsAnsweredUnderTheLineThatStatesIt(t *testing.T) {
	tr := &Tray{dictionary: i18n.Open([]string{"en"})}
	word := tr.t("tray.menu.settings")

	find := func(f *flyout, label string) int {
		for i, c := range f.cmds {
			if c.label == label {
				return i
			}
		}
		return -1
	}

	t.Run("it is above the code and below the lines", func(t *testing.T) {
		f := &flyout{t: tr, dpi: 96, qrBm: 1, qrPx: 120}
		f.compose(Status{Fault: FaultMicDenied})
		i := find(f, word)
		if i < 0 {
			t.Fatalf("the panel does not offer %q: the one place the switch can be reached", word)
		}
		if f.leadIndex() != i {
			t.Fatal("the command is not the lead: it would be drawn below the code")
		}
		p := f.layout()
		if !(p.linesY < p.leadY && p.leadY < p.qrY) {
			t.Errorf("lines at %d, command at %d, code at %d: the command has to sit "+
				"between the sentence it answers and the code", p.linesY, p.leadY, p.qrY)
		}
		// And the block below still starts after the code, which is what says
		// the lead was taken out of it rather than added twice.
		if p.cmdY <= p.qrY {
			t.Errorf("the command column starts at %d, at or above the code at %d", p.cmdY, p.qrY)
		}
		for _, row := range f.rows() {
			for _, k := range row {
				if k == i {
					t.Error("the lead is in the column as well: it would be laid out twice")
				}
			}
		}
	})

	// **It is drawn like every other command.** The selected one already wears
	// the accent, which is the panel rule about marks, so a filled pill on top
	// of the focus would be two marks for one thing. The pill stays on the
	// tunnel step, which is a different question.
	t.Run("it wears no pill, and the tunnel step keeps its own", func(t *testing.T) {
		f := &flyout{t: tr, dpi: 96}
		f.compose(Status{
			Fault:   FaultCameraDenied,
			Todo:    "approve this machine",
			TodoURL: "https://login.example/admin",
		})
		i := find(f, word)
		if i < 0 {
			t.Fatal("the panel does not offer the settings page")
		}
		if f.cmds[i].style != styleGhost {
			t.Errorf("the command is drawn as style %d: it takes the colour of the "+
				"others and the focus, not a pill of its own", f.cmds[i].style)
		}
		pills := 0
		for _, c := range f.cmds {
			if c.style == stylePill {
				pills++
			}
		}
		if pills != 1 {
			t.Errorf("%d filled pills: the panel has one main action", pills)
		}
	})

	// The focus falls on it by construction, because `initialFocus` takes the
	// first command. It is asserted rather than left to the reading: the order
	// of `compose` is what holds it up, and a command inserted above it would
	// move the focus in silence.
	t.Run("it is the first command, so it takes the focus", func(t *testing.T) {
		f := &flyout{t: tr, dpi: 96}
		f.compose(Status{Fault: FaultMicDenied, Todo: "x", TodoURL: "https://example/x"})
		if find(f, word) != 0 {
			t.Errorf("the command is at %d and not first: the focus would open "+
				"somewhere else", find(f, word))
		}
	})

	t.Run("nothing is refused", func(t *testing.T) {
		f := &flyout{t: tr, dpi: 96}
		f.compose(Status{Fault: FaultCaptureStopped})
		if find(f, word) >= 0 {
			t.Error("the panel offers a settings page with nothing refused")
		}
		if f.leadIndex() >= 0 {
			t.Error("there is a lead command with nothing to lead")
		}
		if p := f.layout(); p.leadY != -1 {
			t.Errorf("the layout keeps a row at %d for a command that is not there", p.leadY)
		}
	})
}
