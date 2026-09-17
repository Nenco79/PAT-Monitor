package config

import (
	"strings"
	"testing"
)

// The name is one per machine but **stable on this one**: were it to change at
// every start, every restart would register a new node in the tailnet and the
// public address would not stand still for a minute.
func TestTheDefaultNameIsStable(t *testing.T) {
	a, b := DefaultFunnelHostname(), DefaultFunnelHostname()
	if a != b {
		t.Fatalf("two calls give %q and %q", a, b)
	}
	if !strings.HasPrefix(a, "patmon") {
		t.Errorf("the name %q does not start with patmon", a)
	}
	// It has to stay a valid DNS label: lower case, digits and hyphens.
	for _, r := range a {
		if (r < 'a' || r > 'z') && (r < '0' || r > '9') && r != '-' {
			t.Errorf("character %q is not allowed in a DNS label: %q", r, a)
		}
	}
	if len(a) > 63 {
		t.Errorf("label too long (%d): %q", len(a), a)
	}
}

// And it must not carry the name of the computer: that label ends up in a
// public DNS name, and "desktop-firstname" would become readable by anyone.
func TestTheNameDoesNotExposeTheMachine(t *testing.T) {
	name := DefaultFunnelHostname()
	if name == "patmon" {
		t.Skip("no machine name available: it fell back to the bare name")
	}
	suffix := strings.TrimPrefix(name, "patmon-")
	if len(suffix) != 6 {
		t.Fatalf("the suffix %q is not a fingerprint of six hexadecimal digits", suffix)
	}
	for _, r := range suffix {
		if (r < 'a' || r > 'f') && (r < '0' || r > '9') {
			t.Errorf("the suffix %q is not hexadecimal: it looks like a name, not a fingerprint", suffix)
		}
	}
}

// Default has to use it, otherwise it gets fixed here and stays put over there.
func TestDefaultUsesTheDistinctName(t *testing.T) {
	if got := Default().FunnelHostname; got != DefaultFunnelHostname() {
		t.Errorf("Default() says %q instead of %q", got, DefaultFunnelHostname())
	}
}
