package server

import (
	"archive/zip"
	"bytes"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

// zipURL composes the archive's address for the given clips.
func zipURL(names ...string) string {
	q := url.Values{}
	for _, n := range names {
		q.Add("name", n)
	}
	return "/api/clips.zip?" + q.Encode()
}

// **A selection downloads as one archive, each clip whole and under its own
// name.** It is the one road that works on an iPhone, where a page starting
// several downloads delivers the first.
func TestSeveralClipsDownloadAsOneArchive(t *testing.T) {
	first := []byte("the first clip's bytes")
	s, token, store, name := serverWithClip(t, first)
	const other = "keep-20260902-213000-bark.mp4"
	second := []byte("the second clip, which is kept")
	if err := os.WriteFile(filepath.Join(store.Dir(), other), second, 0o600); err != nil {
		t.Fatal(err)
	}

	// A name asked twice is one entry, not two.
	w := request(t, s, token, http.MethodGet, zipURL(name, other, name), "")
	if w.Code != http.StatusOK {
		t.Fatalf("the archive answered %d", w.Code)
	}
	if got := w.Header().Get("Content-Type"); got != "application/zip" {
		t.Errorf("Content-Type %q", got)
	}
	zr, err := zip.NewReader(bytes.NewReader(w.Body.Bytes()), int64(w.Body.Len()))
	if err != nil {
		t.Fatalf("not an archive: %v", err)
	}
	want := map[string][]byte{name: first, other: second}
	if len(zr.File) != len(want) {
		t.Fatalf("%d entries, want %d", len(zr.File), len(want))
	}
	for _, f := range zr.File {
		rc, err := f.Open()
		if err != nil {
			t.Fatal(err)
		}
		got, _ := io.ReadAll(rc)
		rc.Close()
		if !bytes.Equal(got, want[f.Name]) {
			t.Errorf("%s holds %q, want %q", f.Name, got, want[f.Name])
		}
		if f.Method != zip.Store {
			t.Errorf("%s is compressed: a clip is H.264 already", f.Name)
		}
	}
}

// **A clip that is not there refuses the whole archive, before a byte is
// sent.** Once an archive has begun the status is 200 for good, so a name the
// cleanup removed since the page listed it would have come out as a truncated
// file that says it is fine. Put back — each clip opened as it is written —
// this answers 200.
func TestAnArchiveWithAMissingClipIsRefusedBeforeItBegins(t *testing.T) {
	s, token, _, name := serverWithClip(t, []byte("x"))

	w := request(t, s, token, http.MethodGet, zipURL(name, "clip-20260101-000000-motion.mp4"), "")
	if w.Code != http.StatusNotFound {
		t.Fatalf("an archive with a missing clip answered %d, want 404", w.Code)
	}
	if w.Header().Get("Content-Type") == "application/zip" {
		t.Error("the refusal was sent as an archive")
	}
}

// An archive of nothing, or of more than the ceiling, is a malformed request.
func TestAnArchiveWantsBetweenOneAndTheCeiling(t *testing.T) {
	s, token, _, name := serverWithClip(t, []byte("x"))

	if w := request(t, s, token, http.MethodGet, "/api/clips.zip", ""); w.Code != http.StatusBadRequest {
		t.Errorf("an archive of nothing answered %d, want 400", w.Code)
	}
	many := make([]string, maxZipClips+1)
	for i := range many {
		many[i] = name
	}
	if w := request(t, s, token, http.MethodGet, zipURL(many...), ""); w.Code != http.StatusBadRequest {
		t.Errorf("an archive past the ceiling answered %d, want 400", w.Code)
	}
}

// **The page knows the archive's ceiling, and the two copies are one.** The
// server refuses past it, and a refusal met by a download link is shown by
// nobody: the page checks first, with its own copy of the number. There is no
// way to share a constant between Go and a browser, so it is watched here.
func TestThePageKnowsTheArchiveCeiling(t *testing.T) {
	js := readAsset(t, "clips.js")
	want := "const MAX_ZIP = " + strconv.Itoa(maxZipClips) + ";"
	if !strings.Contains(js, want) {
		t.Errorf("clips.js does not carry %q: the page would let through a selection the server refuses", want)
	}
}
