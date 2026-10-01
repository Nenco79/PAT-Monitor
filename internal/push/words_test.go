package push

import (
	"path/filepath"
	"strings"
	"testing"

	"patmonitor/internal/alerts"
)

// **Every word a notification can carry is in every catalogue.** The page's
// guard does not see these: the notification is composed here, for a phone
// with no page open, and a key missing from one language would reach that
// phone as the bare key — `viewer.alert.cry` on a lock screen at three in the
// morning. The languages are read from the folder and the codes from the alerts
// package, so a language or a code added tomorrow is covered with nothing to
// remember.
func TestEveryWordANotificationCarriesIsInEveryCatalogue(t *testing.T) {
	files, err := filepath.Glob(filepath.Join("..", "i18n", "catalogs", "*.json"))
	if err != nil || len(files) < 2 {
		t.Fatalf("%d catalogues found: this guard is looking at nothing", len(files))
	}
	keys := []string{"push.test.body"}
	for _, c := range alerts.AllCodes() {
		keys = append(keys, "viewer.alert."+string(c))
		if alerts.LevelOf(c) != alerts.Event {
			keys = append(keys, "viewer.recovered."+string(c))
		}
	}
	for _, f := range files {
		lang := strings.TrimSuffix(filepath.Base(f), ".json")
		cat := readCatalogue(t, lang)
		for _, k := range keys {
			if strings.TrimSpace(cat[k]) == "" {
				t.Errorf("%s: %q is missing, and a notification would carry the key", lang, k)
			}
		}
	}
}
