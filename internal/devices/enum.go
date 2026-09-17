// Package devices lists the cameras Windows can see.
//
// The enumeration itself is Media Foundation's (see mf_windows.go), which
// answers in structured form.
//
// Microphones do not come through here: internal/audio opens them straight
// from WASAPI, so there is no video/audio split to make in this package.
package devices

import (
	"errors"
	"strings"
)

// Device is a camera.
type Device struct {
	// Name is the human-readable name shown to the user. It may hold
	// non-ASCII characters (e.g. "Tecnologia Intel® Smart Sound").
	Name string
	// link is the symbolic path Media Foundation opens the device by.
	//
	// It is the identifier that stays the same across restarts; the friendly
	// name is not, because two cameras of the same model share it.
	link string
}

// New builds a Device. It lives here rather than at the literals because the
// symbolic path is unexported: whoever needs it goes through Link.
func New(name, link string) Device { return Device{Name: name, link: link} }

// Link is the symbolic path to hand Media Foundation to open the device.
func (d Device) Link() string { return d.link }

// IsLikelyIR spots infrared cameras (Windows Hello). They are kept out of the
// user's choice: as ordinary video they are unusable, and they are often
// reserved for face recognition anyway.
func (d Device) IsLikelyIR() bool {
	n := strings.ToLower(d.Name)
	for _, s := range []string{" ir ", "ir camera", "infrared", "hello"} {
		if strings.Contains(n, s) || strings.HasPrefix(n, "ir ") {
			return true
		}
	}
	return false
}

// Cameras drops the infrared cameras from the list.
//
// **It filters before Pick matches, so a hand-pinned infrared camera reads as an
// absent one.** A `camera_device_id` naming a Windows Hello sensor that is
// plugged in falls into the same branch as one that has been unplugged:
// `fallback` comes back true, and the alert used to say *"the camera you chose
// is not connected"* about a camera sitting in the machine. The page cannot
// produce that configuration — its own list is this one — so it takes a
// hand-edited `config.yaml`.
//
// **The behaviour is right and the sentence asserted a cause.** Opening an
// infrared sensor as ordinary video gives an unusable picture, so falling back
// to a real camera is the better answer; what could not stand is a banner saying
// *not connected* about a camera sitting in the machine. And the general form of
// that is the rule this file already keeps about faults: **it names a state, not
// a cause.** What is known here is that the chosen camera cannot be used; that
// the cable is out is one way for it to be true and not the only one.
//
// So all four of the strings that describe this state say *not available* —
// `viewer.alert.camera-other`, `tray.note.camera-other`,
// `viewer.stats.camera-gone` and `err.no-such-camera`, in both languages.
// Changing one of them alone was tried and is worse than either wording used
// throughout: it put *not available* in the banner directly above *not
// connected* in the picker, on the same screen, about the same camera.
//
// The microphone's equivalents are deliberately left saying *not connected*:
// there is no filter on that side, so an endpoint that is not in the list really
// is not there. And the log line in internal/pipeline keeps its own wording — it
// is read by whoever debugs, and it is quoted in CLAUDE.md as measured output.
func Cameras(all []Device) []Device {
	var out []Device
	for _, d := range all {
		if !d.IsLikelyIR() {
			out = append(out, d)
		}
	}
	return out
}

// Pick reports which camera to open, and whether it is the one that was asked
// for.
//
// **It does not refuse because the chosen camera is absent**, and that is the
// decision rather than a convenience. The precedent is the microphone, where
// refusing would leave the monitor mute all night with a working device under
// its nose; here the same argument holds and the value of the fallback is
// higher, because there is no second road at all — Media Foundation does not
// fall back on its own the way WASAPI does, so a refusal is a baby monitor
// that will not switch on.
//
// **What video risks and audio does not is the wrong room.** The fallback may
// be watching the kitchen while the chosen camera watched the cot, and at three
// in the morning, on a phone, a dark room looks like a dark room. So falling
// back is only half the answer: the other half is that it gets **declared**,
// and not as a line in the details — whoever consumes this raises an alert,
// because "you are not watching the room you chose" is something that is wrong
// now.
//
// fallback is true only when a link was asked for and is not among the
// connected cameras: nothing chosen is nothing missing.
func Pick(all []Device, wantLink string) (cam Device, fallback bool, err error) {
	cams := Cameras(all)
	if len(cams) == 0 {
		return Device{}, false, errors.New("no usable webcam found")
	}
	// Comparing case-insensitively is not tidiness: Windows hands the same
	// symbolic link back in different cases depending on who is asked, so a
	// link copied out of pat-diag and one read here can differ by nothing else.
	if wantLink != "" {
		for _, c := range cams {
			if strings.EqualFold(c.link, wantLink) {
				return c, false, nil
			}
		}
		return cams[0], true, nil
	}
	return cams[0], false, nil
}

// Names lists what there was to choose from, which is the one thing that turns
// "not connected" into something the reader can act on.
func Names(cams []Device) string {
	names := make([]string, 0, len(cams))
	for _, c := range cams {
		names = append(names, c.Name)
	}
	return strings.Join(names, ", ")
}
