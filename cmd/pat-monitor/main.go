// PAT Monitor: captures webcam and microphone, encodes them with whatever
// hardware encoder is available, and serves them over WebRTC to a browser.
//
// The binary produced is called pat-monitor.exe: build.ps1 imposes that, because
// a main package is always called main and the folder alone is not enough to say
// so.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"math"
	"net"
	"net/http"
	_ "net/http/pprof"
	"net/url"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/pion/webrtc/v4"
	"golang.org/x/sync/errgroup"
	"golang.org/x/term"

	"patmonitor/internal/alerts"
	"patmonitor/internal/applog"
	"patmonitor/internal/audio"
	"patmonitor/internal/config"
	"patmonitor/internal/detect"
	"patmonitor/internal/devices"
	"patmonitor/internal/encoder"
	"patmonitor/internal/guard"
	"patmonitor/internal/i18n"
	"patmonitor/internal/media"
	"patmonitor/internal/pipeline"
	"patmonitor/internal/record"
	"patmonitor/internal/rtc"
	"patmonitor/internal/server"
	"patmonitor/internal/tray"
	"patmonitor/internal/tunnel"
	"patmonitor/internal/update"
	"patmonitor/internal/version"
)

var (
	configPath  = flag.String("config", "", "path of the configuration file")
	listenAddr  = flag.String("listen", "", "listen address (overrides the configuration)")
	setPassword = flag.Bool("set-password", false, "set the password and exit")
	showConfig  = flag.Bool("show-config", false, "show the active configuration and exit")
	verbose     = flag.Bool("v", false, "debug logging")
	showVersion = flag.Bool("version", false, "show the version and exit")

	// Replaces the microphone with a tone. It stays a command-line option and
	// does not go into the configuration: it is a test instrument, and a baby
	// monitor that transmits a tone instead of the room must not be able to stay
	// that way by forgetfulness.
	audioTestTone = flag.Bool("audio-test-tone", false,
		"send a test tone instead of the microphone (diagnostics)")

	// Forces a bitrate different from the preset's, **without** changing its
	// resolution. It serves to test congestion control and the resolution scale
	// in cases the presets do not cover — 720p at a low bitrate is the
	// combination in which the quantiser rises and the scale has to come down —
	// and for the same reason as the test tone it does not go into the
	// configuration: it is declared in the log, so a monitor left like that by
	// forgetfulness can be seen.
	bitrateOverride = flag.Int("bitrate", 0,
		"force the video bitrate in kbit/s, keeping the preset resolution (diagnostics)")

	// **The fault simulation exists so that the alert channel can be tested
	// without a real fault.** Waiting for something to break in order to check
	// that the warning works would mean checking it on the night it mattered.
	// The codes accepted are those of `alerts`, separated by commas; they come on
	// and go off every twenty seconds, so the test covers the **return** too,
	// which is the half that gets forgotten.
	simulateFault = flag.String("simulate-fault", "",
		"diagnostics: alternate the given faults ("+strings.Join(simulatableCodes(), ", ")+")")

	// A panic raised on purpose, for the same reason as the line above: what it
	// checks is what the monitor **does** about one, and that cannot be checked
	// on the night it happens.
	//
	// The four places are the four different answers, and they are worth being
	// able to tell apart on a machine nobody here has seen:
	//
	//	video      the capture restarts, and the picture comes back
	//	audio      the microphone reopens, and the camera never notices
	//	accessory  a line in the log, and nothing else changes
	//	now        nothing catches it: the process ends, and the trace has to
	//	           be in the log file — which is the whole of CaptureCrashes,
	//	           and the one thing no test in this repository can assert,
	//	           because it needs a process with no console of its own
	simulatePanic = flag.String("simulate-panic", "",
		"diagnostics: raise a deliberate panic (video, audio, accessory, now)")

	// The profiler, off unless explicitly asked for.
	//
	// **It exists because "it uses too much" is not a diagnosis.** This program's
	// cost could be narrowed down by exclusion — not the pixels, not the logs,
	// not the viewer — but narrowing by exclusion says where it is **not**, and
	// the next step is to look.
	//
	// It sits behind a flag and is never on by default: `net/http/pprof` exposes
	// the stacks of every goroutine and lets whoever asks run a profile, that is,
	// it is at once an information leak and a way of making the machine work from
	// outside. On a program that publishes an address on the Internet that is not
	// something to leave on.
	pprofAddr = flag.String("pprof", "",
		"start the profiler on this loopback address, e.g. localhost:6060 (diagnostics)")
)

func main() {
	flag.Parse()

	// Before anything else, and without touching anything: whoever asks for the
	// version is asking for it in order to copy it into a report, and must be
	// able to do that even while another instance is running.
	if *showVersion {
		fmt.Println(version.Product + " " + version.Full())
		return
	}

	// A badly written diagnostic flag is reported at once and the program exits:
	// discovering it later means a test that tests nothing, with somebody waiting
	// for an alert that will never arrive.
	if err := validateSimulation(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(2)
	}

	level := slog.LevelInfo
	if *verbose {
		level = slog.LevelDebug
	}

	// The path is needed before the log, which goes next to the configuration. If
	// it cannot be determined nothing is done here: run will report it with its
	// own error, where there is already a logger to say it.
	cfgPath := *configPath
	if cfgPath == "" {
		if p, err := config.DefaultPath(); err == nil {
			cfgPath = p
		}
	}

	// The log goes to a file **and** to the console.
	//
	// To a file because the monitor is meant to live without a console — with the
	// tray it is compiled with -H=windowsgui — and without a file the diagnosis
	// would have nowhere left to write. It has happened, in small: a process that
	// exited at night left only a switched-off webcam, and the cause had to be
	// deduced instead of read.
	//
	// To the console because while a console is there it is the most direct way
	// of watching what happens. **The two destinations must not be able to
	// silence each other**, which is the same rule as the separate lives of audio
	// and video, applied to the log.
	//
	// `io.MultiWriter(os.Stderr, logFile)` writes in order and **returns at the
	// first error**: compiled with `-Gui`, that is with no console, `os.Stderr`
	// is an invalid handle, so the file received nothing. The log on file died
	// exactly in the configuration it was invented for — the one where it is the
	// only witness. See internal/applog/fanout.go for the measurement.
	logFile, logErr := openLogFile(cfgPath)
	// The file is added only if it is really there. **A nil `*applog.Writer`
	// inside an `io.Writer` is not nil**: the interface carries the type, the
	// `!= nil` check inside NewFanout would pass, and the first log line would
	// panic. It is Go's classic trap, and here it would cost the program's start.
	dest := []io.Writer{os.Stderr}
	if logFile != nil {
		dest = append(dest, logFile)
		// **The log is deliberately not closed**, and that is not untidiness.
		//
		// Closing it gives the standard error back, and a deferred close runs
		// **while a panic is unwinding** — that is, an instant before the
		// runtime writes the trace. Measured: with the close in place, a
		// process that panicked left `crash traces=this file` in the log and
		// then no trace at all, which is the exact silence this was built to
		// remove. No unit test can see it; it took `-simulate-panic now` on a
		// binary with no console.
		//
		// Nothing is lost by not closing: this Writer buffers nothing — every
		// line is an `os.File.Write` — so there is nothing left to flush, and
		// the handle goes back to the system when the process ends. What it
		// buys is that the log is the **last** thing alive, which is what a
		// witness has to be.
	}
	out := applog.NewFanout(dest...)

	// **Every line carries the process it came from**, and it costs nine
	// characters.
	//
	// Two instances of the monitor write into the same file — the log follows
	// the configuration, so a second copy started from the same folder appends
	// to the same `monitor.log` — and on 17 September 2026 that is exactly what
	// happened: one instance stuck with the camera open and a second one
	// retrying the capture every thirty seconds, their lines interleaved and
	// indistinguishable. Half the time spent reading that file went on deciding
	// which of the two had written each line, and the answer was never in it.
	//
	// It goes on the handler rather than on the `version` line, because the
	// question is not "was there a second instance" — two `version` lines an
	// hour apart already say so — but "whose line is this one", and that is
	// asked of every line.
	log := slog.New(slog.NewTextHandler(out, &slog.HandlerOptions{Level: level})).
		With("pid", os.Getpid())
	slog.SetDefault(log)

	// A failure of the log is reported but stops nothing: a baby monitor that
	// refuses to start because it does not know where to write would be an absurd
	// way of not watching over a child.

	// The version is the **first** line of the log, before even the file's path.
	// The log is read from the beginning when something has gone wrong, and the
	// question that comes before all the others is which binary was running: if
	// it sits at the end, or is not there, it gets reconstructed from memory.
	log.Info("version", "product", version.Product, "version", version.Full())

	if logErr != nil {
		log.Warn("log file unavailable, continuing on the console only",
			"error", logErr)
	} else if logFile != nil {
		// **Whether the traces come here has to be said.** Reading this file
		// after a night that ended with the camera off, "there is no trace" and
		// "traces do not come to this file" are the same silence, and only this
		// word separates them. It declines when the process has a standard
		// error of its own, which is a console somebody is watching.
		where := "the console"
		if logFile.CaptureCrashes() {
			where = "this file"
		}
		log.Info("log file", "path", logFile.Path(), "crash traces", where)
	}

	// Raised here, on the main goroutine and outside every guard, which is the
	// point of it: nothing catches this one, so what it proves is the line
	// above — that the runtime's last words reach the file.
	if *simulatePanic == "now" {
		panic("-simulate-panic now: a deliberate panic that nothing catches")
	}

	if err := run(log, cfgPath); err != nil {
		log.Error("exited with an error", "error", err)
		os.Exit(1)
	}
}

// logDir is the log's folder, next to the configuration.
//
// It lives in a function because three readers use it — whoever opens the log,
// the tray that opens it in Explorer, and the pre-roll flush — and the same
// expression written three times diverges at the first person who moves it.
func logDir(cfgPath string) string {
	return filepath.Join(filepath.Dir(cfgPath), "log")
}

// openLogFile opens the log next to the configuration.
//
// It follows the configuration file, even when that is moved with -config:
// whoever tries a second instance from another folder must not find the two logs
// mixed together, which is the quickest way of reading a fault in the wrong
// place.
func openLogFile(cfgPath string) (*applog.Writer, error) {
	if cfgPath == "" {
		return nil, errors.New("configuration path not determined")
	}
	w, err := applog.New(logDir(cfgPath), "monitor.log")
	if err != nil {
		return nil, err
	}
	return w, nil
}

func run(log *slog.Logger, path string) error {
	if path == "" {
		return errors.New("configuration path not determined")
	}

	cfg, err := config.Load(path)
	if err != nil {
		return err
	}
	cfg.SetPath(path)
	if *listenAddr != "" {
		cfg.ListenAddr = *listenAddr
	}

	if *showConfig {
		fmt.Printf("version:        %s\n", version.Full())
		fmt.Printf("configuration:  %s\n", cfg.Path())
		fmt.Printf("listening on:   %s\n", cfg.ListenAddr)
		fmt.Printf("password:       %s\n", boolLabel(cfg.HasPassword(), "set", "NOT set"))
		fmt.Printf("quality:        %s\n", cfg.Quality)
		// The saving is on by default, so somewhere it has to **say so**: a
		// function that decides on its own to send a fifth of the bits over
		// the network and appears nowhere is indistinguishable from a fault,
		// the day somebody wonders why the picture looks like that.
		saving := "off"
		if cfg.TargetQP > 0 {
			saving = fmt.Sprintf("on, quality %d", cfg.TargetQP)
		}
		fmt.Printf("bit saving:     %s\n", saving)
		// **The same argument as the line above, and it had been missed.** The
		// update check is on by default and sends a request to GitHub once a
		// day — that is, the one thing this program does that leaves the house
		// without being asked, carrying this machine's address. It reaches the
		// log only when there is something to say, so on an ordinary
		// installation it appeared on no surface at all, and whoever wanted to
		// know whether it was running had nowhere to look. A feature that goes
		// out to the Internet and shows up nowhere is exactly what the comment
		// three lines up says must not exist.
		fmt.Printf("update check:   %s\n", boolLabel(cfg.UpdateCheck, "on, daily", "off"))
		fmt.Printf("funnel:         %s\n", boolLabel(cfg.FunnelEnabled, "on", "off"))
		return nil
	}

	if *pprofAddr != "" {
		if err := startProfiler(log, *pprofAddr); err != nil {
			return err
		}
	}

	if *setPassword {
		return promptAndSetPassword(&cfg)
	}

	ctx, stopSignals := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stopSignals()

	// A second way of closing, separate from the signal: with -H=windowsgui there
	// is no console to press Ctrl+C in, and the only way out is the tray's "Quit"
	// item. Keeping them distinct avoids having to call the function that
	// uninstalls the signal handlers to get something that has nothing to do with
	// signals.
	ctx, quit := context.WithCancel(ctx)
	defer quit()

	// --- device selection ---
	//
	// The formats **are enumerated**, and the opposite claim does not hold: "the
	// Source Reader negotiates the nearest to the one requested on its own and
	// then declares what it really delivers" is two statements and neither of
	// them stands. With the scaler on the reader does not choose the nearest — it
	// takes what it is asked for and enlarges — and reading the format back
	// repeats what was assigned to it. See mf.PickCameraSize, which is the only
	// place where the size can go below the preset.
	// **The enumeration is not fatal here, and neither is an empty one.** What
	// this asks for is one thing only: the link the superseded `camera_name`
	// key names, which is a migration and can only be resolved from a list.
	// Which camera gets opened is decided by whoever opens it, at every open —
	// see pipeline.resolveCamera.
	cams, err := devices.ListCameras()
	if err != nil {
		log.Warn("cannot list the cameras", "error", err)
	}
	chosenCam := chosenCamera(cams, cfg.CameraDeviceID, cfg.CameraName, log)
	// **And the migrated link becomes the choice, in memory.**
	//
	// Handed to the capture alone, it would pin a camera that the state declares
	// nobody chose: the box in the details would say "first available webcam"
	// while the capture is pinned, and on unplugging that camera the alert would
	// say "the camera you chose is not connected" beside a box showing no
	// choice. It is the codes chapter's question — at every change of shape, not
	// who writes it but **who reads it** — and the readers of a chosen camera
	// are two, the capture and the server.
	//
	// **It is not written to the file here.** Rewriting somebody's
	// configuration at start-up is a decision of theirs, not ours; the migration
	// completes by itself the first time a camera is chosen from the box, which
	// is where `camera_name` gets cleared.
	cfg.CameraDeviceID = chosenCam

	// **From here on the configuration is the store, and `cfg` is spent.**
	// Everything above runs on this goroutine before anything else exists; what
	// follows hands the value to closures run by the tunnel, by the HTTP request
	// goroutines, by the talk-back and by the tray's own thread — and two of
	// those **write** it: the tunnel recording the name the tailnet granted, and
	// the routes that change a setting.
	//
	// It used to be a local guarded by a mutex here, with the server holding a
	// second copy taken by value. That is one value in two places, and it failed
	// in both of the ways that always does: a race on this side, a stale base on
	// the other. `config.Store` is the one copy, and the reason it lives in
	// `internal/config` rather than here is that the server needs it too — see
	// the note on `server.Options.Config`.
	store := config.NewStore(cfg)
	// configNow is the snapshot every reader takes. **Whoever needs two fields
	// takes it once**: read twice, the two can come from two different
	// configurations.
	configNow := store.Get

	preset, err := encoder.PresetByName(cfg.Quality)
	if err != nil {
		return err
	}
	settings := preset.Settings()

	// **The size is no longer lowered here.** It used to be, "once only", and the
	// reason written beside it was that two readers need the starting size: the
	// pipeline that opens the camera and the hub that builds the resolution scale
	// on it. Both are still true and the conclusion no longer is — asked once, the
	// answer describes the camera that was there at start-up, and the camera can
	// be changed while the monitor runs. It is asked at every open, by whoever
	// opens (see runVideo), and the hub reads it back through VideoStartSize.
	//
	// So `settings` stays the preset from here on: a cap, which nothing raises.

	if *bitrateOverride > 0 {
		log.Warn("bitrate forced from the command line, the preset no longer sets the bandwidth",
			"kbps", *bitrateOverride, "preset", preset.Name,
			"video", fmt.Sprintf("%dx%d@%d", settings.Width, settings.Height, settings.FPS))
		settings.BitrateKbps = *bitrateOverride
	}

	// The encoder is no longer probed by trying to encode: the pipeline chooses
	// it by querying Media Foundation, which declares directly what the GPU can
	// do. So its name is known only once the capture is running.

	// --- WebRTC hub ---
	//
	// The pipeline is born after the hub but the hub needs to ask it for a
	// keyframe: the variable is declared here and the callback reads it when the
	// first PLI arrives, that is, long after the capture has started.
	var p *pipeline.Pipeline

	// The start of a motion episode, from the detection sink to the quality loop.
	// An `atomic.Bool` and not the `mu` lock below: a single goroutine writes it
	// and another consumes it, and putting it there would lie about who touches
	// it.
	//
	// **It does not pass through the `detect_motion` switch, and that is a
	// decision.** That switch says **what to notify** — the banner, the chime,
	// the log line — not what quality the video should have: whoever turns off
	// the motion alert because there is a cat in the house has not asked for a
	// worse picture when the child moves. The mask lives in one place only and
	// downstream, where the alerts are composed; this consumer skips it
	// deliberately.
	var motionStarted atomic.Bool

	// **The encoder is asked one thing only: a bitrate, in CBR.** It is the
	// only one every encoder can do, and the only one we have seen bite on
	// different hardware. The saving is not asked of it — no special mode, no
	// optional property to check chip by chip: it is the bitrate itself that
	// falls when the picture comes out better than the target.
	hub := rtc.New(rtc.Config{
		ICEServers:  iceServers(cfg, log),
		FPS:         settings.FPS,
		BitrateKbps: settings.BitrateKbps,
		AudioKbps:   pipeline.DefaultAudioBitrateKbps,
		Width:       settings.Width,
		Height:      settings.Height,
		// **The level announced in the SDP is the preset's, not this capture's.**
		// The two differ exactly where PickCameraSize lowered the size, and there
		// the capture is the smaller: announcing its level would promise less
		// capacity than the session can end up needing, and it is a promise that
		// cannot be taken back, because NewViewer reads it at the moment of the
		// offer. The preset is the one bound the whole process respects — nothing
		// raises the size or the bitrate above it — so an announcement made from it
		// holds for every format that follows.
		//
		// It is computed from the numbers rather than written beside them: a "3.1"
		// kept next to the size that determines it is a second list.
		//
		// **All four numbers are the preset's, the bitrate included, and `-bitrate`
		// must not reach here.** MinLevelIDC exists to avoid declaring a level
		// higher than necessary, because browsers announce 3.1 and negotiation
		// fails over more: forced past 14000 kbit/s the same function answers 3.2,
		// and a diagnostic flag would stop every viewer connecting. Where that flag
		// really does push the stream above the preset's level, the stream says so
		// in its own SPS and announce() takes the larger of the two.
		LevelIDC: media.MinLevelIDC(preset.Width, preset.Height, preset.FPS, preset.BitrateKbps),
		// Where the resolution scale takes its steps from: the size the capture
		// really starts at, which on a camera with fewer pixels than the preset is
		// not the preset. Read through a function because it changes when the
		// camera does.
		VideoStartSize: func() (int, int, int) { return p.StartSize() },
		OnBitrate: func(kbps int) {
			if err := p.SetBitrate(kbps); err != nil {
				log.Warn("bitrate not applied", "kbps", kbps, "error", err)
			}
		},
		OnVideoFormat: func(w, h, delivered, declared int) { p.SetVideoFormat(w, h, delivered, declared) },
		// **The cadence compared is the delivered one.** `VideoFormat` reports
		// the **declared** one, which follows the camera on its own account:
		// comparing that, the scale would think itself out of step every time
		// automatic exposure lowers the frames.
		VideoFormat: func() (int, int, int) {
			w, h, _ := p.VideoFormat()
			return w, h, p.DeliveredFPS()
		},
		QP:            func() (int, bool) { return p.TakeRecentQP() },
		MotionStarted: func() bool { return motionStarted.Swap(false) },
		TargetQP:      cfg.TargetQP,
		OnKeyframeRequest: func() {
			if err := p.ForceKeyFrame(); err != nil {
				// A request arriving while the capture is restarting is not a
				// fault: the viewer will get the new session's initial
				// keyframe.
				log.Debug("keyframe not forced", "error", err)
			}
		},
		// The talk-back opens the audio output **when somebody speaks**, not
		// at startup: the speakers stay free until they are needed.
		OpenPlayer: func(rate int) (rtc.Player, error) {
			return audio.NewPlayer(rate, configNow().SpeakerDeviceID, log)
		},
		Log: log,
	})

	// --- shared state for the UI ---
	var mu sync.Mutex
	var audioLevel detect.Block
	var motionNow bool
	motion := detect.NewMotion()
	// **The shape detector is the gate, the model is what decides.** `sound` no
	// longer produces alerts: it tells the recogniser when to look, and its
	// tuning is a gate's — lose nothing, do not be right. See recognise.go.
	sound := detect.NewSound(pipeline.AnalysisSampleRate)
	recog := newRecogniser(log)
	defer recog.Close()
	// Touched only by the motion sink, which is a single goroutine: they do not
	// go under the lock, and putting them there would lie about who uses them.
	var motionPeak float64
	var motionMaxDelta int
	var motionLoggedAt, soundLoggedAt time.Time
	// **This one instead sits under `mu`, and the three above do not.** Those are
	// touched by a single goroutine, the sink that produces them; the status
	// round, on the other hand, is called by two — the tray's thread once a second
	// and **every** HTTP request to /api/status — so here a bare variable is a
	// data race. It would not show: the race detector wants cgo, which is not on
	// this machine.
	var prerollLoggedAt time.Time
	startedAt := time.Now()

	p = pipeline.New(pipeline.Config{
		PanicIn:     *simulatePanic,
		CameraLink:  chosenCam,
		Width:       settings.Width,
		Height:      settings.Height,
		FPS:         settings.FPS,
		BitrateKbps: settings.BitrateKbps,
		// **From the settings, not from a literal.** It used to be a 2 written
		// here while `encoder.Settings.KeyframeSecs` held a 2 of its own, so the
		// field moved nothing: changing it changed the number pat-capture
		// expects in its report and not the number the encoder is given, which
		// is the instrument accusing the capture of its own arithmetic. The two
		// agreed, which is exactly why it survived every reading.
		GOPSeconds: settings.KeyframeSecs,

		PreferEncoder: cfg.PreferEncoder,
		MicDeviceID:   cfg.MicDeviceID,
		MicGainDB:     cfg.MicGainDB,
		AudioTestTone: *audioTestTone,
		Log:           log,
	})
	if *audioTestTone {
		log.Warn("test tone active: the audio being sent does NOT come from the microphone")
	}

	// **Recording the events.** An alert always arrives late on what caused it:
	// without the preceding seconds a clip shows the consequences and not the
	// cause, and without the following ones it shows the cause and not the
	// consequences. The recorder keeps the first in a ring and collects the
	// second ones as they pass.
	//
	// It lives in RAM and costs a few megabytes — they are already-encoded H.264
	// bytes, which are already in system memory because the camera is opened with
	// no Direct3D device, so that motion detection can read the luminance plane.
	//
	// **It has no switch of its own, and that is not an oversight**: it records
	// on the events, and the events are already turned on by `detect_cry`,
	// `detect_bark` and `detect_motion`. A second switch would say the same thing
	// in another place, and the two would diverge.
	rec := record.NewRecorder(record.RecorderConfig{Log: log})
	clips := clipStore(cfg, clipsFolder(videosDir(log), cfg.Path(), provenDir, log), log)
	guard.Go(log, "the recordings", func() { serveClips(ctx, clips, rec.Clips(), log) })
	// The folder is declared **always**, not only with a detector on: since the
	// "Clip" button exists a clip can be had with the three switches off too, and
	// then "where has it put it?" is a question somebody will ask anyway.
	log.Info("clips folder", "dir", clips.Dir(), "post_roll", record.DefaultPostRoll,
		"max_mb", cfg.ClipsMaxMB, "max_days", cfg.ClipsMaxDays)
	// **The old folder is resolved before being compared**, and without that the
	// comparison inside says "two different folders" about one: the current one
	// comes back from `provenDir` as the path Windows really wrote to, and a
	// packaged program's `%APPDATA%` is not where it says it is. The line would
	// then announce the clips as left behind while serving them.
	tellAboutOldClips(resolvedDir(oldClipsDir(cfg.Path())), clips, log)

	sinks := pipeline.Sinks{
		// **The hub first, then the ring**: the media has priority, the pre-roll
		// is an accessory. Neither of the two copies a byte.
		Video: func(au media.AccessUnit) {
			hub.WriteVideo(au)
			rec.WriteVideo(au, time.Now())
		},
		// The ring receives the room's audio **during the talk-back too**, where
		// the hub discards it: there the silence is there to avoid howling, in a
		// recording it would only erase the room.
		Audio: func(pkt []byte) {
			hub.WriteAudio(pkt)
			rec.WriteAudio(pkt, time.Now())
		},
		Motion: func(frame []byte, srcW, srcH int) {
			now := time.Now()
			st := motion.Feed(frame, srcW, srcH, now)
			mu.Lock()
			motionNow = st.Moving
			mu.Unlock()
			if st.Started {
				log.Info("motion in the room", "fraction", round4(st.Ratio))
				// The room has stopped being still, so the premise of the
				// bitrate discount has fallen: the loop gives it back on the
				// first round. See internal/rtc/quality.go.
				motionStarted.Store(true)
			}
			// **The numbers for tuning the thresholds come out here**, and at
			// `-v`: the defaults are a declared starting point, not a
			// measurement, and the real ones are chosen by looking at what
			// values this room produces with the people who live in it. The
			// **maximum** of the window is reported, not the last sample: it
			// is the maximum that decides whether a threshold fires, and a
			// sample taken at random every five seconds says nothing about
			// what happened in the other four.
			motionPeak = max(motionPeak, st.Ratio)
			motionMaxDelta = max(motionMaxDelta, st.MaxDelta)
			if now.Sub(motionLoggedAt) >= 5*time.Second {
				log.Debug("motion", "peak", round4(motionPeak),
					"threshold", round4(detect.DefaultStartRatio),
					"delta", motionMaxDelta, "pixel_threshold", detect.DefaultPixelThreshold,
					"active", st.Moving)
				motionPeak, motionMaxDelta, motionLoggedAt = 0, 0, now
			}
		},
		Level: func(pcm []byte) {
			blk := detect.AnalyzeS16LE(pcm)
			now := time.Now()
			st := sound.Feed(blk, now)
			mu.Lock()
			audioLevel = blk
			mu.Unlock()
			// **The shape detector no longer decides: it opens the gate.** What
			// it says here does not become an alert — what is in the room is said
			// by the model — but it tells the recogniser it is worth looking. The
			// stream reaches it always, because the window the model will classify
			// is the one of the ten seconds **before** this moment.
			recog.Feed(pcm, p.AnalysisRate())
			if st.Cry || st.Bark {
				recog.Ask()
			}
			// The same two numbers as the motion, for the same reason: they are
			// the only thing to look at if one day the gate closed too far, and
			// without them there would be nothing to reason about.
			if now.Sub(soundLoggedAt) >= 10*time.Second {
				log.Debug("sound gate", "floor", round1(st.Floor), "level", round1(st.Level),
					"hz", round1(st.DominantHz), "cry", st.Cry, "bark", st.Bark)
				soundLoggedAt = now
			}
		},
	}

	// --- remote access ---
	//
	// The tunnel stays off unless explicitly asked for, and in any case never
	// without a password: the address it publishes is reachable by anybody on the
	// Internet. The check lives in CanExposePublicly, here it is respected.
	//
	// **The tunnel is always built and starts only if authorised.** Existing only
	// with `funnel_enabled` already true at startup meant that turning remote
	// access on required restarting the program — inside a guided path, the step
	// at which people give up. `New` opens nothing: it is `Enable` that releases
	// the brake, and `Run` sleeps until it arrives.
	remote := tunnel.New(tunnel.Config{
		Hostname: cfg.FunnelHostname,
		AuthKey:  cfg.TailscaleAuthKey,
		StateDir: filepath.Dir(cfg.Path()),
		Log:      log,
		// The name the tailnet actually granted is **written down**, not merely
		// suffered. At the next start what we already have is asked for, and the
		// node is never renamed again: without that, every start repeats the
		// losing request, and the day the short name came free we would take it
		// back — changing the public address in silence.
		OnHostname: func(name string) {
			// **Through the store, so the name survives the next save.** Written
			// to a copy of our own it reached the file and then the server's own
			// copy — taken before this ever ran — put the old one back at the
			// first setting changed from any page.
			if _, err := store.Set(func(c *config.Config) {
				c.FunnelHostname = name
			}); err != nil {
				log.Warn("node name not saved, it will be asked again at the next start",
					"name", name, "error", err)
			}
		},
	})
	if cfg.FunnelEnabled {
		if err := cfg.CanExposePublicly(); err != nil {
			log.Error("remote access not enabled", "reason", err)
		} else {
			remote.Enable()
		}
	}

	registry := alerts.NewRegistry()
	statusFn := func() server.Status {
		// One snapshot for the whole round: the address below and the three
		// switches further down have to describe the same configuration.
		conf := configNow()
		mu.Lock()
		lvl := audioLevel
		mu.Unlock()
		// **The microphone's name is known by whoever opened it.** A constant
		// there would say the same word for every device, on a field the page
		// shows verbatim. The line now says which microphone is capturing, which
		// is also the only way of noticing that a different one was opened because
		// the chosen one was not there.
		mic := p.Microphone()
		st := server.Status{
			Ready:            hub.Ready(),
			Encoder:          p.EncoderName(),
			EncoderVendor:    encoderVendor(p.HardwareEncoder()),
			Resolution:       videoResolution(p, settings),
			FPS:              declaredFPS(p, settings),
			MeasuredFPS:      hub.MeasuredFPS(),
			DeliveredFPS:     p.DeliveredFPS(),
			VideoDropped:     p.Stats.VideoDropped.Load(),
			LastFrameUnix:    p.Stats.LastVideoUnix.Load(),
			VideoKbps:        videoKbps(hub),
			Camera:           p.Camera().Name,
			CameraFallback:   p.CameraIsFallback(),
			CameraDenied:     p.CameraDenied(),
			MicrophoneDenied: p.MicrophoneDenied(),
			Microphone:       mic.Name,
			MicrophoneID:     mic.ID,
			MicrophoneActive: p.AudioActive(),
			RawAudio:         p.RawAudioMode(),
			AudioLevelDBFS:   lvl.RMSdBFS,
			MicHealth:        lvl.Health().Code(),
			Viewers:          hub.Stats.ViewersNow.Load(),
			TalkbackBusy:     hub.TalkbackActive(),
			// If a clip is in progress the recorder knows, and it is the only
			// one: the bar's button does not colour on being pressed, it waits
			// for this answer. See `Status.Recording`.
			Recording:      rec.Recording(),
			VideoFrames:    p.Stats.VideoFrames.Load(),
			Keyframes:      p.Stats.Keyframes.Load(),
			KeyframeReqs:   hub.Stats.KeyframeReqs.Load(),
			Restarts:       p.Stats.Restarts.Load(),
			ProfileLevelID: hub.ProfileLevelID(),
			Uptime:         time.Since(startedAt).Round(time.Second).String(),
			LocalURL:       homeAddress(conf.ListenAddr),
			Remote:         remoteState(remote),
		}
		// The alerts are recomputed from scratch on every round on the snapshot
		// just built: no state to keep aligned, and a fault that clears switches
		// itself off with no branch charged with switching it off.
		now := time.Now()
		mu.Lock()
		moving := motionNow
		mu.Unlock()
		// **The cry and the bark are declared by the model**, not by the shape
		// detector: that one opened the gate and has finished its job. The verdict
		// is consumed here, at one round a second, instead of arriving from a
		// callback — a second point from which the state is commanded is precisely
		// the defect that governors fighting each other come from.
		recog.Wanted(conf.DetectCry, conf.DetectBark)
		cry, bark := recog.Verdict(now)
		// **The switches turn off the event, not its alert.** Whoever has no dog
		// must not hear a wrong bark: not in the banner, not in the log, and
		// tomorrow not on the phone.
		cry = cry && conf.DetectCry
		bark = bark && conf.DetectBark
		moving = moving && conf.DetectMotion
		// The simulated faults sit **outside** the grace: they are asked for with
		// a flag of their own, and waiting half a minute to see whether the test
		// instrument works would make it awkward for precisely the person using
		// it.
		present := append(presentAlerts(st, moving, cry, bark, startedAt, now), simulatedFaults(now)...)
		appeared, recovered := registry.Update(now, present)
		// **On the file, not only on the screen.** The banner is seen by whoever
		// has the page open; whoever comes back in the morning has only the log,
		// and that is where the hour the camera stopped has to be.
		for _, a := range appeared {
			log.Warn("alert", "code", a.Code, "level", a.Level)
			// **A picture that has stopped after having started is worth a
			// goroutine dump**, and what it answers is the question that had no
			// answer on the night this was written: which call did not come
			// back. It is asked here because this is where the monitor first
			// knows, and it happens once in the life of the process.
			//
			// **The condition does not separate a stall from an unplugged
			// camera, and does not pretend to.** Both reach it — one with the
			// log already full of the capture's errors, the other with nothing
			// in the log at all — and the code cannot tell them apart without a
			// second reading of state that would be a second idea of what "not
			// working" means. One dump per process is the price of covering the
			// silent one, which is the case nobody could diagnose.
			//
			// A capture that never started is excluded, and that one really is
			// different: `LastFrameUnix` is zero, the stacks would show a
			// camera being opened, and the error is on the line above.
			//
			// **The state is asked, not read off this loop**, because
			// `appeared` is the diff of a set that carries the simulated faults
			// too — see stalledAfterStarting.
			if a.Code == alerts.CaptureStopped && stalledAfterStarting(st, startedAt, now) {
				dumpStacksOnce(log, "the picture stopped while the capture was still running")
			}
			// **The pre-roll is flushed where the event appears**, and only for
			// an event: here the detect_* mask has already been applied and so
			// has the thirty-second grace. A fault is not something that happened
			// in the room and has no preceding seconds to show.
			if recordsClip(a) {
				rec.Trigger(string(a.Code), now)
			}
		}
		for _, c := range recovered {
			log.Info("alert cleared", "code", c)
		}
		// **The pre-roll declares itself before it is needed.** Noticing it is
		// empty only when looking at the first clip would mean noticing on the
		// night it was needed: `refused` that does not stop rising means keyframes
		// that do not arrive, `resets` rising means the encoder was rebuilt, and
		// that is the first thing to look at when a pre-roll comes out short.
		//
		// It is decided under the lock and written outside it: holding `mu` while
		// writing to a file would make anybody asking for the status wait.
		mu.Lock()
		due := now.Sub(prerollLoggedAt) >= 10*time.Second
		if due {
			prerollLoggedAt = now
		}
		mu.Unlock()
		// **A clip's finish line is checked by the frames that arrive**, and if
		// the camera stops none arrive any more: without this heartbeat the clip
		// would stay open in memory and never be written, that is, precisely the
		// one of the event during which something happened to the camera would be
		// lost.
		rec.Tick(now)
		if due {
			rs := rec.Stats()
			log.Debug("preroll", "gops", rs.Ring.Gops, "frames", rs.Ring.Frames,
				"span_ms", rs.Ring.Span.Milliseconds(), "kb", rs.Ring.Bytes/1024,
				"audio", rs.Ring.AudioPackets, "resets", rs.Ring.Resets,
				"dropped", rs.Ring.Dropped, "refused", rs.Ring.Refused,
				"recording", rs.Recording, "clips", rs.Written,
				"lost", rs.Dropped, "cut", rs.Cut)
		}
		st.Alerts = registry.Active(now)
		return st
	}

	srv, err := server.New(server.Options{
		Config:   store,
		Hub:      hub,
		StatusFn: statusFn,
		// **The enumeration is done by whoever opens the microphones**, and this
		// is only the shape the page wants it in. With the test tone nothing is
		// enumerated: there the microphone is not opened at all, and offering a
		// choice the capture ignores would be a knob that moves nothing.
		Microphones: func() ([]server.Microphone, error) {
			if *audioTestTone {
				return nil, nil
			}
			devs, err := audio.ListCaptureDevices()
			if err != nil {
				return nil, err
			}
			out := make([]server.Microphone, 0, len(devs))
			for _, d := range devs {
				out = append(out, server.Microphone{ID: d.ID, Name: d.Name, Default: d.IsDefault})
			}
			return out, nil
		},
		UseMicrophone: p.SetMicDevice,
		// The cameras. **The list is the same one the capture picks from** —
		// `devices.Cameras`, so the infrared sensors a Windows Hello webcam
		// exposes are dropped here exactly as they are there, and the box cannot
		// offer a choice the open would refuse. With no usable camera it is
		// empty, which the page shows as a box that cannot be pressed: that is
		// not an error, the machine simply has no webcam right now.
		Cameras: func() ([]server.Camera, error) {
			all, err := devices.ListCameras()
			if err != nil {
				return nil, err
			}
			out := []server.Camera{}
			for _, c := range devices.Cameras(all) {
				out = append(out, server.Camera{ID: c.Link(), Name: c.Name})
			}
			return out, nil
		},
		UseCamera:    p.SetCamera,
		EnableRemote: remote.Enable,
		Clips:        clips,
		// **A clip asked for by hand is born kept**, that is, exempt from the
		// retention: a person asked for it, and it is the only one we know
		// somebody will want again. It is released from the recordings page, and
		// from then on the rule of all the others applies — the direction is this
		// way because releasing is always possible, getting an expired clip back
		// is not.
		Record: func() bool { return rec.TriggerKept(record.CodeManual, time.Now()) },
		Log:    log,
	})
	if err != nil {
		return err
	}
	defer srv.Close()

	httpSrv := &http.Server{
		Addr:              cfg.ListenAddr,
		Handler:           srv.Handler(),
		ReadHeaderTimeout: 10 * time.Second,
		// No WriteTimeout: the signalling WebSockets stay open for the whole
		// duration of the viewing.
	}

	// **The port is taken before touching the camera.**
	//
	// `ListenAndServe` did both things together, and it sat in a goroutine of the
	// errgroup next to the one that starts the capture: a second instance opened
	// camera and microphone, *then* discovered the port was taken and exited.
	// Observed on a test machine, twice in the same log — with the first monitor
	// meanwhile retrying to open the same webcam. Contending for the device with
	// oneself is the surest way of turning a double start into a camera fault.
	//
	// The listener is also the only single-instance check we have, and it is
	// free: whoever does not get the port is not this machine's monitor and has
	// nothing to open. Bind now, `Serve` later.
	ln, err := net.Listen("tcp", cfg.ListenAddr)
	if err != nil {
		return fmt.Errorf("HTTP server: %w", err)
	}
	// `Shutdown` closes the listeners it is serving, but if it arrives **before**
	// `Serve` starts — the two are on different goroutines — that one exits at
	// once and the port would stay held by a process that is on its way out.
	defer ln.Close()

	if !cfg.HasPassword() {
		log.Warn("no password set: open the page to run the first-time setup",
			"url", localURL(cfg.ListenAddr, "/setup"))
	}

	// --- notification area ---
	//
	// With the log on file the monitor can live without a console, and without a
	// console it would have no visible presence at all: nothing to say it is
	// running, and no way of closing it other than the Task Manager. The tray is
	// both those things.
	//
	// **The words on this side are chosen by Windows**, not by the browser of
	// whoever is watching: the tray and the notifications are read by whoever is
	// in front of the machine. The dictionary is opened **once and here**, and
	// both the tray and the notifications this file sends from outside the menu
	// use it: opened separately they could choose two different languages inside
	// the same process.
	dict := i18n.Open(i18n.FromSystem())
	log.Info("tray language", "language", dict.Language())
	// A catalogue that cannot be read does not stop the monitor and must not stay
	// silent: the tray would appear in a language nobody asked for, or with the
	// keys in place of the items, and with nothing to read to understand why.
	for _, p := range dict.Problems() {
		log.Warn("tray dictionary", "requested", dict.Requested(), "error", p)
	}

	// The update check is owned here and not by the server, and that is what
	// keeps it off the viewer's page: `server.Status` is serialised to JSON for
	// whoever is watching, so a field on it would reach a phone — where the
	// answer is of no use, because the remedy is to be in front of the machine.
	// Passed to `trayStatus` instead, it can only go where it is wanted.
	updates := &update.Checker{}

	tr := tray.New(tray.Config{
		// **The status is asked of the server, not of `statusFn`.** The fields
		// only it can fill — how many sessions are open, first of all — used to
		// be put in by the HTTP route: the page saw them and the tray did not,
		// and the panel declared "no device connected" while the page counted
		// two. See `server.Status`.
		StatusFn: func() tray.Status {
			// `srv.Status()` is evaluated outside the snapshot deliberately: it
			// takes the server's own `cfgMu`, and this must not be holding
			// anything when it does.
			st := srv.Status()
			return trayStatus(st, configNow(), startedAt, dict, updates.State())
		},
		OnQuit:          quit,
		OnRevoke:        srv.RevokeAllSessions,
		OnResetPassword: srv.ResetPassword,
		// **Resolved, because this one is opened in Explorer and not by us.**
		// The log is written through this program, so any redirection is
		// invisible to it; the panel's command hands the path to the shell,
		// which is not in our package and opens the folder the name really
		// says. See `resolvedDir`.
		LogDir:     resolvedDir(logDir(cfg.Path())),
		VideoDir:   clips.Dir(),
		SetupURL:   localURL(cfg.ListenAddr, "/onboarding"),
		Log:        log,
		Dictionary: dict,
	})

	g, gctx := errgroup.WithContext(ctx)

	// A tray that does not open must not switch off the monitor: it is the same
	// rule by which a camera that does not start does not silence the
	// microphone. On a session with no desktop — a service, a particular kind of
	// remote access — the notification area does not exist, and there the
	// monitor has to go on watching without an icon.
	//
	// The sentence stays here and `aside` adds none of its own: what it catches
	// is the other half, a panic, which this branch never sees.
	g.Go(aside(log, "the notification area", func() error {
		if err := tr.Run(gctx); err != nil {
			log.Warn("notification area unavailable, the monitor carries on without an icon",
				"error", err)
		}
		return nil
	}))

	// The public address is announced **once only**, when it appears: it is the
	// only moment when there is something new to say, and it is also the moment
	// when it is needed — from there it gets copied onto a phone.
	g.Go(aside(log, "the announcement of the public address", func() error {
		if *simulatePanic == "accessory" {
			panic("-simulate-panic accessory: a deliberate panic")
		}
		announcePublicAddress(gctx, tr, remote)
		return nil
	}))

	// The update check is as accessory as a part can be: it watches nothing in
	// the room, and a monitor whose camera went off because GitHub answered
	// oddly would be the invariant on separate lives broken by a courtesy.
	//
	// **It is not started at all when the configuration says not to**, rather
	// than started and made to do nothing: a goroutine that exists in order to
	// decline is a thing somebody later has to read to find out it declines,
	// and the promise here is that no request leaves the house.
	if configNow().UpdateCheck {
		g.Go(aside(log, "the update check", func() error {
			watchForUpdates(gctx, tr, updates, log)
			return nil
		}))
	}

	// **Not `aside`: this is the monitor.** It is guarded all the same, and for
	// a different gain — the video and the audio sessions under it each catch
	// their own panic and are restarted by their own supervisor, so what is left
	// here is the supervisor's loop itself, where a panic really is the end.
	// Ending through the group means an orderly shutdown and a stack in the log,
	// instead of the process going out mid-sentence.
	g.Go(func() error {
		return guard.Run(log, "the capture supervisor", func() error {
			return p.Run(gctx, sinks)
		})
	})

	g.Go(func() error {
		if err := guard.Run(log, "the quality loop", func() error {
			return hub.RunBitrateControl(gctx)
		}); err != nil {
			// **Everything it commands goes back to the ceiling**, which is the
			// answer this program already gives everywhere to "I do not know":
			// the saving is lost, never the picture.
			//
			// It commands **two** levers and not one, and the first version of
			// this put back only the bitrate — so a loop that died at
			// 640x352@2 left that on the wire for the rest of the night with a
			// log line that mentioned the bitrate alone, which is worse than
			// silence: a reader would have believed the picture was whole.
			// `scale.target` is called from inside this loop and from nowhere
			// else, so when the loop goes the size and the cadence stay exactly
			// where they were last put.
			w, h, fps := p.StartSize()
			log.Warn("the quality loop stopped: the bitrate and the picture go back to the top",
				"kbps", settings.BitrateKbps, "size", fmt.Sprintf("%dx%d@%d", w, h, fps),
				"error", err)
			if err := p.SetBitrate(settings.BitrateKbps); err != nil {
				log.Warn("bitrate not put back", "error", err)
			}
			// The starting size is the scale's own full step, so this asks for
			// nothing the capture cannot give: on a camera with fewer pixels
			// than the preset it is the camera's size, which is where the steps
			// begin.
			if w > 0 && h > 0 && fps > 0 {
				p.SetVideoFormat(w, h, fps, fps)
			}
		}
		return nil
	})

	// Remote access is a second listener on the same handler: same sessions,
	// same authentication, no shortcut for whoever arrives from outside. If it
	// cannot open, the monitor at home stays working.
	//
	// **It says `aside` although tunnel.Run already catches for itself.** The
	// protection there is real and it is a package away, so at this call site
	// the line said nothing about which of the two decisions had been taken —
	// and this list is where they are meant to be read. The second catch costs
	// a comparison that never fires.
	{
		handler := srv.Handler()
		g.Go(aside(log, "remote access", func() error { return remote.Run(gctx, handler) }))
	}

	// Not `aside` either: with no server nobody can watch, which is the same as
	// no monitor. Handler panics do not come through here — net/http catches
	// those per connection — so what this covers is Serve itself.
	g.Go(func() error {
		log.Info("server listening", "address", configNow().ListenAddr)
		return guard.Run(log, "the HTTP server", func() error {
			if err := httpSrv.Serve(ln); err != nil && !errors.Is(err, http.ErrServerClosed) {
				return fmt.Errorf("HTTP server: %w", err)
			}
			return nil
		})
	})

	// On the first start the setup path is opened, and **only** on the first.
	//
	// The monitor is meant to start on its own when the computer comes on, and a
	// program that opens a browser tab at every restart becomes a program that
	// gets uninstalled. The witness is `onboarding_done`, distinct from "I have a
	// password": see config.NeedsOnboarding.
	if conf := configNow(); conf.NeedsOnboarding() {
		g.Go(aside(log, "opening the guided path", func() error {
			openSetup(gctx, log, localURL(conf.ListenAddr, "/onboarding"))
			return nil
		}))
	}

	// Orderly shutdown: the server is closed before the pipeline is allowed to
	// die, so that connected viewers receive a clean close.
	//
	// Not `aside`: this runs when everything is already ending, so there is
	// nothing left to carry on without. What the catch buys is that the fault
	// is written down with its stack rather than taking the process out in the
	// middle of closing the viewers' connections.
	g.Go(func() error {
		<-gctx.Done()
		return guard.Run(log, "the orderly shutdown", func() error {
			shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			return httpSrv.Shutdown(shutdownCtx)
		})
	})

	// Gives notice when the stream is really ready to be watched.
	g.Go(aside(log, "the stream-ready notice", func() error {
		readyCtx, cancel := context.WithTimeout(gctx, 30*time.Second)
		defer cancel()
		if err := hub.WaitReady(readyCtx); err != nil {
			if gctx.Err() == nil {
				log.Warn("video stream not ready within 30s", "error", err)
			}
			return nil
		}
		log.Info("stream ready", "profile-level-id", hub.ProfileLevelID())
		return nil
	}))

	// **The wait has a deadline, and it starts when stopping is asked for.**
	// `gctx` and not `ctx`: it is cancelled both by whoever asked to quit and by
	// a member that failed, and both of those are moments after which nothing
	// here should still be running. See waitOrLeave for what it cost not to
	// have this.
	err = waitOrLeave(log, g.Wait, gctx.Done(), shutdownLimit, func() error {
		return leaveItToTheSystem(log, shutdownLimit)
	})
	if err != nil {
		return err
	}
	log.Info("shutdown complete")
	return nil
}

// aside runs a part of the monitor that the monitor can outlive.
//
// An errgroup takes everything down together as soon as one member answers with
// an error, which is right for the pieces the monitor **is** and wrong for the
// ones beside it: the notification area, the announcement of the address, the
// browser opened at first start. A panic is worse still, because it does not
// even reach the group — it ends the process from wherever it happened.
//
// **It is the invariant on separate lives, applied to the group.** A camera
// that will not open must not silence the microphone; by the same argument an
// icon that will not draw must not switch off the camera. It is written at the
// call site rather than inside each of them, so that which parts are accessory
// is something one reads in the list instead of something one has to go and
// check: whatever is not wrapped here is something the monitor cannot do
// without.
func aside(log *slog.Logger, what string, fn func() error) func() error {
	return func() error {
		if err := guard.Run(log, what, fn); err != nil {
			log.Warn("carrying on without it", "part", what, "error", err)
		}
		return nil
	}
}

// firstLine keeps the first line of a text and shortens it to menu width.
//
// The prerequisite text comes from Tailscale and can be long and on several
// lines: a menu item cannot show it, and truncating it without saying so would
// make it a half sentence. The whole version stays on the page and in the log;
// here the end of the thread is enough, because the item serves to **open** the
// address, not to explain.
func firstLine(s string) string {
	if i := strings.IndexAny(s, "\r\n"); i >= 0 {
		s = s[:i]
	}
	s = strings.TrimSpace(s)
	const max = 60
	if len([]rune(s)) > max {
		s = string([]rune(s)[:max-1]) + "…"
	}
	return s
}

// simulatedFaults returns the fake faults asked for with -simulate-fault.
//
// **They come on and go off in twenty-second windows** instead of staying on:
// the case one gets wrong when writing it is not the alert that appears, it is
// the one that does not go away — if the return does not work, the banner stays
// there declaring a fault that is over and the page becomes a liar. By
// alternating, the test covers both directions on its own.
//
// It takes the instant as a parameter so that it can be checked, like the media
// clock and the measured cadence.
func simulatedFaults(now time.Time) []alerts.Code {
	if *simulateFault == "" || (now.Unix()/20)%2 == 1 {
		return nil
	}
	var out []alerts.Code
	for _, c := range strings.Split(*simulateFault, ",") {
		if c = strings.TrimSpace(c); c != "" {
			out = append(out, alerts.Code(c))
		}
	}
	return out
}

// validateSimulation refuses a code that does not exist.
//
// A badly written code would produce no error at all: the registry would accept
// it like any other and the page would show a banner with no words. It is the
// family of the wrong GUID that does not complain, and it is closed the same
// way: by looking at the real list instead of trusting.
func validateSimulation() error {
	// A misspelling stops the start-up instead of producing nothing: whoever
	// asked for a panic and got a monitor that runs beautifully would conclude
	// the machinery works, which is the wrong answer given by the right
	// symptom.
	switch *simulatePanic {
	case "", "video", "audio", "accessory", "now":
	default:
		return fmt.Errorf("-simulate-panic: unknown place %q, choose from video, audio, accessory, now",
			*simulatePanic)
	}
	if *simulateFault == "" {
		return nil
	}
	known := map[string]bool{}
	for _, c := range simulatableCodes() {
		known[c] = true
	}
	for _, c := range strings.Split(*simulateFault, ",") {
		c = strings.TrimSpace(c)
		if c != "" && !known[c] {
			return fmt.Errorf("-simulate-fault: unknown code %q, choose from %s",
				c, strings.Join(simulatableCodes(), ", "))
		}
	}
	return nil
}

// simulatableCodes are the codes -simulate-fault accepts, **derived from the
// registry** rather than listed here.
//
// It used to be a hand-written map, and beside it a hand-written sentence in the
// flag's help: two lists of the same thing, of which the second was already
// visible to the user. A code added to the registry now appears in both without
// anybody remembering.
//
// **The events are out, and that is the one judgement made here**: crying,
// barking and movement are things that happen in the room, and simulating them
// would say nothing about whether the alert chain works that the faults do not
// already say — while making the page announce a child crying is the one lie
// this program must not tell.
func simulatableCodes() []string {
	var out []string
	for _, c := range alerts.AllCodes() {
		if alerts.LevelOf(c) != alerts.Event {
			out = append(out, string(c))
		}
	}
	return out
}

// round4 rounds for the log: four decimals are the useful resolution of a
// fraction of a pixel, and the sixteen digits of a float64 in a log line hide
// what sits next to them.
func round4(v float64) float64 { return math.Round(v*10000) / 10000 }

// round1 is enough for decibels and for a roughly estimated frequency.
func round1(v float64) float64 { return math.Round(v*10) / 10 }

// startupGrace is how long one waits before calling broken what has not started
// yet.
//
// **Found live, not reasoned:** on the first test with simulated faults the log
// announced "microphone missing" at instant zero and its return a second later,
// that is, while WASAPI was opening the device. On an alert that is not a
// cosmetic detail: that line is a banner and a chime **at every start of the
// program**, and an alarm that sounds every time stops being read.
const startupGrace = 30 * time.Second

// The fault predicates live **once only**, and two readers use them: the tray,
// which turns them into a colour, and the alerts, which turn them into a banner
// on the page. Written twice they would diverge at the first touch, and the
// colour would say one thing and the banner another about the same monitor.

// videoStall is how long the picture may stand still before the monitor says
// the capture has stopped.
//
// **It is the number the prose already promised.** `Pipeline.SetCamera` argues
// that a camera chosen while the monitor runs needs no grace of its own because
// "what a closed camera makes false is `capture-stopped`, which asks for half a
// minute of no frames" — and no code asked for anything of the sort. Half a
// minute is what makes that sentence true, and it is above every ordinary pause
// the capture has: a reopen on a chosen camera takes about a second, the
// restart backoff reaches thirty and is meant to be announced when it does.
const videoStall = 30 * time.Second

// captureStopped: there is no live picture.
//
// **It is a freshness and not a latch, and that is the repair.** The predicate
// used to be `!s.Ready`, that is, "the first keyframe has not arrived yet" —
// which answers "did it ever start" and was read by three consumers as "is it
// working now". On 17 September 2026 the encoder's rebuild did not come back:
// frames stopped at 14:58, the page went on saying ready, the notification area
// stayed green, and the log carried no alert for the three and a half minutes
// until somebody restarted the program by hand. The fault had every instrument
// it needed — `Stats.LastVideoUnix` was being written on every frame — and
// nobody loading it.
//
// The two halves are both kept, because they are two faults with one name and
// one remedy: a capture that never started (`Ready` false past the startup
// grace) and one that has stopped (`videoStall` with no frame). What must not
// happen is the second being invisible because the first is false.
func captureStopped(s server.Status, startedAt, now time.Time) bool {
	if now.Sub(startedAt) <= startupGrace {
		return false
	}
	if !s.Ready {
		return true
	}
	// A stream declared ready and no frame ever produced is not a state this
	// program can reach — the latch closes on a frame — so it is read as the
	// fault it would be rather than as health.
	if s.LastFrameUnix == 0 {
		return true
	}
	return now.Sub(time.Unix(s.LastFrameUnix, 0)) >= videoStall
}

// stalledAfterStarting is the picture having stopped after it had started, and
// it is the one case worth a goroutine dump: the capture that never started
// leaves its error on the line above, while this one can leave nothing at all.
//
// **It is a function rather than three conditions at the call site because of
// where that call site is.** The dump is raised while walking the alerts that
// appeared, and that set is `presentAlerts` plus `simulatedFaults` — so
// `-simulate-fault capture-stopped` raises this code on a monitor that is
// working, where `Ready` is true and a frame arrived a moment ago. Both of the
// conditions guarding the dump are then satisfied by construction: the
// instrument would write sixty kilobytes of stacks of a capture that is
// running, and **spend the one dump the process has**, so a real stall the same
// night would write nothing. Asking `captureStopped` about the status is what
// no flag can reach, and the test instrument goes back to costing nothing.
func stalledAfterStarting(s server.Status, startedAt, now time.Time) bool {
	return captureStopped(s, startedAt, now) && s.Ready && s.LastFrameUnix != 0
}

// micSilent: the path delivers zeros. The microphone is there and cannot be heard.
func micSilent(s server.Status) bool {
	return s.MicHealth == detect.MicCodeDigitalSilence
}

// micMissing: there is no audio at all. It happens on laptops with the lid
// closed, and it must not switch off the video.
func micMissing(s server.Status) bool { return !s.MicrophoneActive }

// cameraDenied and micDenied: Windows is refusing the device because the
// permission is off.
//
// **They are read and not deduced**, and that is the point of the two fields:
// deduced from the absence of frames or of a level, a withdrawn consent looks
// exactly like a camera that will not open — see `pipeline.CameraDenied`.
//
// They are functions rather than the field read at the call site so that they
// sit beside the predicates they take precedence over: whoever adds a case to
// `activeFaults` sees the whole family in one place.
func cameraDenied(s server.Status) bool { return s.CameraDenied }

func micDenied(s server.Status) bool { return s.MicrophoneDenied }

// cameraOther: the camera being watched is not the one that was chosen.
//
// **It is read and not recomputed.** The obvious form — chosen != open — is
// wrong in a way that does not show: Windows gives the same symbolic link back
// in different cases depending on who is asked, so it would declare another
// camera about the right one. The capture matches the two once and publishes
// the fact.
func cameraOther(s server.Status) bool { return s.CameraFallback }

// remoteDown: remote access does not work. **It is not a fault of the monitor**
// — at home it can be seen perfectly well — but whoever is outside would notice
// only by trying to open it, so it has to be said.
func remoteDown(s server.Status) bool {
	if s.Remote.Warning != "" {
		return true
	}
	// **The probe is worth as much as the alert**, and more than the phase:
	// `running` says what Tailscale answered us, this says that a request of
	// ours went out onto the Internet and did not come back. It comes in here
	// and not from a second place because two readers use this predicate — the
	// banner and the icon — and written twice they would say different things
	// about the same monitor.
	//
	// ReachUnknown is not enough and must not be: it is the absence of a proof,
	// that is, the minutes after startup in which the public name is not yet in
	// the DNS, and a machine where the probe cannot be run at all.
	if s.Remote.Reach == tunnel.ReachFailed {
		return true
	}
	switch s.Remote.Phase {
	case tunnel.PhaseError, tunnel.PhaseNeedsLogin, tunnel.PhaseNeedsFunnel, tunnel.PhaseNeedsApproval:
		return true
	}
	return false
}

// presentAlerts is the whole snapshot: what is wrong, and what is happening.
//
// **In the first thirty seconds nothing is announced.** The monitor is opening
// camera, microphone and tunnel, and what is not ready yet is not yet broken:
// the tray can afford to flash for a second, an alert cannot, because that stays
// written in the log. The first live test found the defect, with "microphone
// missing" announced at instant zero and cleared a second later.
//
// **For motion the same grace has one more reason, and a stronger one: whoever
// starts the monitor is in front of the machine**, that is, in front of the
// camera, and is moving. There a motion alert is not rare, it is guaranteed.
func presentAlerts(s server.Status, moving, cry, bark bool, startedAt, now time.Time) []alerts.Code {
	if now.Sub(startedAt) <= startupGrace {
		return nil
	}
	out := activeFaults(s, startedAt, now)
	// The events go into the same snapshot as the faults and follow its rules:
	// they are announced when they appear, they do not repeat while they last,
	// and they go away on their own.
	if moving {
		out = append(out, alerts.Motion)
	}
	if cry {
		out = append(out, alerts.Cry)
	}
	if bark {
		out = append(out, alerts.Bark)
	}
	return out
}

// activeFaults is the snapshot of what is wrong now.
//
// It returns them **all**, not the worst: whoever has a single banner is the one
// who chooses which to show, and that decision lives in the alert registry
// together with the ordering. Here one merely observes.
func activeFaults(s server.Status, startedAt, now time.Time) []alerts.Code {
	var out []alerts.Code
	// **The refusal wins over the consequence**, which is the rule the
	// microphone's pair below already states: with the permission off there are
	// no frames, so both are true and they are the same fault seen from two
	// points — and of the two only one says what to do about it. Announcing
	// both would put *no images from the camera* beside *the camera permission
	// is off*, in a product that shows one banner.
	if cameraDenied(s) {
		out = append(out, alerts.CameraDenied)
	} else if captureStopped(s, startedAt, now) {
		out = append(out, alerts.CaptureStopped)
	}
	if micDenied(s) {
		out = append(out, alerts.MicDenied)
	} else if micMissing(s) {
		out = append(out, alerts.MicMissing)
	} else if micSilent(s) {
		// If the microphone is not there, saying it also delivers zeros is
		// true and useless: they are the same fault seen from two points.
		out = append(out, alerts.MicSilent)
	}
	// **It is not conditioned on the capture running**, unlike the microphone's
	// pair above: a camera that is not delivering is `capture-stopped`, and the
	// two say different things — one that there is no picture, the other that
	// the picture is of somewhere else. Whoever has both has to read both.
	if cameraOther(s) {
		out = append(out, alerts.CameraOther)
	}
	if remoteDown(s) {
		out = append(out, alerts.RemoteDown)
	}
	return out
}

// trayStatus turns the monitor's state into what an icon can say.
//
// The reduction to a colour is the point: whoever walks past the computer does
// not read, they look. So the order of the cases is that of severity, and the
// first that answers wins — a mute microphone counts for more than remote access
// that works, because a baby monitor that does not let the child be heard is no
// use however reachable it is.
func trayStatus(s server.Status, cfg config.Config, startedAt time.Time, dict *i18n.Dictionary, up update.State) tray.Status {
	out := tray.Status{
		// **The click on the icon opens here, the QR code does not**, and for a
		// while the same line decided both. This function used to recompute the
		// home address with `localURL`, which on a listen across all interfaces
		// answers `localhost`, and threw away what `homeAddress` had already
		// **asked the system** inside `s.LocalURL`: the page received 192.168.x.y
		// and the tray panel localhost, from the same status and at the same
		// instant.
		//
		// It showed only while the tunnel was not up yet — afterwards the public
		// one takes its place — that is, in the first seconds after startup, and
		// what one saw was a useless address **and a QR code that led to it**. A
		// false affordance costs more than a missing one.
		//
		// Two lists of the same thing always diverge: here the home address is
		// not recomputed, it is taken.
		OpenURL: localURL(cfg.ListenAddr, "/"),
		HomeURL: addressOthersCanReach(s.LocalURL),
		// Two lines, not five. The video size, the cadence and the microphone's
		// name are numbers that change every second and that nobody opens the
		// menu to read: the status page shows them all, and there is room there
		// for their meaning beside them. Here stays what a two-second glance can
		// use — who is watching, and how long it has been running.
		//
		// What is missing from these lines has not disappeared: the **faults**
		// come in as a line of their own in the cases below, which is the moment
		// when they really count. A menu that always lists everything makes
		// nothing stand out.
		//
		// **Numbers pass through here, not sentences.** The words are chosen by
		// the tray, which is the only one that knows which language it is
		// speaking: since it takes that from Windows, a line composed here would
		// be Italian inside a German menu.
		Viewers: s.Viewers,
		Devices: int64(s.Devices),
		Uptime:  s.Uptime,
	}
	if s.Remote.Phase == tunnel.PhaseRunning && s.Remote.PublicURL != "" {
		out.PublicURL = s.Remote.PublicURL
	}

	// The step that falls to the user reaches the tray only if it has an address
	// to open. A menu item that describes a problem without leading anywhere does
	// what a log line does, but occupying the place where a command is expected.
	//
	// **It is the only tray field that arrives already written**, and that is
	// deliberate: it can carry Tailscale's sentence, which is not translated
	// because the control server composes it knowing the tailnet and the reader's
	// role. Not being able to be a code always, it is resolved here — with the
	// **tray's dictionary**, which is the one of whoever is in front of the
	// machine, not with one opened on its own account.
	if s.Remote.Action != tunnel.ActionNone && s.Remote.ActionURL != "" {
		text := s.Remote.ActionText
		if text == "" {
			text = dict.T("tunnel.action." + string(s.Remote.Action))
		}
		out.Todo = firstLine(text)
		out.TodoURL = s.Remote.ActionURL
	}

	switch {
	// Without a password the monitor is reachable by nobody and public exposure
	// stays closed: it is the most urgent thing it can have to say, and the
	// remedy is to open the page — which is precisely what the double click does.
	case !cfg.HasPassword():
		out.Phase = tray.PhaseCheck
		// The click leads straight to choosing the password. **Only the
		// click**: the home address stays the home address, because without a
		// password the Funnel does not come on and that QR code is the only way
		// of finishing the setup from a phone. From there `/` redirects to
		// `/setup`, which from the home network is open — what gets refused are
		// the requests arriving from the Internet.
		out.OpenURL = localURL(cfg.ListenAddr, "/setup")
		out.Fault = tray.FaultNoPassword
		out.Note = tray.NoteNoPassword

	// **Windows is refusing the camera**, and that comes before the camera having
	// stopped: both are true — a refused device delivers nothing — and only one
	// of the two says what to do about it. It is the alerts' own precedence,
	// which is where the argument is written; here it also decides the colour,
	// and brick is right, because there is no picture at all.
	//
	// **It sits above the microphone's cases for the reason the case below
	// states**: a colour says one thing and it has to say the worst, and no
	// picture beats a filtered microphone. The two refusals are two switches in
	// Windows and the camera's is the graver, so it is first — with both off,
	// the panel offers the camera's page and the microphone's alert is still on
	// the phone.
	case cameraDenied(s):
		out.Phase = tray.PhaseFault
		out.Fault = tray.FaultCameraDenied

	// No stream after half a minute is no longer slowness: the camera opens in a
	// few seconds. It sits **before** the microphone, and the live test is what
	// imposed that: on a machine with the webcam held by another process and no
	// default microphone, the tray declared "to be checked" — that is, it
	// reported the lesser fault and hid the greater. A colour can say one thing
	// and it has to say the worst.
	case captureStopped(s, startedAt, time.Now()):
		out.Phase = tray.PhaseFault
		out.Fault = tray.FaultCaptureStopped

	// The microphone's half of the refusal above, and it takes precedence over
	// the case below for the same reason: a microphone the user has taken away
	// is not a microphone that has broken, and only this says where the switch
	// is.
	case micDenied(s):
		out.Phase = tray.PhaseCheck
		out.Fault = tray.FaultMicDenied

	// Digital silence is the most insidious fault of a baby monitor: green page,
	// still meter, and the child crying without anybody hearing.
	//
	// **The comparison is on a code, not on a sentence.** A comparison against
	// the shown phrase would be turned off by translating the interface, with
	// nothing to say so — that is, it would remove the warning from the worst
	// fault of all. Covered by traystatus_test.go.
	case micSilent(s) || micMissing(s):
		out.Phase = tray.PhaseCheck
		if micMissing(s) {
			out.Fault = tray.FaultMicMissing
		} else {
			out.Fault = tray.FaultMicSilent
		}

	// Starting up has no menu line — it is not a fault, and in two seconds it is
	// gone — but the tooltip has a sentence for it anyway: brushing the icon and
	// reading the counters while the camera is still opening is the answer to a
	// question nobody asked.
	case !s.Ready:
		out.Phase = tray.PhaseStarting
		out.Note = tray.NoteStarting

	case !s.RawAudio:
		out.Phase = tray.PhaseCheck
		out.Fault = tray.FaultMicFiltered

	// The chosen camera is not connected, so another one is on screen. **It
	// sits below the microphone's cases and above remote access**, which is
	// where its severity puts it: the room can be heard, and what is in doubt
	// is whether the picture is of the right room. It is a note and not a
	// fault, because there is a live picture — see alerts.CameraOther.
	case cameraOther(s):
		out.Phase = tray.PhaseCheck
		out.Note = tray.NoteCameraOther

	// Remote access that fails **is not a fault of the monitor**: at home it can
	// be seen perfectly well, and it is the same rule by which the two lives do
	// not switch each other off. It is to be said, not dramatised.
	case remoteDown(s) && s.Remote.Warning == "":
		out.Phase = tray.PhaseCheck
		out.Note = tray.NoteRemoteDown

	// The tunnel is open and the address is there, but Tailscale has not granted
	// the ingress: from the Internet there is no way through. **It is the worst
	// of the cases that look fine** — green icon and an address to copy, with
	// whoever opens it from outside seeing nothing and going to look for the
	// fault in their own network. It is worth an amber, and that is why Warning
	// exists.
	case s.Remote.Warning != "":
		out.Phase = tray.PhaseCheck
		out.Fault = tray.FaultRemoteNoIngress

	// Remote access is open and it is still being proved that one can get in
	// from outside. **It is not a phase of its own and must not be**: the colour
	// stays that of a ready monitor, because ready it is — what is missing is the
	// proof. Saying so are the tooltip's sentence and the icon's breathing, both
	// of which take it from this note.
	case out.PublicURL != "" && s.Remote.Reach == tunnel.ReachChecking:
		out.Phase = tray.PhaseOutside
		out.Note = tray.NoteVerifying

	case out.PublicURL != "":
		out.Phase = tray.PhaseOutside

	default:
		out.Phase = tray.PhaseHome
	}

	// **A newer version is set after the switch and not inside it**, and the two
	// halves go to two different places on purpose.
	//
	// The **command** is filled whatever else is true. It is not a competitor of
	// the cases above: they decide the colour and the sentence, this adds a row
	// to a panel — and the case that settles it is the worst one, because a
	// monitor whose camera keeps stopping is exactly the one whose owner wants
	// the release with the fix. Put in the switch it would have been hidden by
	// every fault, that is, taken away in the hour it is worth having.
	//
	// The **sentence** yields to anything else. `summary` gives a Note
	// precedence over a Fault in the tooltip, so setting one here
	// unconditionally would have written "a newer version is available" over
	// "the camera has stopped" — the tooltip is brushed to find out whether one
	// can go to bed, and a version number is not the answer to that. It is the
	// rule the tray already holds: a fault always beats the sentence.
	out.UpdateVersion, out.UpdateURL = up.Version, up.URL
	if out.UpdateVersion != "" && out.Fault == tray.FaultNone && out.Note == tray.NoteNone {
		out.Note = tray.NoteUpdate
	}

	// **The line "where it can be seen and who is watching" is composed by the
	// tray**, which for that has the numbers and its own language's plural. Here
	// the only thing decided is whether there is something more urgent to say in
	// its place, which is the real decision.
	return out
}

// watchForUpdates asks GitHub, now and then, whether a newer release exists.
//
// **It says so and does nothing else.** No download, no replacement, no
// restart: the answer reaches the notification area and the log, and acting on
// it is a gesture made in front of the machine — the same division the password
// reset and the session revocation already follow, and for the same reason.
//
// **The balloon fires once per version, not once per answer.** The check runs
// every day for the life of an installation, and a monitor that pops the same
// notice every morning is a monitor whose notices stop being read — which
// matters here more than anywhere, because this program's other notifications
// are the ones about a child. The panel's row stays for as long as the release
// does, which is where somebody who dismissed the balloon goes to find it
// again.
func watchForUpdates(ctx context.Context, tr *tray.Tray, c *update.Checker, log *slog.Logger) {
	// **Nothing asks during start-up.** The camera, the microphone and the
	// tunnel are all opening, and on a machine that has just booted the network
	// may not be up at all — an attempt there would fail for a reason that says
	// nothing about anything. Nobody is waiting on this, so it can afford to go
	// last.
	timer := time.NewTimer(update.FirstDelay)
	defer timer.Stop()

	var told, said string
	for {
		select {
		case <-ctx.Done():
			return
		case <-timer.C:
		}
		timer.Reset(update.Interval)

		st, err := c.Check(ctx)
		if err != nil {
			// **A failed check is a debug line, and that is the whole rule
			// about how much gets written.** This runs on a machine that may
			// have no network for a night, and an hourly warning about a
			// courtesy nobody asked for would bury the lines that say what
			// happened to the camera. The state stays Unknown, which the tray
			// renders as silence rather than as "you are up to date".
			log.Debug("the update check did not get an answer", "error", err)
			continue
		}
		// **Written when the answer changes, never while it stays the same.**
		// Daily for a year is 365 identical lines, and a log that repeats
		// itself hides the thing that happened once.
		if s := string(st.Code) + " " + st.Version; s != said {
			said = s
			switch st.Code {
			case update.Available:
				log.Info("a newer version is available",
					"version", st.Version, "running", version.Number, "page", st.URL)
			case update.Current:
				log.Debug("this is the newest release", "running", version.Number)
			}
		}
		if st.Code == update.Available && st.Version != told {
			told = st.Version
			tr.NotifyUpdate(st.Version)
		}
	}
}

// announcePublicAddress gives notice when the monitor becomes reachable from
// outside, and once only.
//
// It is the only moment when there is really something new to say, and it is
// also the moment when the information is needed: the address has to be
// transferred to a phone, and the balloon carries with it the menu item that
// copies it.
func announcePublicAddress(ctx context.Context, tr *tray.Tray, t *tunnel.Tunnel) {
	tick := time.NewTicker(2 * time.Second)
	defer tick.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-tick.C:
			st := t.State()
			if st.Phase == tunnel.PhaseRunning && st.PublicURL != "" {
				tr.NotifyPublicAddress(st.PublicURL)
				return
			}
		}
	}
}

// remoteState reports the state of remote access, even when it is off.
//
// An explicit "disabled" state is preferable to an absent field: the page has to
// be able to say that remote access is not there, instead of staying silent and
// letting one believe it is.
func remoteState(t *tunnel.Tunnel) tunnel.State { return t.State() }

// encoderVendor translates, for the status page, the fact that the encoding is
// happening on the GPU.
//
// It is not enough for a hardware encoder to exist: if it did not accept the
// Direct3D device it is working on the CPU, and declaring it "hardware" would
// hide exactly the case the user needs to see.
func encoderVendor(hardware bool) string {
	if hardware {
		return "hardware"
	}
	return "software"
}

// videoKbps is the bandwidth the video is **really occupying**, not the one it
// has been granted.
//
// The two numbers coincide in CBR, where the encoder fills the cap, and diverge
// exactly when the difference matters: under constant quality the cap stays
// where it is while the throughput falls. Showing the cap would say "2500" all
// night even while 900 are being used, that is, it would hide precisely the
// saving that mode exists to obtain.
//
// It is the same correction already made for the cadence and the resolution: the
// status page has to say what is happening, not what was asked for.
func videoKbps(hub *rtc.Hub) int {
	if v := hub.MeasuredVideoKbps(); v > 0 {
		return v
	}
	return hub.TargetBitrateKbps()
}

// startProfiler opens `net/http/pprof` on a loopback address.
//
// **It refuses any address that is not loopback, and that is not pedantry.**
// Those routes expose the stacks of every goroutine and let whoever asks run a
// profile: on a program that publishes an address on the Internet they would be
// at once an information leak and a way of making the machine work from outside.
// The check lives here and not in a recommendation in the guide, because a
// recommendation gets skipped.
//
// It is declared in the log at WARN level, like the test tone and the forced
// bitrate: a diagnostic mode left on by forgetfulness has to be visible by
// reading the log, not by deduction.
func startProfiler(log *slog.Logger, addr string) error {
	host, _, err := net.SplitHostPort(addr)
	if err != nil {
		return fmt.Errorf("invalid profiler address: %w", err)
	}
	loopback := host == "localhost"
	if ip := net.ParseIP(host); ip != nil {
		loopback = ip.IsLoopback()
	}
	if !loopback {
		return fmt.Errorf("the profiler listens on loopback only, not on %q", host)
	}

	ln, err := net.Listen("tcp", addr)
	if err != nil {
		return fmt.Errorf("profiler: %w", err)
	}
	log.Warn("profiler enabled: it exposes the internal state of the process",
		"address", "http://"+ln.Addr().String()+"/debug/pprof/",
		"note", "a diagnostic mode, do not leave it on")
	guard.Go(log, "the profiler", func() {
		// DefaultServeMux is where `net/http/pprof` registers itself. The
		// monitor's server has its own mux, so these routes do not touch it.
		srv := &http.Server{Handler: http.DefaultServeMux, ReadHeaderTimeout: 10 * time.Second}
		if err := srv.Serve(ln); err != nil && !errors.Is(err, http.ErrServerClosed) {
			log.Warn("profiler stopped", "error", err)
		}
	})
	return nil
}

// declaredFPS is the cadence the encoder is receiving, not the preset's.
//
// **A constant here is wrong.** The status page shows "measured/declared" —
// 9.7/30 — and with the declared one nailed to the preset that ratio tells a
// false story: it looks as though the camera were delivering a third of what it
// is asked for, while the monitor has already adjusted the cadence and is
// declaring 10 to the encoder. The number is not slightly wrong: it is wrong
// **at the exact moment it is needed**, that is, when something has changed.
//
// Found by testing the cadence in the dark, which is the only condition in which
// the two numbers diverge — in the light they both sit at 30, and that is why it
// went unnoticed.
func declaredFPS(p *pipeline.Pipeline, settings encoder.Settings) int {
	if _, _, fps := p.VideoFormat(); fps > 0 {
		return fps
	}
	return settings.FPS
}

// videoResolution is the size that is on air now, which is not always the
// preset's: the resolution scale may have lowered it because the bandwidth did
// not cover all the pixels.
//
// Declaring the user's choice anyway would be the kind of lie that costs a
// diagnosis: whoever says "the picture is bad" would look at a page claiming
// 1280x720 while 640x360 arrives, and would look for the fault everywhere except
// where it is. Before the camera opens there is nothing on air yet, and there
// the preset is the right answer.
//
// **It is the size and nothing else.** It used to append `@N fps` when the
// declared cadence was below the preset, and the page appended its own on top:
// out came `1280x720@20 fps @20.0/20fps · 1334 kbit/s`, that is, the same number
// twice and neither of them what the line meant. The declared cadence follows
// the camera and drops on its own in the dark, so that suffix lit up every
// evening; the **delivered** cadence — the only one that says whether frames are
// being dropped — did not appear at all. It now travels on its own, in
// `DeliveredFPS`.
func videoResolution(p *pipeline.Pipeline, settings encoder.Settings) string {
	w, h, _ := p.VideoFormat()
	if w <= 0 || h <= 0 {
		return fmt.Sprintf("%dx%d", settings.Width, settings.Height)
	}
	return fmt.Sprintf("%dx%d", w, h)
}

// chosenCamera is the link of the camera the configuration asks for, empty
// meaning "the first usable one".
//
// **It resolves a wish, it does not open anything**, and the difference is the
// whole point: whether that camera is connected is a question that has to be
// asked again at every open, because a webcam can be unplugged while the
// monitor runs. Answering it here would freeze the answer at start-up — the
// same defect already paid for with the capture size.
//
// So there is one thing to do here that cannot be done later: **the superseded
// `camera_name` key names a camera by a name, and a name is not what gets
// reopened.** Resolving it into a link is a migration, it needs a list, and it
// happens once.
func chosenCamera(all []devices.Device, wantID, supersededName string, log *slog.Logger) string {
	// **They are not two ways of choosing**: the id is the choice and the name
	// is the migration of an older one, so the id commands and the name is not
	// even looked at. A superseded key that still commands says so, once —
	// otherwise the day somebody wonders why the file's id is ignored there is
	// nothing to read.
	if wantID != "" {
		return wantID
	}
	if supersededName == "" {
		return ""
	}
	log.Warn("camera chosen by the superseded camera_name key",
		"name", supersededName,
		"note", "camera_device_id identifies it; a name is shared by two cameras of the same model")
	cams := devices.Cameras(all)
	for _, c := range cams {
		if strings.EqualFold(c.Name, supersededName) {
			return c.Link()
		}
	}
	// **The name that matches nothing is not carried on as a link.** It would
	// become a choice that can never be satisfied — every open falling back and
	// declaring it — for a key that was only ever a name. Better the first
	// usable camera, said out loud.
	log.Warn("the camera named by camera_name is not connected: the first usable one will be opened",
		"name", supersededName, "available", devices.Names(cams))
	return ""
}

// iceServers builds the list of ICE servers from the configuration, and
// **discards those the consumer would refuse**.
//
// The defect it closes is asymmetric in a way that does not show: a badly
// written `stun:` line **does not stop the start** — what refuses it is
// `NewPeerConnection`, which the hub calls **once per viewer**. That is, a
// mistake in one configuration line breaks *every* viewer, and it does so at
// connection time: from the monitor's log one sees one failure per visit and
// never a cause.
//
// **The question is put to the consumer, not to a validator of ours.** The rules
// live in `ICEServer.urls()`, which is not exported, and rewriting them here
// would mean a second copy that agrees with itself: it would get wrong precisely
// the cases we have not thought of.
//
// **Each URL becomes an entry of its own, and that is what makes the loop mean
// anything.** Grouped into a single `ICEServer` the consumer answers about the
// whole list — so one typo in `stun_servers` took every good server with it, and
// the comment here claimed the opposite while the test written beside it
// asserted the loss as if it were intended. A wrong row now costs its own row.
//
// It kept a `turn_url` once. See "A relay is not shipped, and the reason is what
// it would carry" in CLAUDE.md.
func iceServers(cfg config.Config, log *slog.Logger) []webrtc.ICEServer {
	var out []webrtc.ICEServer
	for _, u := range cfg.STUNServers {
		s := webrtc.ICEServer{URLs: []string{u}}
		if err := iceServerUsable(s); err != nil {
			log.Error("an ICE server was refused and will not be used",
				"urls", s.URLs, "error", err)
			continue
		}
		out = append(out, s)
	}
	return out
}

// iceServerUsable asks the consumer whether this entry is acceptable.
//
// A PeerConnection is built and closed, which is exactly what the hub does for
// every viewer: in exchange the error read in the log is **the same one** the
// viewer would have seen. Asking one server at a time costs one of those per
// configured server, and the measurement separates the two halves of that price:
// the **first** PeerConnection of the process is worth **88 ms** whatever it
// validates, and every server after it about **half a millisecond**. So the
// split costs the default pair half a millisecond, not a second 88 — and the
// 88 was being paid before this function ever split anything.
func iceServerUsable(s webrtc.ICEServer) error {
	pc, err := webrtc.NewPeerConnection(webrtc.Configuration{
		ICEServers: []webrtc.ICEServer{s},
	})
	if err != nil {
		return err
	}
	return pc.Close()
}

// promptAndSetPassword sets the password from the command line.
func promptAndSetPassword(cfg *config.Config) error {
	fmt.Print("New password: ")
	first, err := term.ReadPassword(int(os.Stdin.Fd()))
	fmt.Println()
	if err != nil {
		return fmt.Errorf("reading the password: %w", err)
	}
	fmt.Print("Repeat the password: ")
	second, err := term.ReadPassword(int(os.Stdin.Fd()))
	fmt.Println()
	if err != nil {
		return fmt.Errorf("reading the password: %w", err)
	}
	if string(first) != string(second) {
		return errors.New("the two passwords do not match")
	}

	hash, err := config.HashPassword(string(first))
	if err != nil {
		return err
	}
	cfg.PasswordHash = hash
	if err := cfg.Save(); err != nil {
		return err
	}
	fmt.Printf("Password set in %s\n", cfg.Path())
	return nil
}

// openSetup opens the guided path in the default browser.
//
// **It waits for the server to answer before opening.** Opening at once, the
// browser arrives before the listener and shows "cannot connect": the first
// contact with the product becomes an error page, and whoever sees it does not
// reload, they close.
//
// The page is handed to the shell through `tray.Open`, the road the icon's click
// and the panel's two folders take: **the monitor asks the shell and starts no
// program of its own**.
//
// An opening that fails **stops nothing**: the address is written in the log and
// things go on. Refusing to watch over a child because a browser could not be
// opened would be absurd.
func openSetup(ctx context.Context, log *slog.Logger, url string) {
	if !waitForServer(ctx, url) {
		log.Warn("guided setup not opened: the server did not answer in time",
			"address", url)
		return
	}
	log.Info("first run: opening the guided setup in the browser", "address", url)
	if err := tray.Open(url); err != nil {
		log.Warn("browser not opened: open the address by hand",
			"address", url, "error", err)
	}
}

// waitForServer waits for the local listener to answer, ten seconds at most.
func waitForServer(ctx context.Context, url string) bool {
	deadline, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	for {
		req, err := http.NewRequestWithContext(deadline, http.MethodHead, url, nil)
		if err != nil {
			return false
		}
		// Any answer will do, even a 404: it means somebody is listening,
		// which is the only thing being waited for.
		if resp, err := http.DefaultClient.Do(req); err == nil {
			resp.Body.Close()
			return true
		}
		select {
		case <-deadline.Done():
			return false
		case <-time.After(150 * time.Millisecond):
		}
	}
}

// localURL builds an openable URL from the listen address.
//
// ListenAddr can be ":8080" (host omitted) or "127.0.0.1:8099": a plain
// concatenation would produce "localhost127.0.0.1:8099".
func localURL(listenAddr, path string) string {
	host, port, err := net.SplitHostPort(listenAddr)
	if err != nil {
		return "http://localhost" + listenAddr + path
	}
	// An address on every interface is not a reachable host.
	if host == "" || host == "0.0.0.0" || host == "::" || host == "[::]" {
		host = "localhost"
	}
	return "http://" + net.JoinHostPort(host, port) + path
}

// addressOthersCanReach keeps only the addresses that hold from another device
// too, and returns empty for the rest.
//
// **`homeAddress` never comes back empty**: with no IPv4 route — wifi and
// ethernet down together — its last fallback is `localhost`, which for the page
// running on this machine is true and for the panel is false. The tray
// **shows** that address and **engraves it in a QR code**, that is, it invites a
// gesture that cannot succeed: a false affordance costs more than a missing one,
// and the panel already knows how to treat an empty one — no line and no code,
// instead of a space that looks like a half-loaded image.
//
// **`homeAddress` was not corrected because it has two readers**, and for the
// other one that fallback is the right answer: the guided path's last screen is
// read from this machine, where `localhost` works. Whoever hands the address to
// somebody else filters; whoever opens it here does not. It is the same
// distinction as OpenURL and HomeURL, one step further along.
//
// The case is rare and the monitor is unreachable there anyway: what is gained
// is not one more link, it is not promising one.
func addressOthersCanReach(raw string) string {
	if raw == "" {
		return ""
	}
	u, err := url.Parse(raw)
	if err != nil {
		return ""
	}
	host := u.Hostname()
	switch {
	case host == "":
		return ""
	// The name, which is what `localURL` writes, and the number, which could
	// come from a configuration. **They are two roads to the same thing** and
	// both have to be closed: looking only at the word lets `127.0.0.1` through,
	// and looking only at the number lets `localhost` through.
	case strings.EqualFold(host, "localhost"):
		return ""
	}
	if ip := net.ParseIP(host); ip != nil && (ip.IsLoopback() || ip.IsUnspecified()) {
		return ""
	}
	return raw
}

// homeAddress is the URL to give to whoever watches from a phone, and it is not
// localhost.
//
// "At home", on the last screen of the guided path, means "from another device
// on the wifi", and there `localhost` is the one address that certainly does
// **not** work: only this computer sees it. The defect is silent, because the
// page that shows it runs on that very machine and there the address is fine.
//
// **The system is asked which interface it would use, instead of us choosing.**
// A laptop often has five — wifi, ethernet, the virtual switches of Hyper-V and
// WSL, the Tailscale node — and all of them have a private address with the
// right look about it: taking the first in the list is the way to print a
// plausible, wrong number. Opening a UDP socket makes the system apply its own
// routing table and declare which address it would start from. No packet is
// sent: `connect` on UDP merely picks the road.
//
// The destination is in 192.0.2.0/24, which RFC 5737 reserves for
// documentation: it serves only to have the table consulted, and must not be
// somebody's address.
//
// If there is no default route — a machine that is disconnected — nothing is
// invented: it falls back to `localURL`, which at least is true for whoever is
// sitting in front of it.
func homeAddress(listenAddr string) string {
	host, port, err := net.SplitHostPort(listenAddr)
	if err != nil {
		return localURL(listenAddr, "/")
	}
	// An address chosen by the user is respected: if they have bound the server
	// to a particular interface, that is the answer and there is nothing to
	// discover.
	if host != "" && host != "0.0.0.0" && host != "::" && host != "[::]" {
		return localURL(listenAddr, "/")
	}

	c, err := net.Dial("udp4", "192.0.2.1:9")
	if err != nil {
		return localURL(listenAddr, "/")
	}
	defer c.Close()
	ua, ok := c.LocalAddr().(*net.UDPAddr)
	if !ok || ua.IP == nil || ua.IP.IsLoopback() || ua.IP.IsUnspecified() {
		return localURL(listenAddr, "/")
	}
	return "http://" + net.JoinHostPort(ua.IP.String(), port) + "/"
}

func boolLabel(v bool, yes, no string) string {
	if v {
		return yes
	}
	return no
}
