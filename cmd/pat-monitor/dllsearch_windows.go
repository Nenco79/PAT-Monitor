//go:build windows

package main

import "patmonitor/internal/wincom"

// narrowDLLSearch is wincom.NarrowDLLSearch, named here so that the guard on
// main's first statement reads one name.
//
// It is called before anything else in main, and a refusal is reported and
// stops nothing: a monitor that will not watch because it could not tighten a
// search path would be the worse of the two.
func narrowDLLSearch() error { return wincom.NarrowDLLSearch() }
