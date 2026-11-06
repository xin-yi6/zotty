package zotdb

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

var (
	useDataDirPref = regexp.MustCompile(`user_pref\("extensions\.zotero\.useDataDir",\s*true\)`)
	dataDirPref    = regexp.MustCompile(`user_pref\("extensions\.zotero\.dataDir",\s*"((?:[^"\\]|\\.)*)"\)`)
)

// DefaultDir finds Zotero's data directory: the custom one chosen in Zotero's
// settings if there is one, otherwise ~/Zotero.
func DefaultDir(home string) string {
	fallback := filepath.Join(home, "Zotero")
	profiles := filepath.Join(home, "Library", "Application Support", "Zotero", "Profiles")
	prefs, _ := filepath.Glob(filepath.Join(profiles, "*", "prefs.js"))
	for _, path := range prefs {
		data, err := os.ReadFile(path)
		if err != nil || !useDataDirPref.Match(data) {
			continue
		}
		m := dataDirPref.FindSubmatch(data)
		if m == nil {
			continue
		}
		dir := strings.NewReplacer(`\\`, `\`, `\"`, `"`).Replace(string(m[1]))
		if _, err := os.Stat(filepath.Join(dir, "zotero.sqlite")); err == nil {
			return dir
		}
	}
	return fallback
}
