package push

import (
	"bytes"
	"log/slog"
	"strings"
	"testing"
)

// **Every device that shows a message sends its receipt, and every receipt is
// written.** One message goes to every subscription under one id, so a
// receipt that forgot the id let the second phone's delivery go unmeasured —
// and the receipt is the only instrument there is for whether a notification
// reached a phone at all.
func TestEveryDeviceThatShowsAMessageIsCounted(t *testing.T) {
	store, err := OpenStore(t.TempDir(), randomSource)
	if err != nil {
		t.Fatal(err)
	}
	var log bytes.Buffer
	n := newNotifier(store, nil, slog.New(slog.NewTextHandler(&log, nil)), randomSource)
	n.remember("abc", "cry")
	n.Seen("abc")
	n.Seen("abc")
	if got := strings.Count(log.String(), "notification shown"); got != 2 {
		t.Fatalf("%d receipts written for two devices:\n%s", got, log.String())
	}
	n.Seen("nobody")
	if got := strings.Count(log.String(), "notification shown"); got != 2 {
		t.Errorf("a receipt for a message never sent was written")
	}
}
