//go:build windows

package wincom

import "golang.org/x/sys/windows"

// NarrowDLLSearch takes the executable's own folder out of the places Windows
// looks for a DLL loaded by bare name.
//
// **Every DLL this program loads itself already comes from System32**:
// NewLazySystemDLL everywhere, Go's own list for the runtime, and the
// KnownDLLs. What was left was the default search order for loads made inside
// system components on our behalf — the shell during ShellExecute, the helpers
// Media Foundation and WASAPI activate — and that order starts from the
// executable's folder. The release is a ZIP, and a ZIP is unpacked and run from
// Downloads, which is where a DLL somebody else chose to save would be waiting.
// No such load was found; this is the standard answer to the class, at the cost
// of one call, and the one thing it could break is a component relying on the
// folder it is removing.
//
// Run with it on the AMD machine: the camera, the hardware encoder
// (AMDh264Encoder), the microphone in exclusive raw mode, the stream to a
// browser, the audio output for talk-back and the tray's ShellExecute on a
// folder, a page and a settings address all worked as before. On an RTX 4080,
// driver 32.0.16.1714, pat-diag activated and configured the NVIDIA H.264
// Encoder MFT on Direct3D, and ten seconds of pat-capture encoded 136 frames of
// a real camera through it. **The Intel encoder was not run**, and a vendor
// transform that loads a DLL of its own by bare name from its own folder is the
// case to look for if it stops opening.
//
// **The tools call it too**, first thing in main, because pat-diag,
// pat-capture and pat-wasapi answer for the monitor: an encoder they open with the old search
// order and the monitor cannot open with the new one would be a diagnosis of a
// different program.
func NarrowDLLSearch() error {
	return windows.SetDefaultDllDirectories(windows.LOAD_LIBRARY_SEARCH_SYSTEM32)
}
