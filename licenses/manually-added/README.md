# Licenses the generator cannot find

`pat-licenses` collects license files from the Go modules linked into the
binary. This directory holds the things we ship anyway that appear in no module, together with the reason why — because an unexplained "misc" folder is
the first thing a tidy-up deletes.

## `opus/COPYING` — libopus, Xiph.Org and others, BSD-3-Clause

**We ship libopus inside the executable, and nothing upstream carries its
license along.** `internal/opuswasm/libopus.wasm` is placed in the binary by a
`go:embed` directive, and that file is **libopus compiled to WebAssembly**. It
reached us through `github.com/jj11hh/opus`, whose own MIT license covers the
Go wrapper and the C bridge — see `opus-wasm-bridge/` — but not the codec: that
package's README points at opus-codec.org and attaches nothing.

libopus is BSD-3-Clause, and its second clause requires the copyright notice and
the license text to accompany redistributions **in binary form**. Ours is
exactly that. The file here is the official `COPYING` from the Opus project
repository.

This is not a technicality: it is the only non-Go code we ship, and it is also
the easiest to miss, because it does not appear in `go.mod` as a dependency of
its own. If libopus ever arrives by another route — a CGo binding, a different
WASM build — this file stays valid and must be kept.

## `go/LICENSE` and `go/PATENTS` — the Go standard library, BSD-3-Clause

The binary is statically linked: it contains the Go runtime and the parts of the
standard library we use. That is not a module, so `go list` does not report it
and the generator cannot see it, but it travels with us like everything else.
`PATENTS` is the patent grant Google attaches to Go: it goes *with* the license,
not instead of it.

Both files are copied from `GOROOT`, that is, from the same installation the
binary is built with.

## `tabler-icons/LICENSE` — Tabler Icons, Paweł Kuna, MIT

The web pages carry glyphs — `microphone`, `mood-cry`, `dog`, `walk` and the
handful the clip list uses — copied verbatim as inline SVG paths from Tabler
Icons v3.46.0. They are not a Go module and not a build-time dependency: they
are third-party artwork pasted into `internal/server/web/`, which `go:embed`
then places inside the binary. The generator has no way to see them.

**Two glyphs also ship as Go source**, `video` and `file-text` in
`internal/tray/glyph_windows.go`: the tray panel has no browser to draw an SVG
for it, so the `d` attributes are pasted into a Go string and engraved by our
own rasteriser. Same artwork, same set, same licence — only the file extension
differs, and an attribution that stops at `.html` would stop matching what
ships.

MIT requires the copyright notice and the permission text to travel with
"copies or substantial portions of the Software". A dozen icons out of several
thousand is arguably not substantial, and shipping the license anyway costs one
file — the asymmetry decides it.

If another glyph is ever taken from the same set, nothing here changes. If one
is taken from a *different* set, that set needs its own directory: two licenses
merged into one folder is how attribution quietly stops matching what ships.

## `opus-wasm-bridge/LICENSE` and `AUTHORS` — the WebAssembly bridge, Jiang Yiheng and the Go Opus Authors, MIT

`libopus.wasm`, embedded by `internal/opuswasm`, is not libopus alone. It is
libopus **plus** about two hundred lines of C from
`github.com/jj11hh/opus@v1.0.1`: non-variadic wrappers around
`opus_encoder_ctl`, which exist because a variadic C function cannot be called
from outside a WebAssembly module. Both halves ship inside our binary, so both
notices travel with it — BSD-3-Clause above for libopus, MIT here for the
bridge.

**It moved here because the generator was about to delete it, and the test was
going to agree.** That code used to arrive as a Go module, so `go list`
reported it and `licenses/github.com/jj11hh/opus/` was generated like any other
entry. We now write the glue ourselves and the module is gone from `go.mod` —
but the compiled artifact still ships. The generator enumerates *modules*, so
on its next run it would have swept the folder away and left the repository
declaring less than it distributes, with every test green.

The Go wrapper this project once depended on is **not** here and does not need
to be: none of it is in the binary any more. What ships is the artifact, and
this is its notice.

If `libopus.wasm` is ever rebuilt from libopus upstream with wrappers of our
own, this entry goes away with the last byte of theirs — and not one moment
earlier.

## `ced-tiny/LICENSE` and `ced-tiny/NOTICE` — the CED weights, Xiaomi, Apache-2.0

`internal/ced/ced-tiny-q8_0.gguf` is six megabytes of trained parameters placed
in the binary by `go:embed`. It is CED-tiny from `mispeech/ced-tiny`, converted
to GGUF and quantised — the architecture and the numbers are theirs, the
conversion only rewrites how they are stored.

Same shape of omission as `libopus.wasm`: the generator enumerates modules, and
a file embedded from the source tree is not one. It arrived by download, not
through `go.mod`, so `go list` has never heard of it and never will.

Apache-2.0 asks for a copy of the licence and for the attribution notices to
travel with derivative or redistributed works. Ours is a redistribution. That
the licence text is identical to this project's own is a coincidence of choice
and not a reason to leave it out: a reader looking for what covers those six
megabytes must find it next to them, not have to infer that the root `LICENSE`
happens to apply to somebody else's work.

**The fingerprint is in the NOTICE, and it is the line that matters.** The URL
says where the file came from; only the hash says whether what you can download
today is what we are executing.
