package update

import (
	"net/url"
	"path"
	"strings"
	"testing"
)

// FuzzWhatTheReleaseEndpointSays gives verdict answers GitHub never wrote.
//
// **The URL of an Available state goes to ShellExecute**, so the property is
// on that and on nothing else: whatever the tag and the link, an update is
// offered only with an https page that, once a browser has resolved its dot
// segments, is still this repository's releases or a page under them.
func FuzzWhatTheReleaseEndpointSays(f *testing.F) {
	f.Add("v9.9.9", releasePages+"tag/v9.9.9", false, false)
	f.Add("v9.9.9", releasePages+"../../somebody/else", false, false)
	f.Add("v9.9.9", "https://github.com/Nenco79/PAT-Monitor/releases/tag/%2e%2e", false, false)
	f.Add("v9.9.9", `https://github.com/Nenco79/PAT-Monitor/releases/..\x`, false, false)
	f.Add("v9.9.9", "file:///C:/Windows/System32/calc.exe", false, false)
	f.Add("9.9.9.9", releasePages+"tag/v9", true, false)
	f.Add("", "", false, true)

	prefix := strings.TrimPrefix(releasePages, "https://github.com")
	f.Fuzz(func(t *testing.T, tag, link string, pre, draft bool) {
		st := verdict(release{TagName: tag, HTMLURL: link, Prerelease: pre, Draft: draft})
		if st.Code != Available {
			return
		}
		if pre || draft {
			t.Fatalf("a pre-release or draft was offered: %+v", st)
		}
		u, err := url.Parse(st.URL)
		if err != nil || u.Scheme != "https" || u.Host != "github.com" || u.User != nil {
			t.Fatalf("offered with %q, not an https page on github.com", st.URL)
		}
		if clean := path.Clean(u.Path); !strings.HasPrefix(strings.ToLower(clean+"/"), strings.ToLower(prefix)) {
			t.Fatalf("offered with %q, which resolves to %q outside %q", st.URL, clean, prefix)
		}
		if strings.HasPrefix(st.Version, "v") {
			t.Fatalf("version %q keeps the tag's v", st.Version)
		}
	})
}
