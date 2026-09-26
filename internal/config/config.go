// Package config handles the persistent configuration and the credentials.
package config

import (
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"golang.org/x/crypto/argon2"
	"gopkg.in/yaml.v3"
)

// Config is the persistent state of the application.
type Config struct {
	// ListenAddr is the address of the local server. By default it listens on
	// every interface: the LAN is trusted only as far as authentication stands
	// in front of it anyway.
	ListenAddr string `yaml:"listen_addr"`

	// PasswordHash is the argon2id hash in PHC format. Empty means first run:
	// the app asks for a password to be set and stays unexposed.
	PasswordHash string `yaml:"password_hash"`

	// SessionTTLHours is how long an authenticated session lasts.
	SessionTTLHours int `yaml:"session_ttl_hours"`

	// CameraDeviceID is the camera's symbolic link, which is the name Media
	// Foundation opens it by; empty uses the first usable one.
	//
	// **It is the link and not the friendly name, because the name does not
	// identify**: two cameras of the same model carry the same one, and choosing
	// between them by name is a coin toss. It is the same shape as MicDeviceID,
	// for the same reason.
	CameraDeviceID string `yaml:"camera_device_id"`

	// CameraName is the superseded key. It chose the camera by name, and it is
	// still read when CameraDeviceID is empty, so a configuration written before
	// the change goes on opening the camera its owner chose. It is not a second
	// way of choosing: it is the migration of an older one, and the first choice
	// written as an id leaves it behind for good.
	CameraName string `yaml:"camera_name"`
	// MicDeviceID is the WASAPI endpoint ID; empty uses the default one.
	MicDeviceID string  `yaml:"mic_device_id"`
	MicGainDB   float64 `yaml:"mic_gain_db"`
	// SpeakerDeviceID is the talk-back's audio output; empty uses the default
	// one — which is also the only one that follows a user swapping headphones
	// without restarting the monitor. An id written here and no longer present
	// does **not** fall back to the default silently: whoever chose a speaker
	// must not find themselves talking out of another one unawares.
	SpeakerDeviceID string `yaml:"speaker_device_id"`

	// Quality is the name of the video preset (see encoder.Presets).
	Quality string `yaml:"quality"`
	// PreferEncoder forces a specific encoder; empty lets the probe decide.
	PreferEncoder string `yaml:"prefer_encoder"`

	// There is deliberately no rate_control, min_qp or max_qp here. The saving
	// is done by target_qp commanding the bitrate alone, while a quality floor
	// inside the encoder puts a second quality controller under the feet of the
	// first, and two controllers fighting each other is a fault this project has
	// measured. To measure the knee of that curve the flags are still on
	// pat-capture, which is a measuring tool.

	// TargetQP is the quality to hold: the quantiser the picture is wanted to
	// come out at. Thirty by default; zero switches the saving off.
	//
	// **It is the only number that governs the bandwidth saving.** The bitrate
	// comes down when the picture comes out better than this, because those
	// bits cannot be seen, and goes back up as soon as it gets worse — always
	// inside the cap the network allows. Nothing special is asked of the
	// encoder: only a bitrate, in CBR, which is the one thing they all know how
	// to do.
	//
	// Thirty is the value our measurements point at for a clean picture, and it
	// is the same number that came out of the tests on two machines with
	// encoders from different vendors — the quantiser is normative, so "30"
	// means the same quantisation step everywhere, even though the resulting
	// quality depends on the chip. Raising it saves bandwidth and coarsens the
	// picture, lowering it spends: it shows, both ways, and it does not fail
	// quietly.
	//
	// It is checked with pat-capture, watching the quantiser it reports.
	TargetQP int `yaml:"target_qp"`

	// DetectCry, DetectBark and DetectMotion say **what the monitor is to watch
	// and listen for in this room**, and that is why they live in the monitor's
	// configuration rather than in the viewer's browser: that there is a dog is
	// a fact about the installation, not a viewer's preference, and two parents
	// with two phones have to get the same answer. They are also the only way
	// to **not produce** a false positive instead of hiding it: someone with no
	// baby switches cry off and never hears it be wrong.
	//
	// On by default: detection is the reason the product exists, and whoever
	// has only the dog notices on the first night and switches off a toggle
	// that is there for the purpose. They stay on when an old configuration is
	// loaded too, because Load starts from Default and missing keys zero
	// nothing.
	DetectCry    bool `yaml:"detect_cry"`
	DetectBark   bool `yaml:"detect_bark"`
	DetectMotion bool `yaml:"detect_motion"`

	// ClipsMaxMB and ClipsMaxDays are the retention of the event clips.
	//
	// **There are two because they answer two different questions.** Space is
	// the promise that matters — a monitor that runs every night must not be
	// able to fill the disk of whoever hosts it while nobody is watching — and
	// age keeps six-month-old nights from being kept just because there is
	// room. Deletion starts from the oldest as soon as either one bites.
	//
	// Zero, in either, means **no limit**: that is the right way to read a
	// missing value, because the other one would delete the recordings of
	// whoever wrote an incomplete configuration.
	//
	// Clips marked "keep this one" neither count nor expire: that is what the
	// lock means, and whoever locks fifty of them takes up what they take up.
	ClipsMaxMB   int `yaml:"clips_max_mb"`
	ClipsMaxDays int `yaml:"clips_max_days"`

	// STUNServers are what WebRTC hole punching needs. Without them the media
	// only works on the LAN.
	//
	// **An empty list is not a choice: `Load` puts the default pair back.** A
	// missing key and an explicitly empty one arrive there looking identical —
	// the file is unmarshalled over a value already filled from `Default()` — and
	// restoring is the right answer for the first. So `stun_servers: []` is not
	// the way to turn STUN off, and nothing downstream expects an empty list
	// from a loaded configuration: the "no usable STUN server" diagnosis in
	// internal/server/nopath.go is reachable only through entries being refused
	// at start-up.
	STUNServers []string `yaml:"stun_servers"`

	// UpdateCheck asks GitHub, once a day, whether a newer release exists.
	//
	// **It only ever says so.** Nothing is downloaded, nothing is replaced and
	// nothing restarts: the answer reaches the notification area and the log,
	// and applying an update stays a gesture made in front of the machine. That
	// is the same rule the password reset and the session revocation follow,
	// and for the same reason — it rewrites the executable, and whoever has to
	// recover from a bad one is the person standing at the computer.
	//
	// **On by default, and it is the honest default for this program**: a baby
	// monitor runs unattended for months, so the one thing its owner cannot do
	// is notice that a fix exists. A check nobody knows about is a check nobody
	// turns on.
	//
	// **What it costs is declared, because it is the reason somebody would
	// switch it off**: the request leaves the house, so GitHub sees this
	// machine's address and the version it is running, once a day. Nothing else
	// goes with it — not the configuration, not the tailnet name, not who is
	// watching. Whoever would rather it did not happen writes
	// `update_check: false`, and the monitor never speaks to anybody.
	UpdateCheck bool `yaml:"update_check"`

	// Funnel controls the public exposure through Tailscale. Off by default: it
	// is only enabled once a password has been set.
	FunnelEnabled    bool   `yaml:"funnel_enabled"`
	FunnelHostname   string `yaml:"funnel_hostname"`
	TailscaleAuthKey string `yaml:"tailscale_auth_key"`

	// OnboardingDone says the first-run path has been completed, or skipped on
	// purpose.
	//
	// **It is there so the browser is not opened at every start.** The monitor
	// is meant to come up on its own when the computer does, and a program that
	// opens a tab on every restart becomes a program that gets uninstalled.
	//
	// **It is distinct from HasPassword, and that is not redundancy.** Someone
	// who already had a password before the path existed must not have to redo
	// it, and someone who chose "the home network is enough" at the fork has
	// finished the path without having the Funnel: two different questions, and
	// a single field would answer one of them badly.
	OnboardingDone bool `yaml:"onboarding_done"`

	// path is the file the configuration was loaded from.
	path string `yaml:"-"`
}

// Default returns a sensible starting configuration.
func Default() Config {
	return Config{
		ListenAddr:      ":8080",
		SessionTTLHours: 24 * 7,
		Quality:         "high",
		// **The saving is on by default**, and it was switched on only after
		// being measured on two machines and three encoders — an improvement is
		// not imposed before it is known to hold in the worst case. Leaving it
		// off now would mean nobody ever uses it, because nobody writes by hand
		// a line they do not know exists.
		//
		// Thirty is the value both machines produced, and that is no
		// coincidence: the quantiser is normative. Whoever does not want it
		// writes `target_qp: 0`.
		TargetQP:     30,
		DetectCry:    true,
		DetectBark:   true,
		DetectMotion: true,
		// On, for the reason written on the field: the owner of a monitor that
		// runs unattended is the one person who cannot notice a fix exists.
		// Like the three above it survives an older configuration file, because
		// Load starts from here and a missing key zeroes nothing.
		UpdateCheck: true,
		// A gigabyte and two weeks: with clips of a few megabytes that is some
		// hundreds of recordings, which is enough for the cap not to bite on a
		// normal night and little enough for nobody to notice.
		ClipsMaxMB:   1000,
		ClipsMaxDays: 14,
		STUNServers: []string{
			"stun:stun.l.google.com:19302",
			"stun:stun.cloudflare.com:3478",
		},
		FunnelHostname: DefaultFunnelHostname(),
	}
}

// DefaultFunnelHostname is the first label of the public name, one per machine.
//
// **A fixed default collides on the second installation.** Tailscale does not
// refuse a name already taken: it renames the node itself — `patmon` becomes
// `patmon-1` — and it does so silently. The URL we show stays right, because we
// read it from the node and not from what we asked for, but whoever had already
// bookmarked it on their phone is left with an address that does not answer. It
// is the kind of fault that never shows while there is a single installation,
// that is, exactly until the thing gets distributed.
//
// **The suffix is a fingerprint, not the name of the computer.** Putting the
// machine name in would be more readable, but this label ends up in a
// **public** DNS name: "desktop-anna" would become something anyone can read.
// And readable buys nothing — this is not a label, it is an address, and nobody
// types it.
//
// Deterministic: the same machine always gives the same name, even before the
// configuration has been saved for the first time. If there is no way to tell
// the machine apart it falls back to the bare name — better a possible
// collision than a different name at every start, which would be a new node
// every time.
func DefaultFunnelHostname() string {
	h, err := os.Hostname()
	if err != nil || strings.TrimSpace(h) == "" {
		return "patmon"
	}
	sum := sha256.Sum256([]byte(strings.ToLower(strings.TrimSpace(h))))
	return "patmon-" + hex.EncodeToString(sum[:3])
}

// dataDir is the data folder under %APPDATA%: configuration, log and `tsnet/`,
// that is, the identity of the Tailscale node.
const dataDir = "PAT Monitor"

// DefaultPath is the default path of the configuration file.
func DefaultPath() (string, error) {
	dir, err := os.UserConfigDir()
	if err != nil {
		return "", fmt.Errorf("configuration folder cannot be determined: %w", err)
	}
	return filepath.Join(dir, dataDir, "config.yaml"), nil
}

// Load reads the configuration; if the file does not exist it returns the
// defaults without an error, so the first run works on its own.
func Load(path string) (Config, error) {
	cfg := Default()
	cfg.path = path

	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return cfg, nil
	}
	if err != nil {
		return cfg, fmt.Errorf("reading %s: %w", path, err)
	}
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return cfg, fmt.Errorf("parsing %s: %w", path, err)
	}
	cfg.path = path

	// Restore the missing values after a partial load.
	d := Default()
	if cfg.ListenAddr == "" {
		cfg.ListenAddr = d.ListenAddr
	}
	if cfg.SessionTTLHours <= 0 {
		cfg.SessionTTLHours = d.SessionTTLHours
	}
	if cfg.Quality == "" {
		cfg.Quality = d.Quality
	}
	if len(cfg.STUNServers) == 0 {
		cfg.STUNServers = d.STUNServers
	}
	return cfg, nil
}

// Save writes the configuration to disk: the file holds the password hash and,
// where configured, the Tailscale auth key.
//
// **The modes below say nothing on Windows**, where Go maps a permission to
// the read-only attribute and no more; they are kept for the platforms where
// they mean something. What keeps other accounts out here is the folder's own
// ACL — `%APPDATA%` is the user's and nobody else's — so a `-config` pointed
// outside the profile, at a folder every account may write, carries that
// folder's openness with it.
func (c Config) Save() error {
	if c.path == "" {
		return errors.New("configuration path not set")
	}
	if err := os.MkdirAll(filepath.Dir(c.path), 0o700); err != nil {
		return fmt.Errorf("creating the configuration folder: %w", err)
	}
	data, err := yaml.Marshal(c)
	if err != nil {
		return fmt.Errorf("serialising the configuration: %w", err)
	}

	// Atomic write: an interruption must not leave a mutilated file.
	tmp := c.path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o600); err != nil {
		return fmt.Errorf("writing the configuration: %w", err)
	}
	if err := os.Rename(tmp, c.path); err != nil {
		_ = os.Remove(tmp)
		return fmt.Errorf("replacing the configuration: %w", err)
	}
	return nil
}

// Path returns the path of the configuration file.
func (c Config) Path() string { return c.path }

// SetPath sets the path to save to.
func (c *Config) SetPath(p string) { c.path = p }

// NeedsOnboarding says whether the configuration path is to be opened at start.
//
// A password already set counts as a path already walked: that is the
// installation that existed before the path did, and offering it again would be
// asking someone to redo something already done.
func (c Config) NeedsOnboarding() bool { return !c.OnboardingDone && !c.HasPassword() }

// HasPassword says whether a password has been set.
func (c Config) HasPassword() bool { return c.PasswordHash != "" }

// CanExposePublicly refuses to turn the Funnel on without authentication.
//
// It is the most important check in the application: the Funnel URL is public
// on the internet, and without a password anyone who guesses it would see and
// hear a child's room.
func (c Config) CanExposePublicly() error {
	if !c.HasPassword() {
		return ErrNoPassword
	}
	return nil
}

// The two errors of this package that **end up on a screen**, which is why they
// are sentinels rather than sentences.
//
// The message they carry is English like every other one in the program, and it
// is what is read in the log; the words the user sees are chosen by whoever
// shows them, after recognising which of the two it is with errors.Is.
var (
	// ErrNoPassword: exposing the monitor was asked for without a password set.
	ErrNoPassword = errors.New("cannot expose publicly without a password set")
	// ErrPasswordTooShort: the chosen password is shorter than MinPasswordLen.
	ErrPasswordTooShort = fmt.Errorf("the password must be at least %d characters", MinPasswordLen)
)

// ---------- password ----------

// argon2id parameters. Chosen to cost about 100 ms on modern desktop hardware:
// enough to make a dictionary attack impractical, not so much as to slow a
// legitimate login down.
const (
	argonTime    = 3
	argonMemory  = 64 * 1024 // 64 MiB
	argonThreads = 4
	argonKeyLen  = 32
	argonSaltLen = 16

	// MinPasswordLen is deliberately low but not zero: this is a password typed
	// on a phone in the dark.
	MinPasswordLen = 8
)

// HashPassword produces an argon2id hash in PHC format.
func HashPassword(password string) (string, error) {
	if len([]rune(password)) < MinPasswordLen {
		return "", ErrPasswordTooShort
	}
	salt := make([]byte, argonSaltLen)
	if _, err := rand.Read(salt); err != nil {
		return "", fmt.Errorf("salt generation: %w", err)
	}
	key := argon2.IDKey([]byte(password), salt, argonTime, argonMemory, argonThreads, argonKeyLen)

	return fmt.Sprintf("$argon2id$v=%d$m=%d,t=%d,p=%d$%s$%s",
		argon2.Version, argonMemory, argonTime, argonThreads,
		base64.RawStdEncoding.EncodeToString(salt),
		base64.RawStdEncoding.EncodeToString(key)), nil
}

// VerifyPassword compares a password against a hash in PHC format.
//
// The comparison is constant time: an ordinary one would leak how many leading
// bytes were right.
func VerifyPassword(hash, password string) bool {
	parts := strings.Split(hash, "$")
	// ["", "argon2id", "v=19", "m=...,t=...,p=...", salt, key]
	if len(parts) != 6 || parts[1] != "argon2id" {
		return false
	}

	var version int
	if _, err := fmt.Sscanf(parts[2], "v=%d", &version); err != nil || version != argon2.Version {
		return false
	}
	var memory uint32
	var time uint32
	var threads uint8
	if _, err := fmt.Sscanf(parts[3], "m=%d,t=%d,p=%d", &memory, &time, &threads); err != nil {
		return false
	}

	salt, err := base64.RawStdEncoding.DecodeString(parts[4])
	if err != nil {
		return false
	}
	want, err := base64.RawStdEncoding.DecodeString(parts[5])
	if err != nil || len(want) == 0 {
		return false
	}

	got := argon2.IDKey([]byte(password), salt, time, memory, threads, uint32(len(want)))
	return subtle.ConstantTimeCompare(got, want) == 1
}
