//go:build windows

package tray

import "testing"

// The two shapes of the message cannot be told apart by looking at the values,
// so the only way of noticing that they have been swapped is to ask here.
func TestTheIconMessageIsReadInTheFormThatWasNegotiated(t *testing.T) {
	// With version 4 the event is in the low word and the icon's identifier in
	// the high one: reading the whole of lParam would give an enormous number
	// matching no event, and the menu would not open.
	const iconOne = 1 << 16
	if got := eventFromLParam(iconOne|wmContextMenu, true); got != wmContextMenu {
		t.Errorf("v4: event = %#x, wanted %#x", got, wmContextMenu)
	}
	if got := eventFromLParam(iconOne|ninSelect, true); got != ninSelect {
		t.Errorf("v4: event = %#x, wanted %#x", got, ninSelect)
	}

	// With version 0 lParam **is** the event, and there is no high word to take
	// off.
	if got := eventFromLParam(wmRButtonUp, false); got != wmRButtonUp {
		t.Errorf("v0: event = %#x, wanted %#x", got, wmRButtonUp)
	}
}

// The case that goes wrong in silence: a screen to the left of the main one has
// negative coordinates, and unsigned the menu ends up sixty-five thousand
// pixels away — that is, it does not appear, and nobody says so.
func TestAScreenToTheLeftHasNegativeCoordinates(t *testing.T) {
	// x = -40, y = 12, packed the way the shell packs them.
	x, y := int16(-40), int16(12)
	wparam := uintptr(uint32(uint16(x)) | uint32(uint16(y))<<16)

	where := pointFromWParam(wparam, true)
	if !where.Known {
		t.Fatal("with version 4 the position is there")
	}
	if where.X != -40 || where.Y != 12 {
		t.Errorf("position = (%d,%d), wanted (-40,12)", where.X, where.Y)
	}
}

// "I do not have it" is not "the top left corner": they are two different
// things, and collapsing them would open the menu in a plausible, wrong place
// instead of making it ask the cursor.
func TestWithoutTheModernContractThereIsNoPosition(t *testing.T) {
	if where := pointFromWParam(0x000C_0028, false); where.Known {
		t.Error("with version 0 wParam is the icon's identifier, not a point")
	}
}
