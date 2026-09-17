//go:build !windows

package applog

import "os"

// Elsewhere the log does not take the standard error over.
//
// The monitor runs on Windows, and the reason for the redirection is Windows's:
// a process built for the graphical subsystem has no standard error to write
// its last words to. This file exists so that the package still builds where
// the tests of everything around it can run.

func standardErrorIsMissing() bool { return false }

func pointStandardErrorAt(*os.File) error { return nil }

func releaseStandardError() error { return nil }
