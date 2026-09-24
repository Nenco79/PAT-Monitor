package config

import (
	"os"
	"reflect"
	"strings"
	"testing"
)

// The README carries the table of keys, that is, **a second list of the same
// thing**, and on this project those always diverge. The question is therefore
// asked of the struct, which is the original, and not of a list written in the
// test — that would be the third list, and it diverges like the second. The
// default and the meaning stay out of reach: those are re-read by hand, and no
// test can say whether a sentence is still true.
const readmePath = "../../README.md"

// notKnobs are the values the file holds that the table must not list, for one
// of two reasons: the guided configuration writes them rather than a person, or
// they have been superseded and are read only so an older file keeps working.
// Either way they are named **in the prose** below the table, which is where
// this test looks for them.
var notKnobs = map[string]bool{
	"password_hash":   true,
	"onboarding_done": true,
	"camera_name":     true,
}

func yamlKeys() []string {
	var out []string
	t := reflect.TypeFor[Config]()
	for field := range t.Fields() {
		tag := field.Tag.Get("yaml")
		tag, _, _ = strings.Cut(tag, ",")
		if tag == "" || tag == "-" {
			continue
		}
		out = append(out, tag)
	}
	return out
}

// tableKeys pulls the keys out of the **first column** of the table's rows.
//
// The first only: the other two carry text between backticks as well — a
// default, a `0` that switches something off — and taking them all would turn
// this test into a generator of false alarms.
func tableKeys(readme string) []string {
	_, after, ok := strings.Cut(readme, "## Configuration")
	if !ok {
		return nil
	}
	body, _, _ := strings.Cut(after, "Command line:")

	var out []string
	for line := range strings.SplitSeq(body, "\n") {
		if !strings.HasPrefix(line, "| `") {
			continue
		}
		first, _, _ := strings.Cut(strings.TrimPrefix(line, "|"), "|")
		parts := strings.Split(first, "`")
		// The odd indices are what sits between two backticks.
		for i := 1; i < len(parts); i += 2 {
			out = append(out, parts[i])
		}
	}
	return out
}

func readREADME(t *testing.T) string {
	t.Helper()
	b, err := os.ReadFile(readmePath)
	if err != nil {
		t.Fatalf("the README cannot be read: %v", err)
	}
	return string(b)
}

// The direction that gets forgotten: a new key in the struct and nobody to say
// so. Whoever adds one tests it on their own machine by writing it by hand, and
// never notices that no one else can know it exists.
func TestEveryConfigKeyIsDocumented(t *testing.T) {
	readme := readREADME(t)
	inTable := make(map[string]bool)
	for _, k := range tableKeys(readme) {
		inTable[k] = true
	}

	for _, k := range yamlKeys() {
		if notKnobs[k] {
			// Not in the table, but named: were it to vanish from the prose,
			// the file would hold a line the README does not explain.
			if !strings.Contains(readme, "`"+k+"`") {
				t.Errorf("%s is not a knob, but the README does not name it at all", k)
			}
			continue
		}
		if !inTable[k] {
			t.Errorf("the key %s is not in the README table", k)
		}
	}
}

// And the opposite direction: a key taken out of the program and left written
// down. It is not dangerous, it is **false** — whoever writes it in their own
// config.yaml gets nothing and goes looking for the fault where it is not.
func TestTheTableInventsNoKey(t *testing.T) {
	declared := make(map[string]bool)
	for _, k := range yamlKeys() {
		declared[k] = true
	}

	listed := tableKeys(readREADME(t))
	if len(listed) == 0 {
		t.Fatal("no key read from the table: this test is looking in the wrong place")
	}
	for _, k := range listed {
		if !declared[k] {
			t.Errorf("the README documents %s, which does not exist in the configuration", k)
		}
	}
}
