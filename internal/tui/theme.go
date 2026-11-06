package tui

import (
	"image/color"
	"os"
	"path/filepath"
	"strings"

	"charm.land/lipgloss/v2"
)

// theme is a palette. Every theme uses one accent, so color always marks the
// current thing rather than decorating the page.
type theme struct {
	name                    string
	bg, fg, soft, dim, line string
	// panel is the background of overlays, a step above the page.
	panel                 string
	accent, highlight     string
	paperDark, paperLight [3]float64
}

var themes = []theme{
	{
		name: "Warm Night", bg: "#141210", fg: "#e8e2d6", soft: "#c9c2b4", dim: "#7d7669",
		line: "#35302a", accent: "#d9744f", highlight: "#211d19", panel: "#1b1815",
		paperDark: [3]float64{58, 52, 46}, paperLight: [3]float64{232, 222, 200},
	},
	{
		name: "Paper", bg: "#f4efe4", fg: "#332e27", soft: "#4f483d", dim: "#8d8574",
		line: "#d8cfbd", accent: "#b4532f", highlight: "#e9e1d1", panel: "#efe9dc",
		paperDark: [3]float64{120, 110, 96}, paperLight: [3]float64{255, 251, 240},
	},
	{
		name: "Moss", bg: "#121714", fg: "#dde5da", soft: "#bcc7b9", dim: "#75857a",
		line: "#2c382f", accent: "#8fbf8a", highlight: "#1b241e", panel: "#161d19",
		paperDark: [3]float64{46, 58, 49}, paperLight: [3]float64{214, 226, 208},
	},
}

func themeIndex(name string) int {
	for i, t := range themes {
		if strings.EqualFold(t.name, strings.TrimSpace(name)) {
			return i
		}
	}
	return 0
}

func loadTheme(dataDir string) int {
	return themeIndex(loadSetting(dataDir, "theme"))
}

func saveTheme(dataDir string, index int) error {
	return saveSetting(dataDir, "theme", themes[index].name)
}

// Settings are small files in the data directory, one per setting, so each
// is easy to read and reset by hand.
func loadSetting(dataDir, name string) string {
	data, err := os.ReadFile(filepath.Join(dataDir, name))
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(data))
}

func saveSetting(dataDir, name, value string) error {
	if err := os.MkdirAll(dataDir, 0o700); err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(dataDir, name), []byte(value+"\n"), 0o600)
}

// styles are the theme's spans. Each sets the background, because a span with
// none would show the terminal's own color through the page.
type styles struct {
	base, soft, dim, accent, bold, line   lipgloss.Style
	selected, selectedDim, selectedAccent lipgloss.Style
	key, button, buttonOn                 lipgloss.Style
	panelText, panelDim, panelAccent      lipgloss.Style
	menuSel, menuSelDim, menuSelAccent    lipgloss.Style
	background                            color.Color
}

func newStyles(t theme) styles {
	bg := lipgloss.Color(t.bg)
	hi := lipgloss.Color(t.highlight)
	plain := func(fg string) lipgloss.Style {
		return lipgloss.NewStyle().Background(bg).Foreground(lipgloss.Color(fg))
	}
	on := func(bg, fg string) lipgloss.Style {
		return lipgloss.NewStyle().Background(lipgloss.Color(bg)).Foreground(lipgloss.Color(fg))
	}
	onHighlight := func(fg string) lipgloss.Style {
		return lipgloss.NewStyle().Background(hi).Foreground(lipgloss.Color(fg))
	}
	return styles{
		background:     bg,
		base:           plain(t.fg),
		soft:           plain(t.soft),
		dim:            plain(t.dim),
		accent:         plain(t.accent),
		bold:           plain(t.fg).Bold(true),
		line:           plain(t.line),
		selected:       onHighlight(t.fg),
		selectedDim:    onHighlight(t.dim),
		selectedAccent: onHighlight(t.accent),
		key:            plain(t.fg),
		panelText:      on(t.panel, t.fg),
		panelDim:       on(t.panel, t.dim),
		panelAccent:    on(t.panel, t.accent),
		menuSel:        on(t.line, t.fg),
		menuSelDim:     on(t.line, t.dim),
		menuSelAccent:  on(t.line, t.accent),
		button:         onHighlight(t.fg),
		buttonOn:       onHighlight(t.accent),
	}
}
