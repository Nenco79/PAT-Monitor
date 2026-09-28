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
// no rescaling. **The first is the default**, the one `config.Default` names,
// and this is the only place its numbers are written: the pipeline's zero-value
// defaults and the instruments' flags read them from here rather than carrying
// a copy of their own.
var Presets = []Preset{
	{"high", 1280, 720, 30, 2500},
	{"medium", 960, 540, 30, 1200},
	{"low", 640, 360, 15, 500},
}

// KeyframeSecs is the distance between keyframes, in seconds.
//
// It reaches the encoder through `pipeline.Config.GOPSeconds`, which both
// callers fill from here. They used to write the literal 2 while a settings
// field also held 2, so it was a knob that moved nothing and no reading could
// tell the two shapes apart. The other place that reads it, pat-capture's
// report of how many keyframes the run should have produced, would have gone
// on expecting the old distance and accused the capture of its own arithmetic.
//
// With Media Foundation a keyframe can also be asked for on demand, in response
// to a PLI for instance, so this value is the background rhythm rather than the
// only way to get one.
const KeyframeSecs = 2

// PresetByName looks a preset up by name.
func PresetByName(name string) (Preset, error) {
	for _, p := range Presets {
		if p.Name == name {
			return p, nil
		}
	}
	return Preset{}, fmt.Errorf("unknown quality preset %q", name)
}
