package encoder

// Settings are the parameters the video encoder is configured with.
//
// They stay a type of their own rather than fields scattered through the
// pipeline configuration because a preset is the user's choice, while the way
// to obtain it is an engine detail: this says what is wanted, not how it is
// done.
type Settings struct {
	Width, Height int
	FPS           int
	BitrateKbps   int
	// KeyframeSecs is the distance between keyframes.
	//
	// It reaches the encoder through `pipeline.Config.GOPSeconds`, which both
	// callers now fill from here. They used to write the literal 2 while this
	// field also held 2, so it was a knob that moved nothing and no reading
	// could tell the two shapes apart — and `deadcode` cannot report a struct
	// field. The one place that read it, pat-capture's report of how many
	// keyframes the run should have produced, would have gone on expecting the
	// old distance and accused the capture of its own arithmetic.
	//
	// With Media Foundation a keyframe can also be asked for on demand, in
	// response to a PLI for instance, so this value is the background rhythm
	// rather than the only way to get one.
	KeyframeSecs int
}

// DefaultSettings is the starting configuration, refined by the preset.
func DefaultSettings() Settings {
	return Settings{
		Width:        1280,
		Height:       720,
		FPS:          30,
		BitrateKbps:  2500,
		KeyframeSecs: 2,
	}
}
