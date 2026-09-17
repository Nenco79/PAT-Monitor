//go:build windows

package audio

import "golang.org/x/sys/windows"

// Windows audio endpoints are **per session**, and that alone explains the most
// baffling fault this monitor can present on a server: a perfect camera, no
// microphone at all, and hardware in order.
//
// Inside a Remote Desktop session the machine's physical audio devices are not
// exposed: in their place there are the "Remote Audio" endpoints, and
// **microphone** redirection is off by default — and when it is turned on it
// exposes the microphone of whoever connected, not the server's. Video capture
// does not follow that path, so the webcam keeps working: the two halves behave
// in opposite ways, which is why it looks like a defect of ours.
//
// The hardware enumeration is not the one that matters here, and it is the
// misleading one: Win32_SoundDevice lists the machine's sound devices as
// usual, because that is the **hardware**, while EnumAudioEndpoints(eCapture,
// DEVICE_STATE_ACTIVE) returns nothing.
//
// The practical consequence counts for more than the cause: **a monitor started
// inside an RDP session stays deaf for its whole life**, even after
// disconnecting, because the process does not change session. It has to be
// launched from the console.

const smRemoteSession = 0x1000

var procGetSystemMetrics = windows.NewLazySystemDLL("user32.dll").NewProc("GetSystemMetrics")

// remoteSession says whether this process is running inside a Remote Desktop
// session.
//
// SM_REMOTESESSION describes the session of the **process asking**, not the
// state of the machine: which is exactly the right question, because it is the
// process's session that decides which audio endpoints exist.
func remoteSession() bool {
	r, _, _ := procGetSystemMetrics.Call(smRemoteSession)
	return r != 0
}
