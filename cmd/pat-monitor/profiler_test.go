package main

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

// **Listening on loopback keeps other machines out, not other pages.** A site
// that re-points its own name at 127.0.0.1 makes a browser on this PC talk to
// the profiler as the same origin and read what it answers — the stacks of
// every goroutine, a CPU profile of whatever length it asks for. It cannot
// call us by a loopback name, and that is the whole of the check.
//
// **The defect was put back and this test fails with it**: with the profiler's
// mux served bare, the foreign name gets its answer.
func TestTheProfilerAnswersOnlyToALoopbackName(t *testing.T) {
	served := false
	h := loopbackNamesOnly(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { served = true }))

	for host, want := range map[string]bool{
		"localhost:6060":             true,
		"127.0.0.1:6060":             true,
		"[::1]:6060":                 true,
		"not-the-monitor.example:80": false,
		"desktop-pc:6060":            false,
		"192.168.1.20:6060":          false,
	} {
		served = false
		r := httptest.NewRequest(http.MethodGet, "/debug/pprof/goroutine", nil)
		r.Host = host
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		if served != want {
			t.Errorf("Host %q: served=%v, wanted %v (status %d)", host, served, want, w.Code)
		}
	}
}
