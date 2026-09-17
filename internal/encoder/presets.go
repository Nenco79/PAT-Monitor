package encoder

import "fmt"

// Preset is a resolution, framerate and bitrate taken together.
//
// It is the starting point the user picks, not the last word: congestion
// control moves inside and below the preset while the capture keeps running.
// The bitrate changes on a running encoder; the size changes by retargeting the
// Source Reader and rebuilding the encoder, which the video does not notice
// either.
type Preset struct {
	Name          string
	Width, Height int
	FPS           int
	BitrateKbps   int
}

// **There is no Label, and that is a decision.** There was one, carrying
// "720p 30fps" and its two neighbours, and nothing read it — no route
// serialises a preset, no page asks for one, and the quality selector composes
// its own words from the catalogue, which is where words belong. It is the pair
// this repository has paid for twice already, the camera entry's `Default` and
// the dead SDP field: **the field cannot break and the comment cannot fail**,
// and `deadcode` reports neither, a struct field not being a function. Here it
// was the worse of the two, because the value was English prose inside a
// multilingual interface: the only thing keeping `540p 30fps` off an Italian
// page was that nobody read it. Whoever wants the sentence composes it from the
// three numbers, which are right here.

// Presets are ordered from the heaviest to the lightest.
//
// The resolutions match modes common webcams expose natively, so capture needs
// no rescaling.
var Presets = []Preset{
	{"high", 1280, 720, 30, 2500},
	{"medium", 960, 540, 30, 1200},
	{"low", 640, 360, 15, 500},
}

// PresetByName looks a preset up by name.
func PresetByName(name string) (Preset, error) {
	for _, p := range Presets {
		if p.Name == name {
			return p, nil
		}
	}
	return Preset{}, fmt.Errorf("unknown quality preset %q", name)
}

// Settings turns the preset into encoder parameters.
func (p Preset) Settings() Settings {
	s := DefaultSettings()
	s.Width, s.Height = p.Width, p.Height
	s.FPS = p.FPS
	s.BitrateKbps = p.BitrateKbps
	return s
}
