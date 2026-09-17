package devices

import (
	"strings"
	"testing"
)

// twoOfTheSameModel is the case the friendly name cannot express, and the whole
// reason the choice travels as a symbolic link: Windows gives both cameras the
// same name, and only the link tells them apart.
func twoOfTheSameModel() []Device {
	return []Device{
		New("HD Pro Webcam C920", `\?\usb#vid_046d&pid_082d#aaa#{guid}\global`),
		New("HD Pro Webcam C920", `\?\usb#vid_046d&pid_082d#bbb#{guid}\global`),
	}
}

// **The link chooses, and the name could not have.** Asking for the second of
// two identically named cameras is a coin toss by name and exact by link.
func TestTheCameraIsChosenByItsLinkAndNotByItsName(t *testing.T) {
	cams := twoOfTheSameModel()

	got, fellBack, err := Pick(cams, cams[1].Link())
	if err != nil {
		t.Fatalf("choosing by link: %v", err)
	}
	if fellBack {
		t.Error("the camera that was asked for was reported as a fallback")
	}
	if got.Link() != cams[1].Link() {
		t.Errorf("opened %q, and the choice was the other camera of the same name", got.Link())
	}
}

// Windows hands the same symbolic link back in different cases depending on who
// is asked, so a comparison that respects case would refuse the camera the
// configuration names — and it would then be declared a fallback while the
// chosen camera sits there.
func TestTheLinkIsComparedWithoutCase(t *testing.T) {
	cams := []Device{New("Integrated Camera", `\?\usb#VID_046D#{GUID}\global`)}

	got, fellBack, err := Pick(cams, `\?\usb#vid_046d#{guid}\global`)
	if err != nil {
		t.Fatalf("the same link in another case was refused: %v", err)
	}
	if fellBack {
		t.Errorf("the same link in another case was taken for another camera: %q", got.Link())
	}
}

// With nothing chosen the first usable one is taken, and **usable excludes the
// infrared sensor**: webcams with Windows Hello expose one, it is unusable as
// ordinary video, and it is often first in the list.
func TestWithNoChoiceTheFirstCameraThatIsNotInfraredIsTaken(t *testing.T) {
	cams := []Device{
		New("Integrated IR Camera", `\?\usb#ir`),
		New("Integrated Camera", `\?\usb#rgb`),
	}

	got, fellBack, err := Pick(cams, "")
	if err != nil {
		t.Fatalf("with no choice: %v", err)
	}
	if fellBack {
		t.Error("nothing was chosen, so nothing was missing: reported as a fallback")
	}
	if got.Name != "Integrated Camera" {
		t.Errorf("opened %q, which is the infrared sensor", got.Name)
	}
}

// **A chosen camera that is not connected does not stop the monitor**, and this
// is the decision rather than a convenience: Media Foundation has no fallback of
// its own, so refusing means a baby monitor that will not switch on because a
// webcam was unplugged.
//
// What it must not do is stay quiet about it: the fallback is reported, because
// the picture that arrives may be of another room.
func TestAChosenCameraThatIsNotConnectedFallsBackAndSaysSo(t *testing.T) {
	cams := twoOfTheSameModel()

	got, fellBack, err := Pick(cams, `\?\usb#gone`)
	if err != nil {
		t.Fatalf("a chosen camera that is gone stopped the choice: %v", err)
	}
	if !fellBack {
		t.Error("fell back on another camera without saying so")
	}
	if got.Link() != cams[0].Link() {
		t.Errorf("fell back on %q instead of the first usable camera", got.Link())
	}
}

// With no camera at all there is nothing to fall back **to**, and that is the
// one case that is an error: the answer is not a camera, so there is nothing to
// hand to whoever opens.
func TestWithNoCameraAtAllThereIsNothingToFallBackTo(t *testing.T) {
	if _, _, err := Pick(nil, `\?\usb#aaa`); err == nil {
		t.Fatal("no camera at all was accepted as a choice")
	}
	// The infrared sensor is not a camera to fall back on either: as ordinary
	// video it is unusable, so a machine with only that one has none.
	if _, _, err := Pick([]Device{New("Integrated IR Camera", `\?\usb#ir`)}, ""); err == nil {
		t.Fatal("the infrared sensor was accepted as a usable camera")
	}
}

// "Not connected" on its own leaves the reader with nothing to do next: what
// turns it into something actionable is the list of what there was.
func TestTheNamesSayWhatThereWasToChooseFrom(t *testing.T) {
	got := Names(Cameras(twoOfTheSameModel()))
	if strings.Count(got, "HD Pro Webcam C920") != 2 {
		t.Errorf("the list does not name both cameras: %q", got)
	}
}
