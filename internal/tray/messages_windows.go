//go:build windows

package tray

import "time"

// How a notification-area message is read, and where the menu opens from.
//
// **The shape of the message depends on the contract declared with
// `NIM_SETVERSION`**, and the two shapes cannot be told apart by looking at the
// values: a program that reads the wrong one gets no error, it simply stops
// responding to the click. It is the family of the GUID that does not protest,
// so the two readings live here, apart from the rest and under test.
//
//   - **version 0** (what you get by declaring nothing): `lParam` is the event
//     and nothing else, `wParam` is the icon's identifier. The pointer position
//     is not there, and has to be asked of `GetCursorPos`.
//   - **version 4**: `lParam` carries the event in the low word and the
//     identifier in the high one, and `wParam` carries **the screen
//     coordinates**, x in the low word and y in the high one.

// eventFromLParam pulls the event out of whichever shape is in force.
func eventFromLParam(lparam uintptr, v4 bool) uint32 {
	if v4 {
		return uint32(lparam & 0xFFFF)
	}
	return uint32(lparam)
}

// spot is the point to open the menu at, when we know it.
//
// `Known` separates "the screen, at coordinates X,Y" from "I do not have it":
// they are two different things, and collapsing them onto a (0,0) would open
// the menu in the top left corner, which is a plausible place and the wrong one.
type spot struct {
	X, Y  int32
	Known bool
}

// pointFromWParam reads the screen coordinates, which exist only with
// version 4.
//
// **The two words are signed.** On a second monitor placed to the left of the
// main one the screen coordinates are negative, and reading them unsigned would
// send the menu sixty-five thousand pixels away — that is, off every screen,
// with the menu not appearing and nothing to say so.
func pointFromWParam(wparam uintptr, v4 bool) spot {
	if !v4 {
		return spot{}
	}
	return spot{
		X:     int32(int16(wparam & 0xFFFF)),
		Y:     int32(int16((wparam >> 16) & 0xFFFF)),
		Known: true,
	}
}

// iconEvent turns the event into one of the two things that can be done.
//
// The two contracts send different events for the same gesture, and both are
// accepted rather than chosen on `v4`: it costs one more line and removes a way
// of being wrong, because an event we do not expect here does nothing and
// nobody says so.
//
//   - open the monitor: `NIN_SELECT` and `NIN_KEYSELECT` with version 4, the
//     left button release and the double click with version 0;
//   - open the menu: `WM_CONTEXTMENU` with version 4, the right button release
//     with version 0.
//
// `NIN_KEYSELECT` arrives **twice** for a single Enter, and that is documented:
// it is not filtered, because opening the monitor twice opens one tab, and the
// defence against the double click is already in `activatedAt`.
func (t *Tray) iconEvent(ev uint32, where spot) {
	switch ev {
	case ninSelect, ninKeySelect, wmLButtonUp, wmLButtonDblClk:
		if t.activatedAt(time.Now(), doubleClickTime()) {
			t.open(t.status().OpenURL)
		}
	case wmContextMenu, wmRButtonUp:
		// **The same collapse as the left button**, and for the same reason: a
		// right click sends two of the messages we accept, and the panel is a
		// toggle. See panelAt.
		if t.panelAt(time.Now(), doubleClickTime()) {
			t.showFlyout(where)
		}
	}
}
