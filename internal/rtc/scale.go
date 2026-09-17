package rtc

import "time"

// The resolution scale: when the bits are not enough, fewer pixels are sent.
//
// Below a certain bandwidth, going on taking bits away from the same picture
// does not produce a less sharp picture, it produces a ruined one — the blocks
// become visible and a child's face in dim light stops being readable. With
// fewer pixels, those same bits are enough again.
//
// The criterion is not a table of resolutions with their bitrates, which would
// hold only for today's preset: it is **how many bits each pixel gets**. That
// way the same rule holds if one day the starting point is 1080p or 480p, and
// there is no second list to keep aligned with the first.

// bitsPerPixel is the spend below which it is worth sending fewer pixels.
//
// **A provisional value, and the way to decide it is to measure the quantiser.**
// 0.05 is a deduction from the preset: 2500 kbit/s over 1280x720 at 30 fps makes
// 0.09 bits per pixel, so half of that looks like the point where things start
// to break. It is not, and the numbers say so — at 1200 kbit/s 720p gives QP
// 31.4, that is a clean picture, while that threshold already stepped down at
// 1382.
//
// Coming down when there is no need is a real defect: a size change is visible,
// and seeing it while the picture was still good is worse than not having done
// it. 0.04 puts the 720p threshold at ~1100 kbit/s, where the measured quantiser
// is still around 32.
//
// It stays provisional because that curve was taken on a **still, dark scene**,
// and with movement the QP at the same bitrate goes up. The definitive value
// will come from the same measurement repeated with something moving; in the
// long run the decision should go through the QP and not through a constant,
// which is what libwebrtc does.
const bitsPerPixel = 0.04

// cadenceBitsPerPixel is what a pixel costs on the **cadence** steps.
//
// **Twice `bitsPerPixel`, measured rather than reasoned**: at 5 and at 2 frames
// a second a frame no longer resembles the one before it, so it costs nearly a
// keyframe and the proportional formula asks for half of what is needed. See
// `newScaleGovernor` for the sweep and for what it took to get the number
// right.
const cadenceBitsPerPixel = 0.08

// scaleRiseMargin is how much more than the necessary bandwidth is needed to
// **climb back**.
//
// Without a margin it would oscillate between two steps at every breath of the
// estimate, and a change of resolution is far more visible than a change of
// sharpness: it is the picture changing shape. The same asymmetry as the
// bitrate's, with a wider gap because the error costs more.
const scaleRiseMargin = 1.4

// scaleDwell is how long a step lasts at least before it can be changed.
//
// A network that comes and goes would otherwise produce a succession of changes
// in the shape of the picture, which is more annoying than the small picture.
const scaleDwell = 20 * time.Second

// The quantiser is the real criterion, because it is the only one that knows how
// hard **this** scene is, while a bitrate does not know that at all.
//
// The measurement that shows it, made with somebody really moving in front of
// the camera — the only proof that counts, and the one that overturned a
// calibration made on a still room:
//
//	still scene,   2418 kbit/s -> QP 29.5   p95 32
//	moving scene,  2422 kbit/s -> QP 30.3   p95 32
//	moving scene,  1091 kbit/s -> QP 38.5   p95 42
//
// The first two lines say that at full bitrate movement costs almost nothing.
// The third says that at the same bitrate which on a still room gave QP 31 —
// that is, a perfect picture — the same room with somebody moving goes to 38.5.
// **Seven points at the same bitrate**: no constant of bits per pixel can
// express that, because the bitrate does not know what is happening in the room.
//
// The thresholds are not here, and they are not learnt either: they are a fixed
// number, where three independent measurements put the break. See
// qpthresholds.go.

// scaleSettle is how long to wait, after a size change, before listening to the
// quantiser again.
//
// Rebuilding the encoder produces a few anomalous frames and a keyframe, which
// costs many more bits than the others: judging there would mean reading the
// transient and cascading down to the last step over a fault that is not there.
const scaleSettle = 4 * time.Second

// scaleStep is one step of the scale: a size, a cadence, and the bandwidth below
// which it is no longer worth keeping them.
type scaleStep struct {
	Width, Height int
	FPS           int
	MinKbps       int
}

// scaleGovernor chooses the frame size according to the bandwidth.
//
// Like bitrateGovernor it knows neither the encoder nor the viewers: it takes a
// number and an instant and says what size to sit at. It is written that way so
// it can be checked without a network, which is the only way to test the
// hysteresis — otherwise it would take hours of a network getting worse and
// better on command.
type scaleGovernor struct {
	// baseW, baseH and baseFPS are the size the steps were computed from, kept
	// so that whoever holds this governor can tell it apart from one built on
	// another camera. The steps are fractions of that size: on a different one
	// they match nothing that arrives, and resync deliberately does not invent a
	// size that is not a step.
	baseW, baseH, baseFPS int
	steps                 []scaleStep
	current               int // index into steps; 0 is the full size
	changed               time.Time
	// lastAvail serves to separate a low bandwidth from one that is still
	// climbing, which are the same figure and two opposite things.
	lastAvail int
	// below counts for how many consecutive samples the bandwidth has been under
	// the threshold.
	below int
	// recentQP are the last quantiser readings, and they serve **the veto
	// alone**. The break threshold goes on reading the instant: there one bad
	// second has to be caught straight away. See scaleQPWindow.
	recentQP []int
}

// scaleQPWindow: how many readings the veto is judged on.
//
// **Two GOPs, and without this window the veto does not hold.** The quantiser
// arriving here is the p90 of **one** second and the GOP lasts two, so one
// sample in two contains the keyframe and reads high: with a scene at 29 and its
// keyframes at 35, the veto switches on and off on alternate seconds. On its own
// that would not be serious — a descent needs a low bandwidth anyway — but the
// veto also clears the confirmation counter, and on alternate seconds that
// counter never reaches three: the confirmation becomes unreachable, and only
// the collapse gets through. That is, the veto would break the half it claims to
// leave intact, and precisely in the 33-38 band where the bandwidth has to get
// its say back.
//
// Two readings are enough to remove the bias — with a GOP of two seconds, two
// consecutive seconds contain exactly one keyframe — and four damp the rest.
// Below two, whatever there is gets used, which at the first sample means the
// instant: a degradation, not a jump.
const scaleQPWindow = 4

// noteQP records a reading and returns the window's average, or zero if there is
// nothing.
func (g *scaleGovernor) noteQP(qp int) int {
	if qp > 0 {
		g.recentQP = append(g.recentQP, qp)
		if len(g.recentQP) > scaleQPWindow {
			g.recentQP = g.recentQP[len(g.recentQP)-scaleQPWindow:]
		}
	}
	if len(g.recentQP) == 0 {
		return 0
	}
	s := 0
	for _, v := range g.recentQP {
		s += v
	}
	return s / len(g.recentQP)
}

// scaleConfirmSamples is for how many consecutive samples the bandwidth has to
// stay under the threshold before bringing the scale down, when the shortfall is
// small.
//
// The sampling is one second, so that is three seconds. They are not granted to
// a collapse, where waiting means keeping the picture broken on purpose.
const scaleConfirmSamples = 3

// scaleCadences are the cadences of the bottom steps, in frames per second.
//
// **They are values and not divisors, and that is a change.** They were {2, 5}
// read as divisors, which from a preset of 30 gave 15, 6 and — added outside the
// loop, at `scaleMinFPS` — 2.
//
// **A fraction of the starting cadence is a fraction of a number the camera
// chose.** That cadence is not the preset: `cameraFPS` takes the highest rate
// the device declares at or below it, and falls back to the lowest it has, so a
// webcam at 25 gave rungs of 12 and 5, one at 24 gave 12 and 4, and the 640x480
// kind gives whatever it gives. Those are not designed values — nobody picked
// them, they are not in `cadenceSteps`, and the bottom of the quality ladder
// moved with whatever was plugged in. Two values chosen here are the same two on
// every camera.
//
// **And the first rung removed nothing on the night this program is for.** The
// gate drops a frame only when the wanted cadence is below the incoming one, and
// in the dark auto-exposure brings the camera to 9.8-20 fps, so a step at 15
// passed every frame through and rebuilt the encoder to say so. The declared
// cadence, which chases the measurement, was already at 10 and a cap of 15 sits
// above it: both halves inert, exactly when the step was wanted.
//
// So the ladder is 5 and then 2, which is what the documentation had claimed all
// along by accident — "5 and then 2 a second" was a mistranslation of the
// original *meta', un quinto, e un fondo a 2 fps*, in which the fifth became a
// five and the half vanished. **The wrong sentence described a better design
// than the right one**, and that is the whole reason it went unquestioned for a
// year: it names a monitor more frugal than the one that existed, which is the
// direction this chapter argues for.
//
// Two things fall out of the change. The floor stops being a special case added
// after the loop — 2 is a value in this list like any other, and `scaleMinFPS`
// goes back to being only the floor that nothing may go below. And the two
// cadence lists finally agree: 5 and 2 are both in `cadenceSteps`, while 6 never
// was, so a cap the scale imposes is now always a value the declaration knows.
//
// **2 fps is a frame every half second, not every two seconds.** That reciprocal
// read backwards is what put "a sharp frame every two seconds" into this
// comment.
//
// Below the last resolution step there would otherwise be a ruined 360p held at
// full cadence. A baby monitor has a way out an ordinary video does not: **at
// night the cadence is worth less than the detail** — whether the child is
// there, which way they are turned, whether their face is covered, are questions
// a sharp frame answers and a smoother mush does not. The audio is not touched:
// it stays full for the whole descent, and it is the other half of the warning.
//
// The cadence comes down **only here, at the bottom**, never between one
// resolution step and the next: the resolution scale is tuned and measured, and
// interleaving new steps into it would mean reopening that tuning to gain in a
// case that arises rarely.
//
// **The saving is very sublinear**: in video surveillance 30 fps costs about
// seven times one frame per second, not thirty, because at a low cadence the
// prediction fails and every frame costs almost as much as a keyframe. The
// `MinKbps` proportional to the cadence therefore underestimates these steps.
//
// **And `-fps2` is not the instrument that corrects it**, which is what was
// written here: that flag answers whether the frames really fall. This number is
// how much bandwidth the step needs, read where the quantiser reaches the
// reference, that is a sweep of `-br` at a fixed `-fps` — the way the 0.04 bits
// per pixel was tuned in the first place. And **not on a still room**: the whole
// phenomenon is consecutive frames ceasing to resemble each other as the
// interval grows, and on a scene that does not change they resemble each other
// at any interval, so the cost per frame comes back flattering and confirms the
// formula it was meant to correct.
var scaleCadences = []int{5, 2}

// scaleMinFPS is the floor of the scale.
//
// **Two and not one, and it is a limit taken from outside.** libwebrtc's
// absolute floor is `kMinFramerateFps = 2`, and its `balanced` mode stops well
// before (15 fps below 640x480, roughly our last step). And a browser counts a
// **freeze** when the interval exceeds `max(3 x mean, mean + 150 ms)`: at one
// frame per second a single late frame is already a declared freeze, and the
// watcher's statistics stop telling our saving from a fault.
//
// Video surveillance really does go down to almost no frames, but **driven by
// motion**: on movement "the camera immediately returns to 30 fps". Driven by
// the bandwidth, as here, the same cadence would be reached while the child is
// moving too. So the floor stays cautious.
const scaleMinFPS = 2

// newScaleGovernor builds the scale starting from the preset's size.
//
// The steps are three quarters and half of the starting height: far enough apart
// to really change the cost, close enough not to make it look as though the
// monitor had broken. The full size is always the first step, because it is the
// cap the user chose. Then, on the last size, the cadence steps.
func newScaleGovernor(width, height, fps int) *scaleGovernor {
	if width <= 0 || height <= 0 || fps <= 0 {
		return nil
	}
	g := &scaleGovernor{baseW: width, baseH: height, baseFPS: fps}
	add := func(w, h, f int, bpp float64) {
		if w <= 0 || h <= 0 || f <= 0 {
			return
		}
		// A step that reduces nothing is not a step.
		if n := len(g.steps); n > 0 && g.steps[n-1].Width == w && g.steps[n-1].FPS == f {
			return
		}
		g.steps = append(g.steps, scaleStep{
			Width: w, Height: h, FPS: f,
			MinKbps: int(float64(w*h*f) * bpp / 1000),
		})
	}
	for _, frac := range []float64{1, 0.75, 0.5} {
		add(even16(int(float64(width)*frac)), even16(int(float64(height)*frac)), fps, bitsPerPixel)
	}
	if len(g.steps) == 0 {
		return nil
	}
	// The cadence steps all sit on the last size: everything that could be taken
	// away from the pixels has already been taken away.
	//
	// **Their `MinKbps` is not the same arithmetic, and that was measured.**
	// Below full cadence a frame no longer resembles its predecessor, so it
	// costs nearly a keyframe and the proportional formula asks for half of what
	// is needed. Swept on a Lunar Lake laptop at 640x352, **with somebody moving
	// in front of the camera** — the only scene where the question exists, since
	// on a still room frames resemble each other at any interval and the formula
	// comes back confirmed. Reading the bitrate each cadence needs to hold a
	// given quantiser:
	//
	//	qp   30 fps   5 fps   2 fps  |  bits per pixel, and the ratio
	//	32      600     185      80  |  0.089   0.164 (1.9x)   0.178 (2.0x)
	//	33      507     150      70  |  0.075   0.133 (1.8x)   0.155 (2.1x)
	//	34      413     133      60  |  0.061   0.118 (1.9x)   0.133 (2.2x)
	//	35      320     117      56  |  0.047   0.104 (2.2x)   0.125 (2.6x)
	//
	// **The ratio is what transfers, not the absolute figures.** Every row is
	// inflated against the 0.04 constant, because that was tuned on a still
	// scene and this one moves — which this file already says about it. What is
	// stable is the column on the right: **about twice**, at every quantiser
	// level and at both cadences. Hence one coefficient rather than two numbers.
	//
	// **What it costs to get wrong is the climb back, not the descent.** Too low
	// a figure makes the bandwidth reluctant to bring us onto these steps, which
	// is harmless; it also lets the scale leave 2 fps as soon as 40% more than
	// the step above claims is available — 63 kbit/s against a real need around
	// 150. The picture then breaks, the quantiser sends it back down, and that
	// is an oscillation at the bottom of the scale, where there is least to look
	// at.
	//
	// **And the first answer to this was 5.5x, which was an artefact.** The full
	// cadence had been read from the report's mean quantiser and the two low
	// ones from the median of the per-second p90s — two statistics five points
	// apart, compared as though they were one. It was caught by re-running the
	// full cadence through the same road as the others, and it is the reason the
	// numbers above come from one instrument and one session. **A ratio between
	// two quantities measured differently is not a ratio.**
	//
	// **The whole ladder is built by this one loop**, floor included, and that is
	// worth keeping: while the floor was a step added after it, reading the loop
	// gave five sixths of the answer and stopping there was a mistake somebody
	// really made. What the ladder is remains written out as numbers in
	// `TestTheLadderIsWrittenOut`, because a property test cannot be read as a
	// list and the list is what a reader wants.
	last := g.steps[len(g.steps)-1]
	for _, f := range scaleCadences {
		if f < last.FPS && f >= scaleMinFPS {
			add(last.Width, last.Height, f, cadenceBitsPerPixel)
		}
	}
	return g
}

// even16 rounds to multiples of 16, which is the size of the H.264 macroblock. A
// size that does not fit forces the encoder to crop, and the crop is another
// field of the SPS that can go wrong.
func even16(v int) int { return v / 16 * 16 }

// atFullSize says whether the scale is at the first step, that is whether the
// picture is the one the user asked for.
//
// The quality loop reads it, and it needs it for one reason: the saving only
// makes sense here. Below, the extra bits buy pixels instead of buying nothing —
// see quality.go. A scale that is not there is full size by definition: there is
// no step below to sit on.
func (g *scaleGovernor) atFullSize() bool { return g == nil || g.current == 0 }

// builtOn says whether this scale was computed from that size.
//
// A scale that is not there was built on nothing, so any real size differs from
// it: that is what makes the first build and a rebuild the same line of code.
func (g *scaleGovernor) builtOn(w, h, fps int) bool {
	return g != nil && g.baseW == w && g.baseH == h && g.baseFPS == fps
}

// release forgets the last session's judgements, keeping the step.
//
// With no viewers `target` is not called at all, so the quantiser window, the
// last estimate and the shortfall count would stay those of whoever left: the
// first tick of whoever arrives ten minutes later would be decided by a scene
// that no longer exists. It is the same reason as `qualityGovernor.release` and
// the window over the throughput.
//
// **The step is not touched**, and that is the distinction that matters: that is
// not a judgement, it is what the pipeline is sending now. Clearing it would mean
// believing we are at full size while the reduced pixels go onto the network —
// the defect `resync` exists to correct.
func (g *scaleGovernor) release() {
	if g == nil {
		return
	}
	g.recentQP = g.recentQP[:0]
	g.lastAvail = 0
	g.below = 0
}

// resync realigns the scale with what the pipeline is really sending.
//
// It returns the new step and whether we moved. Zero and false mean "all in
// order", or "not known": a size matching no step is not invented.
//
// **The governor is not the authority on what is coming out**, and it is the one
// place where this could stay hidden for ever: `target` commands only when the
// step **changes**, so a lost request produces no second attempt. The two ways of
// losing it are both real — the pipeline giving up on the format (a camera or an
// encoder refusing it, and there a line of log does exist) and the **capture
// restart**, which starts again from the preset and tells nobody.
//
// It waits out the settling window, which already serves to avoid judging a
// just-rebuilt encoder: inside that, the old size is simply a request not yet
// applied.
func (g *scaleGovernor) resync(w, h, fps int, now time.Time) (int, bool) {
	if g == nil || len(g.steps) == 0 || w <= 0 || h <= 0 || fps <= 0 {
		return 0, false
	}
	if !g.changed.IsZero() && now.Sub(g.changed) < scaleSettle {
		return 0, false
	}
	if cur := g.steps[g.current]; cur.Width == w && cur.Height == h && cur.FPS == fps {
		return 0, false
	}
	for i, s := range g.steps {
		if s.Width == w && s.Height == h && s.FPS == fps {
			g.current = i
			g.recentQP = g.recentQP[:0]
			// We have just moved, so we keep quiet for the usual window: the
			// step was not chosen by us and there is nothing to judge about an
			// encoder that has just restarted.
			g.changed = now
			return i, true
		}
	}
	return 0, false
}

// target returns the size to impose and whether it changed.
//
// availKbps <= 0 means the congestion control does not have enough feedback yet:
// nothing is touched, because the absence of an estimate is not an estimate of
// zero.
//
// maxBits says no more bits can be bought. While it is false, a high quantiser is
// cured by spending more, not by taking pixels away.
//
// credible says whether availKbps may **bring the picture down**, and the
// distinction cost the small picture for a whole session:
//
//   - **coming down it is worth nothing**: the scale read the 1200 kbit/s we had
//     just imposed on ourselves and went down to 960 without anybody having
//     measured the network;
//   - **climbing it is worth a great deal**, because an echo is a **lower
//     bound**. If gcc declares 2696 while the encoder produces 35, that there is
//     room for more pixels is not in doubt.
//
// Passing zero in both cases lets the scale come down and never climb back: with
// the saving switched on the throughput always sits well below the cap, so the
// estimate is **never** credible and the function would exit at once.
func (g *scaleGovernor) target(availKbps int, credible bool, qp int, lim qpLimits, maxBits bool, now time.Time) (w, h, fps int, changed bool) {
	qpKnown := lim.known && qp > 0
	if g == nil || len(g.steps) == 0 {
		return 0, 0, 0, false
	}
	cur := g.steps[g.current]

	// The veto's window is fed on every turn, before any exit: it is the only way
	// for it to always contain the same number of keyframes.
	qpVeto := 0
	if qpKnown {
		qpVeto = g.noteQP(qp)
	}

	// Straight after a size change nothing is judged: the encoder has just been
	// rebuilt and its first frames say nothing about the scene.
	settling := !g.changed.IsZero() && now.Sub(g.changed) < scaleSettle

	// **The quantiser commands the descent**, because it is the only one that
	// knows how hard the scene is now. The bandwidth says how much gets through,
	// not whether it is enough: measured, at 1100 kbit/s a still room sits at QP
	// 31 and the same room with somebody moving sits at 38.5.
	//
	// It comes down **one** step at a time and then looks again: unlike the
	// bandwidth, the QP does not say by how much we are outside, only that we
	// are. A bandwidth collapse goes on coming down in one go by its own road,
	// further below.
	if qpKnown && !settling && maxBits && qp >= lim.breakAt && g.current < len(g.steps)-1 {
		g.current++
		g.changed = now
		g.recentQP = g.recentQP[:0]
		s := g.steps[g.current]
		return s.Width, s.Height, s.FPS, true
	}

	if availKbps <= 0 {
		return cur.Width, cur.Height, cur.FPS, false
	}

	// The asymmetry is the bitrate's, and for the same reason: **it comes down at
	// once and all the way, it climbs back slowly and one step at a time.**
	//
	// Coming down in stages looks prudent and is not. The bitrate drops in one go
	// — it has to, or packets go on being lost — and while the pixels stay as
	// many as they were the picture is blocky: a staged descent therefore
	// guarantees a window of blockiness as long as the stages still to come. On a
	// real collapse, 2500 → 300, that was a good twenty seconds of ruined picture
	// produced by a rule of ours, not by the network.
	//
	// The price is paid on the other side: a network hole of a few seconds leads
	// to the small picture, and getting back up takes the dwell and the margin.
	// It is the right direction to be wrong in — a smaller picture can be
	// watched, a blocky one cannot.
	// An estimate still climbing is not a low bandwidth: it is a bandwidth not
	// yet discovered.
	//
	// gcc starts cautious and takes some ten seconds to reach the true value —
	// measured, from 700 declared to the real 2500. The eight-second warm-up
	// covers the bitrate but is not enough here, because the climb passes right
	// through the 720p threshold: on wifi the estimate went through ~1500, the
	// scale came down to 960, and a few seconds later climbed back. A change in
	// the shape of the picture every time somebody opens the page.
	rising := availKbps > g.lastAvail
	g.lastAvail = availKbps

	// A small shortfall is confirmed, a large one is not.
	//
	// It is the usual asymmetry applied to urgency: grazing the threshold is
	// almost always noise or settling, and reacting at once does more damage than
	// it repairs; halving it is a collapse, and there waiting means keeping the
	// picture broken on purpose.
	// The shortfall count is kept **only** on credible estimates. Counting them on
	// the echoes too would mean accumulating in silence for the whole time
	// bandwidth is being saved, and then making the picture fall in the instant
	// credibility returns — a descent decided by minutes in which nobody had
	// measured the network.
	// **The quantiser has a veto over the descent for bandwidth**, and without
	// this line the design of this file is written by halves. The rule is already
	// in the chapter that decided the thresholds — the QP brings it down, the
	// bandwidth stays the constraint for climbing — and the bits per pixel are
	// there as a **fallback** for encoders that do not declare the quantiser. In
	// the code the bandwidth would bring it down anyway, even with a good reading
	// in hand.
	//
	// What that cost, measured on AMD with two viewers on the home network and a
	// still room: four descents in twenty minutes, all with `qp` between 27 and
	// 30 against a break threshold of 38, losses at 0.0% and the real bandwidth
	// re-measuring at 2696 kbit/s a few seconds later. From outside, a picture
	// that shrinks and takes a minute to come back — twenty seconds of dwell per
	// step — while it had never been in danger.
	//
	// The threshold is the **climb** one, not the break one, and the two halves
	// hold together: if the picture is good enough to authorise one more step, it
	// is too good to lose one. Between 33 and 38 the bandwidth gets its say back,
	// which is the band where the picture really does start to suffer and the fast
	// road of the collapse is needed.
	//
	// **What is not lost is the multiple descent on a real collapse.** There the
	// quantiser rises in a couple of seconds — measured, from 28 to 37 — and from
	// that moment this veto is gone and the bandwidth jumps to the steps needed in
	// one go. A softening transient is paid for, which is the right direction to
	// be wrong in against a minute of small picture on every still room.
	//
	// **The veto does not look at the settling, and that is a choice.** Inside
	// that window the quantiser carries the keyframe of a just-rebuilt encoder, so
	// it is high and the veto does not fire: the bandwidth keeps its say exactly
	// as before. Adding a `!settling` looks prudent and breaks an older invariant
	// — no wait may hold back a descent, because holding it back would mean
	// keeping the picture broken on purpose. TestTheScaleDoesNotHoldBackDescents
	// says so, and that is how that rule survives this one.
	pictureHealthy := qpVeto > 0 && qpVeto <= lim.climbAt
	bandwidthMayDescend := !pictureHealthy

	// The shortfalls are counted only while the bandwidth has standing to act on
	// them. Otherwise they accumulate in silence for a whole still room, and make
	// the picture fall in the instant the quantiser grazes the threshold.
	if !credible || !bandwidthMayDescend {
		g.below = 0
	} else if availKbps < cur.MinKbps {
		g.below++
	} else {
		g.below = 0
	}
	// A collapse is a **fall**, so it does not exist while the estimate is
	// climbing. Without this condition the scale fell to the last step on the
	// first sample of every viewer, because gcc starts low: the figure of a
	// bandwidth that has yet to grow and that of one that has just collapsed are
	// identical, and only where it comes from tells them apart.
	collapse := credible && !rising && availKbps*2 < cur.MinKbps
	descend := bandwidthMayDescend &&
		(collapse || (g.below >= scaleConfirmSamples && !rising))

	want := g.current
	if availKbps < cur.MinKbps && descend {
		for want < len(g.steps)-1 && availKbps < g.steps[want].MinKbps {
			want++
		}
	} else if g.current > 0 {
		// It climbs **one** step at a time, and only if the bandwidth is enough
		// with a margin for the one above — not for the one we are on.
		//
		// And, if the quantiser is there, only if there is quality margin too:
		// climbing with the encoder already at its limit means arriving above the
		// break threshold and coming straight back down, that is making the
		// picture change shape twice for nothing. The bandwidth alone cannot see
		// that, because it does not know how hard the scene is.
		up := g.steps[g.current-1]
		bandwidthEnough := float64(availKbps) >= float64(up.MinKbps)*scaleRiseMargin
		qualityHasMargin := !qpKnown || (!settling && qp <= lim.climbAt)
		if bandwidthEnough && qualityHasMargin {
			want = g.current - 1
		}
	}
	if want == g.current {
		return cur.Width, cur.Height, cur.FPS, false
	}
	// The dwell holds **only for climbing back**: that is where it oscillates,
	// because that is where a lucky estimate for an instant can bring it back up
	// too soon. Holding back a descent would instead mean keeping the picture
	// broken on purpose.
	if want < g.current && !g.changed.IsZero() && now.Sub(g.changed) < scaleDwell {
		return cur.Width, cur.Height, cur.FPS, false
	}

	g.current = want
	g.changed = now
	// The readings from the previous size do not judge the new one: it is the
	// same reason as the window over the throughput.
	g.recentQP = g.recentQP[:0]
	s := g.steps[want]
	return s.Width, s.Height, s.FPS, true
}
