package zotdb

import (
	"os"
	"path/filepath"
	"testing"
)

func TestDefaultDir(t *testing.T) {
	home := t.TempDir()
	if got, want := DefaultDir(home), filepath.Join(home, "Zotero"); got != want {
		t.Errorf("without a profile got %s, want %s", got, want)
	}

	custom := filepath.Join(home, "Papers", "My Zotero")
	os.MkdirAll(custom, 0o755)
	os.WriteFile(filepath.Join(custom, "zotero.sqlite"), nil, 0o644)
	profile := filepath.Join(home, "Library", "Application Support", "Zotero", "Profiles", "abc.default")
	os.MkdirAll(profile, 0o755)
	prefs := `user_pref("extensions.zotero.dataDir", "` + custom + `");` + "\n" +
		`user_pref("extensions.zotero.useDataDir", true);` + "\n"
	os.WriteFile(filepath.Join(profile, "prefs.js"), []byte(prefs), 0o644)
	if got := DefaultDir(home); got != custom {
		t.Errorf("with a custom data directory got %s, want %s", got, custom)
	}

	// A data directory chosen once and then switched off isn't used.
	os.WriteFile(filepath.Join(profile, "prefs.js"), []byte(`user_pref("extensions.zotero.dataDir", "`+custom+`");`), 0o644)
	if got, want := DefaultDir(home), filepath.Join(home, "Zotero"); got != want {
		t.Errorf("with the custom directory off got %s, want %s", got, want)
	}
}
