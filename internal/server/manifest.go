package server

import (
	"bytes"
	"encoding/json"
	"fmt"
	"image"
	"image/color"
	"image/draw"
	"image/png"
	"net/http"
	"strconv"
	"sync"

	"patmonitor/internal/icon"
	"patmonitor/internal/version"
)

// The web app manifest and the icons it points at.
//
// **It exists for one thing no line of a page can do: take the browser's own
// bars away on an iPhone.** The Fullscreen API is not there before iOS 17.2 and
// Brave does not expose it at all, so on those phones the mode fills the page
// one can see and the address bar stays. Saved to the Home Screen with this
// manifest the monitor opens with no chrome whatever, in either orientation,
// and it does not depend on an API answering yes. It is also where the guided
// path already sends people — "the bookmark one will not have to look for
// again".
//
// **The icons are drawn and not embedded**, which is the tray's decision
// applied to the web: `internal/icon` computes the pixels in pure Go, so there
// is nothing generated to commit, nothing for a build step to remember, and no
// second drawing that can drift from the one in the executable. It costs a PNG
// encode the first time each size is asked for.

// iconSizes are the sizes the pages ask for, and the list is the authority: a
// route is registered per entry, so no request can ask for an arbitrary size
// and make us draw it.
//
//   - 32 is the favicon. Without it the browser asks for /favicon.ico, which
//     has no route of its own and so falls onto `GET /` — that is, it answers a
//     request for an image with the viewer's HTML.
//   - 180 is what iOS uses for the Home Screen. Without one it saves a
//     screenshot of the page, which for a baby monitor is a picture of a room.
//   - 192 and 512 are the manifest's two, which is what Chrome wants for the
//     launcher and the splash.
var iconSizes = []int{faviconSize, 180, 192, 512}

// faviconSize is the one size that keeps its transparency, and it is named
// because a rule below asks about it: in a browser tab the icon sits on
// whatever colour the tab is, so an opaque field there would be a small dark
// rectangle among the other tabs.
const faviconSize = 32

// ground is the night palette's page colour, copied out of the stylesheet
// because a manifest is JSON and cannot read CSS.
//
// **It is one value and the hexadecimal is derived from it**, rather than the
// two being written side by side: three things now need the same answer — the
// manifest's two colours and the field the Home Screen icons are composited
// onto — and three spellings of one colour is how two of them drift.
var ground = color.NRGBA{R: 0x17, G: 0x15, B: 0x12, A: 0xff}

func groundHex() string { return fmt.Sprintf("#%02x%02x%02x", ground.R, ground.G, ground.B) }

// onGround puts the drawing on an opaque field of that colour.
//
// **iOS ignores the alpha channel and composites the Home Screen icon onto
// black**, which Apple states outright, so a transparent field does not stay
// the page's warm near-black: it becomes #000000, and the drawing appears to
// float with no tile under it. Measured on what we serve: the 180 is 27%
// transparent, all four corners included.
//
// Android's maskable icons want the same thing for their own reason — a
// launcher fills what the drawing does not, with a colour it chooses.
func onGround(src *image.NRGBA) *image.NRGBA {
	dst := image.NewNRGBA(src.Bounds())
	draw.Draw(dst, dst.Bounds(), &image.Uniform{ground}, image.Point{}, draw.Src)
	draw.Draw(dst, dst.Bounds(), src, src.Bounds().Min, draw.Over)
	return dst
}

// iconPath is the address of one size. Written once because the routes and the
// manifest both need it, and a second spelling is a 404 nobody notices — see
// "An asset with no route does not give a 404".
func iconPath(size int) string { return "/icon-" + strconv.Itoa(size) + ".png" }

var (
	iconOnce  sync.Mutex
	iconCache = map[int][]byte{}
)

// iconPNG draws one size and keeps it. The drawing is deterministic, so the
// cache is a saving and not a decision.
func iconPNG(size int) ([]byte, error) {
	iconOnce.Lock()
	defer iconOnce.Unlock()
	if b, ok := iconCache[size]; ok {
		return b, nil
	}
	img := icon.Draw(size)
	if size != faviconSize {
		img = onGround(img)
	}
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		return nil, fmt.Errorf("drawing the %dpx icon: %w", size, err)
	}
	iconCache[size] = buf.Bytes()
	return buf.Bytes(), nil
}

func (s *Server) serveIcon(size int) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		b, err := iconPNG(size)
		if err != nil {
			s.log.Warn("icon not drawn", "size", size, "error", err)
			http.Error(w, "icon", http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "image/png")
		// A day, and it is not tuning: the drawing changes with the binary, so
		// what invalidates it is an upgrade, which is a new process anyway.
		w.Header().Set("Cache-Control", "public, max-age=86400")
		_, _ = w.Write(b)
	}
}

// apiManifest composes the manifest rather than serving a file.
//
// **The name is taken and not repeated.** It is `version.Product`, the same
// string the tray, the window title and the data folder use: a second copy of
// "PAT Monitor" in an asset would part company at the first rename, and this is
// the file that names the thing on somebody's Home Screen for years.
func (s *Server) apiManifest(w http.ResponseWriter, r *http.Request) {
	type manifestIcon struct {
		Src     string `json:"src"`
		Sizes   string `json:"sizes"`
		Type    string `json:"type"`
		Purpose string `json:"purpose,omitempty"`
	}
	icons := make([]manifestIcon, 0, 2)
	for _, n := range []int{192, 512} {
		icons = append(icons, manifestIcon{
			Src:   iconPath(n),
			Sizes: fmt.Sprintf("%dx%d", n, n),
			Type:  "image/png",
			// `any maskable` so a launcher that crops to a circle does not eat
			// the drawing: it is a dog's head in a round field already.
			Purpose: "any maskable",
		})
	}
	m := map[string]any{
		"name":       version.Product,
		"short_name": version.Product,
		// **`fullscreen` and not `standalone`.** Standalone still keeps the
		// status bar, and the whole reason this file exists is a mode that wants
		// the screen; where fullscreen is not honoured the browser falls back to
		// standalone by itself, which is the next best thing.
		"display":          "fullscreen",
		"display_override": []string{"fullscreen", "standalone"},
		// The address the icon opens. It is the viewer and not the page that was
		// open when somebody saved it — saving from the recordings would
		// otherwise give a Home Screen icon that opens a list of files.
		"start_url": "/",
		"scope":     "/",
		// The two grounds of the night palette, from the one value above: a
		// manifest is JSON and cannot read a stylesheet, so that copy is
		// unavoidable, but it is made once and the Home Screen icons are
		// composited onto the same thing.
		"background_color": groundHex(),
		"theme_color":      groundHex(),
		"orientation":      "any",
		"icons":            icons,
	}
	w.Header().Set("Content-Type", "application/manifest+json; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	if err := json.NewEncoder(w).Encode(m); err != nil {
		s.log.Warn("manifest not written", "error", err)
	}
}
