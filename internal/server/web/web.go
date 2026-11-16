// Package web is the pages: the markup, the sheets and the scripts the server
// hands to a browser, and the guards that read those files and nothing else.
//
// It is a package and not a folder of the server's so that the pages and the
// tests that watch them are one place: a place a slice of the record can name
// without naming the server's Go, and a place the word budget in internal/doc
// can see.
package web

import (
	"embed"
	"strings"
)

// Assets is every page, sheet and script, at the root: "index.html", not
// "web/index.html".
//
// **The patterns are a list written by hand**, because `*` would take this file
// and the tests beside it and the server would hand them out. A file of a kind
// that is not named here is not embedded and nothing says so, which is why
// TestEveryFileHereIsEmbedded reads the folder and asks.
//
//go:embed *.html *.css *.js
var Assets embed.FS

// WithoutComments removes `//` and the slash-star sections from a script.
//
// **It serves the tests, of this package and of the server's**, and is here
// rather than in a test file because two packages cannot share one of those:
// written twice it would be two strippers, and the guards built on them would
// stop agreeing about what a comment is. It is needed because this project's
// comments are long and quote freely the sentences they talk about: without
// removing them a guard would accuse the explanations instead of the code.
func WithoutComments(src string) string {
	var b strings.Builder
	for i := 0; i < len(src); {
		switch {
		case strings.HasPrefix(src[i:], "//"):
			for i < len(src) && src[i] != '\n' {
				i++
			}
		case strings.HasPrefix(src[i:], "/*"):
			end := strings.Index(src[i+2:], "*/")
			if end < 0 {
				return b.String()
			}
			i += end + 4
		default:
			b.WriteByte(src[i])
			i++
		}
	}
	return b.String()
}
