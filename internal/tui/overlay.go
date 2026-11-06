package tui

import (
	"strconv"
	"strings"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"
)

// Overlays sit over the page: menus of actions, a one-line prompt, and a
// scrolling reader for notes. The page behind dims, and the overlay fades and
// rises into place.

type overlayKind int

const (
	overlayNone overlayKind = iota
	overlayMenu
	overlayPrompt
	overlayReader
)

type menuItem struct {
	label string
	key   string
	// detail is dim text after the label, such as a collection's path.
	detail string
	action func(Model) (Model, tea.Cmd)
}

type overlay struct {
	kind  overlayKind
	title string

	items  []menuItem
	filter string
	cursor int
	offset int

	value    string
	hint     string
	onSubmit func(Model, string) (Model, tea.Cmd)

	lines []string
}

const (
	menuWidth    = 64
	menuRows     = 14
	readerMargin = 6
)

func (m Model) openOverlay(o overlay) (Model, tea.Cmd) {
	m.over = o
	m.searching = false
	m.overAt = m.clock()
	return m, m.animate(overlayLength)
}

func (m Model) closeOverlay() (Model, tea.Cmd) {
	m.over = overlay{}
	return m, nil
}

func (m Model) menuMatches() []menuItem {
	if m.over.filter == "" {
		return m.over.items
	}
	terms := strings.Fields(strings.ToLower(m.over.filter))
	var out []menuItem
	for _, it := range m.over.items {
		text := strings.ToLower(it.label + " " + it.detail)
		all := true
		for _, t := range terms {
			all = all && strings.Contains(text, t)
		}
		if all {
			out = append(out, it)
		}
	}
	return out
}

func (m *Model) clampMenu() {
	n := len(m.menuMatches())
	m.over.cursor = max(min(m.over.cursor, n-1), 0)
	if m.over.cursor < m.over.offset {
		m.over.offset = m.over.cursor
	}
	if m.over.cursor >= m.over.offset+menuRows {
		m.over.offset = m.over.cursor - menuRows + 1
	}
	m.over.offset = max(min(m.over.offset, n-menuRows), 0)
}

func (m Model) handleOverlayKey(msg tea.KeyPressMsg) (Model, tea.Cmd) {
	key := msg.String()
	if key == "esc" {
		return m.closeOverlay()
	}
	switch m.over.kind {
	case overlayMenu:
		switch key {
		case "up":
			m.over.cursor--
		case "down", "tab":
			m.over.cursor++
		case "pgup":
			m.over.cursor -= menuRows
		case "pgdown":
			m.over.cursor += menuRows
		case "enter":
			matches := m.menuMatches()
			if len(matches) == 0 {
				return m, nil
			}
			action := matches[m.over.cursor].action
			next, _ := m.closeOverlay()
			return action(next)
		case "backspace":
			if r := []rune(m.over.filter); len(r) > 0 {
				m.over.filter = string(r[:len(r)-1])
			}
			m.over.cursor = 0
		default:
			// A menu's own keys act at once; other text narrows the list.
			if m.over.filter == "" {
				for _, it := range m.menuMatches() {
					if it.key != "" && it.key == key {
						next, _ := m.closeOverlay()
						return it.action(next)
					}
				}
			}
			if msg.Text != "" {
				m.over.filter += msg.Text
				m.over.cursor = 0
			}
		}
		m.clampMenu()
	case overlayPrompt:
		switch key {
		case "enter":
			value, submit := strings.TrimSpace(m.over.value), m.over.onSubmit
			next, _ := m.closeOverlay()
			return submit(next, value)
		case "backspace":
			if r := []rune(m.over.value); len(r) > 0 {
				m.over.value = string(r[:len(r)-1])
			}
		case "ctrl+u":
			m.over.value = ""
		default:
			m.over.value += msg.Text
		}
	case overlayReader:
		page := m.readerHeight() - 2
		switch key {
		case "down":
			m.over.offset++
		case "up":
			m.over.offset--
		case "space", " ", "pgdown":
			m.over.offset += page
		case "pgup":
			m.over.offset -= page
		case "home":
			m.over.offset = 0
		case "end":
			m.over.offset = len(m.over.lines)
		}
		m.over.offset = max(min(m.over.offset, len(m.over.lines)-page), 0)
	}
	return m, nil
}

func (m Model) handleOverlayPaste(text string) Model {
	text = strings.Join(strings.Fields(text), " ")
	switch m.over.kind {
	case overlayPrompt:
		m.over.value += text
	case overlayMenu:
		m.over.filter += text
		m.over.cursor = 0
		m.clampMenu()
	}
	return m
}

// readerHeight fits the reader to its text, up to most of the screen.
func (m Model) readerHeight() int {
	return min(max(m.height-readerMargin, 6), len(m.over.lines)+4)
}

// overlayBox draws the overlay's frame and contents and records its clicks.
// x and y are where it will sit on screen.
func (m Model) overlayBox(x, y int) []string {
	s := m.style
	width := m.overlayWidth()
	inner := width - 4
	frame := func(content string) string {
		return s.line.Render("│ ") + m.padPanel(content, inner) + s.line.Render(" │")
	}
	title := " " + m.over.title + " "
	lines := []string{s.line.Render("╭─") + s.accent.Render(title) + s.line.Render(strings.Repeat("─", max(width-3-lipgloss.Width(title), 0))+"╮")}

	switch m.over.kind {
	case overlayMenu:
		filter := s.dim.Render("Type to filter")
		if m.over.filter != "" {
			filter = s.panelText.Render(m.over.filter) + s.accent.Render("▏")
		}
		lines = append(lines, frame(filter), frame(s.line.Render(strings.Repeat("─", inner))))
		matches := m.menuMatches()
		if len(matches) == 0 {
			lines = append(lines, frame(s.dim.Render("Nothing matches")))
		}
		end := min(m.over.offset+menuRows, len(matches))
		for i := m.over.offset; i < end; i++ {
			it := matches[i]
			index := i
			m.addRegion(y+len(lines), x, x+width, func(next Model) (Model, tea.Cmd) {
				action := next.menuMatches()[index].action
				closed, _ := next.closeOverlay()
				return action(closed)
			})
			text, dim, key := s.panelText, s.panelDim, s.panelAccent
			if i == m.over.cursor {
				text, dim, key = s.menuSel, s.menuSelDim, s.menuSelAccent
			}
			keyCell := "   "
			if it.key != "" {
				keyCell = " " + it.key + " "
				keyCell += strings.Repeat(" ", max(3-lipgloss.Width(keyCell), 0))
			}
			label := ansi.Truncate(it.label, inner-lipgloss.Width(keyCell)-1, "…")
			detail := ""
			if room := inner - lipgloss.Width(keyCell) - lipgloss.Width(label) - 3; it.detail != "" && room > 4 {
				detail = "  " + ansi.Truncate(it.detail, room, "…")
			}
			row := key.Render(keyCell) + text.Render(" "+label) + dim.Render(detail)
			pad := text.Render(strings.Repeat(" ", max(inner-lipgloss.Width(row), 0)))
			lines = append(lines, frame(row+pad))
		}
		if len(matches) > menuRows {
			lines = append(lines, frame(s.panelDim.Render(strings.Repeat(" ", max(inner-12, 0))+itoa(m.over.cursor+1)+" / "+itoa(len(matches)))))
		}
		lines = append(lines, frame(s.panelDim.Render("↑ ↓ choose   enter run   esc close")))
	case overlayPrompt:
		lines = append(lines, frame(s.panelText.Render(m.over.value)+s.accent.Render("▏")))
		lines = append(lines, frame(s.line.Render(strings.Repeat("─", inner))))
		for _, line := range strings.Split(ansi.Wrap(m.over.hint, inner, " "), "\n") {
			lines = append(lines, frame(s.panelDim.Render(line)))
		}
		lines = append(lines, frame(s.panelDim.Render("enter save   esc cancel")))
	case overlayReader:
		height := m.readerHeight() - 3
		end := min(m.over.offset+height, len(m.over.lines))
		for _, line := range m.over.lines[m.over.offset:end] {
			lines = append(lines, frame(line))
		}
		for len(lines) < height+1 {
			lines = append(lines, frame(""))
		}
		footer := "esc close"
		if len(m.over.lines) > height {
			footer = "↑ ↓ scroll   space next page   esc close   " + itoa(min(end*100/len(m.over.lines), 100)) + "%"
		}
		lines = append(lines, frame(s.panelDim.Render(footer)))
	}
	lines = append(lines, s.line.Render("╰"+strings.Repeat("─", width-2)+"╯"))
	for i := range lines {
		lines[i] = m.padPanel(lines[i], width)
	}
	return lines
}

func (m Model) overlayWidth() int {
	if m.over.kind == overlayReader {
		return min(max(m.width-readerMargin*2, 30), 100)
	}
	return min(menuWidth, m.width-4)
}

// withOverlay draws the overlay over the screen, dimming the page behind it.
// While it's open the page takes no clicks, and a click outside it closes it.
func (m Model) withOverlay(screen []string) []string {
	t := easeOut(m.progress(m.overAt, overlayLength))
	bg := hexColor(themes[m.themeIndex].bg)
	for i := range screen {
		screen[i] = fadeLine(screen[i], 1-0.55*t, bg)
	}

	width := m.overlayWidth()
	x := (m.width - width) / 2
	*m.regions = nil
	height := len(m.overlayBox(0, 0))
	*m.regions = nil
	rise := 0
	if m.motion == motionFull {
		rise = int((1 - t) * 2)
	}
	y := max((m.height-height)/2-1, 1) + rise
	box := m.overlayBox(x, y)
	// The box's rows were recorded first, so they win over these.
	for row := 0; row < m.height; row++ {
		m.addRegion(row, 0, m.width, func(next Model) (Model, tea.Cmd) { return next.closeOverlay() })
	}

	for i, line := range box {
		row := y + i
		if row < 0 || row >= len(screen) {
			continue
		}
		// A wide character cut in half leaves a gap; padding closes it.
		left := m.pad(ansi.Cut(screen[row], 0, x), x)
		right := m.pad(ansi.Cut(screen[row], x+width, m.width), m.width-x-width)
		screen[row] = left + fadeLine(line, t, bg) + right
	}
	return screen
}

// padPanel fits text to width on the overlay's own background.
func (m Model) padPanel(s string, width int) string {
	if lipgloss.Width(s) > width {
		s = ansi.Truncate(s, width, "…")
	}
	return s + m.style.panelText.Render(strings.Repeat(" ", max(width-lipgloss.Width(s), 0)))
}

func itoa(n int) string { return strconv.Itoa(n) }
