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
			Fault:      FaultCameraDenied,
			Todo:       "approve this machine",
			TodoAction: "approve",
			TodoURL:    "https://login.example/admin",
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
	// **It is the first command, and the focus is a separate question now.**
	//
	// The two used to be one: `initialFocus` took the first command, so being
	// first *was* taking the focus, and this subtest asserted the first half
	// while being named for the second. Once the mark followed the focus, the
	// focus began preferring the main command — and with a step also waiting
	// this fixture disproves its own name, while still passing. **A test that
	// goes on passing after the rule under it has changed is a test that has
	// stopped asking anything**, and this one had, in the same commit that
	// changed the rule.
	//
	// So both halves are asserted, separately: the command is first, because
	// that is what puts it under the sentence it answers; and the focus opens
	// on it even with a step waiting, because the two are stacked at the top
	// and a selection that started on the second read as skipping the first.
	// The step keeps its weight; the mark is the selection's.
	//
	// **Verified to catch**: with focusIndex preferring the pill again, this
	// fails naming the step.
	t.Run("it is the first command, and the focus opens on it", func(t *testing.T) {
		f := &flyout{t: tr, dpi: 96}
		f.compose(Status{Fault: FaultMicDenied, Todo: "x", TodoAction: "approve",
			TodoURL: "https://example/x"})
		if find(f, word) != 0 {
			t.Errorf("the command is at %d and not first: it would be drawn "+
				"somewhere else", find(f, word))
		}
		i := f.focusIndex()
		if i < 0 || i >= len(f.cmds) {
			t.Fatalf("no command takes the focus: %d", i)
		}
		if i != find(f, word) {
			t.Errorf("the focus opens on %q and not on the topmost command, %q",
				f.cmds[i].label, word)
		}
	})

	// With a step and nothing refused, the step is the only lead, so it is the
	// topmost and the focus is there: the half the old preference covered.
	t.Run("with only a step, the focus is on the step", func(t *testing.T) {
		f := &flyout{t: tr, dpi: 96}
		f.compose(Status{Todo: "x", TodoAction: "approve", TodoURL: "https://example/x",
			HomeURL: "http://192.168.1.42:8080/"})
		i := f.focusIndex()
		if i < 0 || f.cmds[i].style != stylePill {
			t.Errorf("the focus opens at %d and not on the step", i)
		}
	})

	// And with nothing waiting, the first command is the one, which is the half
	// the rule above must not have taken away.
	t.Run("with no step, the focus is on the first command", func(t *testing.T) {
		f := &flyout{t: tr, dpi: 96}
		f.compose(Status{Fault: FaultMicDenied})
		if i := f.focusIndex(); i != 0 {
			t.Errorf("the focus opens at %d and not on the first command", i)
		}
	})

	// **And never on the address, in any state.** Copying is done by pressing
	// it, so it is a real control and it is the panel's first one: opening from
	// the keyboard and pressing Enter there would copy an address instead of
	// opening the monitor. The rule used to live inside `initialFocus`'s single
	// loop; splitting the choice out of it put it in two places, and this asks
	// the one that decides.
	t.Run("never the address", func(t *testing.T) {
		for _, st := range []Status{
			{Fault: FaultMicDenied, HomeURL: "http://192.168.1.42:8080/"},
			{HomeURL: "http://192.168.1.42:8080/"},
			{Fault: FaultMicDenied, Todo: "x", TodoAction: "approve",
				TodoURL: "https://example/x", HomeURL: "http://192.168.1.42:8080/"},
		} {
			f := &flyout{t: tr, dpi: 96}
			f.compose(st)
			i := f.focusIndex()
			if i < 0 || i >= len(f.cmds) {
				t.Fatalf("no command takes the focus: %d", i)
			}
			if f.cmds[i].style == styleText {
				t.Errorf("the focus opens on the address: Enter would copy it "+
					"instead of opening the monitor (fault=%q)", st.Fault)
			}
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

// **A permission refused and a step Tailscale is waiting on answer two
// different sentences, and each now gets its own row above the code.**
//
// Before this, the tunnel's command sat in the column at the bottom, four
// rows from *"approve this machine …"*, with the QR code and the address in
// between — the exact defect the settings command was pulled out of the
// column for, found again by looking at a screenshot with both notices on
// screen at once.
//
// **Verified to catch**: with `lead` removed from the tunnel command, this
// fails saying it is not above the code.
func TestTheTunnelStepIsAnsweredUnderItsOwnLineToo(t *testing.T) {
	tr := &Tray{dictionary: i18n.Open([]string{"en"})}
	settingsWord := tr.t("tray.menu.settings")
	todoWord := tr.t(todoCommand("approve"))

	find := func(f *flyout, label string) int {
		for i, c := range f.cmds {
			if c.label == label {
				return i
			}
		}
		return -1
	}

	f := &flyout{t: tr, dpi: 96, qrBm: 1, qrPx: 120}
	f.compose(Status{
		Fault: FaultMicDenied, Todo: "approve this machine",
		TodoAction: "approve", TodoURL: "https://login.example/admin",
	})

	si, ti := find(f, settingsWord), find(f, todoWord)
	if si < 0 || ti < 0 {
		t.Fatalf("both commands should be offered: settings=%d todo=%d", si, ti)
	}

	leads := f.leadIndices()
	if len(leads) != 2 || leads[0] != si || leads[1] != ti {
		t.Fatalf("leadIndices = %v, want [%d %d]: both stacked, settings first "+
			"because it is composed first", leads, si, ti)
	}

	p := f.layout()
	if len(p.leadYs) != 2 {
		t.Fatalf("layout kept %d lead rows, want 2", len(p.leadYs))
	}
	if !(p.linesY < p.leadYs[0] && p.leadYs[0] < p.leadYs[1] && p.leadYs[1] < p.qrY) {
		t.Errorf("lines at %d, settings at %d, todo at %d, code at %d: both have to "+
			"sit between the sentences and the code, in order",
			p.linesY, p.leadYs[0], p.leadYs[1], p.qrY)
	}

	// And neither is in the column as well, which would draw it twice.
	for _, row := range f.rows() {
		for _, k := range row {
			if k == si || k == ti {
				t.Error("a lead command is in the column too: it would be laid out twice")
			}
		}
	}
}
