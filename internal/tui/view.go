package tui

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/xin-yi6/zotty/internal/inbox"
	"github.com/xin-yi6/zotty/internal/zotdb"
)

const (
	minPanelWidth  = 100
	minPanelHeight = 20
	// chromeLines is everything around the rows: the page tabs and a blank
	// line, the table header and rule, and a blank line and the status line.
	chromeLines      = 6
	tableHeaderLines = 2
	authorWidth      = 16
	yearWidth        = 5
	numberWidth      = 5
	minTitleWidth    = 30
)

var (
	titleMarkup  = regexp.MustCompile(`</?(i|b|sub|sup|em|strong)>`)
	latexCommand = regexp.MustCompile(`\\[a-zA-Z]+\{([^{}]*)\}`)
)

var groupLabels = map[string]string{
	zotdb.GroupToRead:     "To read",
	zotdb.GroupInProgress: "In progress",
	zotdb.GroupNew:        "New",
	zotdb.GroupRead:       "Read",
}

// groupGlyphs mark reading states in lists. Shapes rather than colors carry
// the meaning, so they read the same in every theme.
var groupGlyphs = map[string]string{
	zotdb.GroupToRead:     "◇",
	zotdb.GroupInProgress: "◐",
	zotdb.GroupNew:        "○",
	zotdb.GroupRead:       "●",
}

// View draws the screen.
func (m Model) View() tea.View {
	var content string
	switch {
	case m.width == 0 || m.height == 0:
	case m.startupActive():
		*m.regions = (*m.regions)[:0]
		content = m.startupScene()
	default:
		lines := m.renderLines()
		if m.over.kind != overlayNone {
			lines = m.withOverlay(lines)
		}
		content = strings.Join(lines, "\n")
	}
	view := tea.NewView(content)
	view.AltScreen = true
	view.MouseMode = tea.MouseModeCellMotion
	view.BackgroundColor = m.style.background
	view.WindowTitle = "zotty"
	return view
}

// contentWidth is the room right of the sidebar.
func (m Model) contentWidth() int {
	return m.width - m.tableX()
}

func (m Model) wide() bool {
	return m.contentWidth() >= minPanelWidth && m.height >= minPanelHeight
}

func (m Model) panelWidth() int {
	return min(max(m.contentWidth()*38/100, 36), 48)
}

func (m Model) tableWidth() int {
	if m.wide() {
		return m.contentWidth() - m.panelWidth() - 1
	}
	return m.contentWidth()
}

// rowHeight is how many lines each paper takes: its own, and a blank one
// between it and the next unless rows are packed.
func (m Model) rowHeight() int {
	if m.compact {
		return 1
	}
	return 2
}

// pageItems is how many papers fit on screen at once.
func (m Model) pageItems() int {
	return max(m.tableRows()/m.rowHeight(), 1)
}

func (m Model) tableRows() int {
	return max(m.height-chromeLines, 1)
}

func (m Model) panelOnly() bool {
	return m.showPanel && !m.wide()
}

// pad fits text to an exact width. Text from one span keeps that span's color,
// and the padding uses the page background.
func (m Model) pad(s string, width int) string {
	if lipgloss.Width(s) > width {
		s = ansi.Truncate(s, width, "…")
	}
	return s + m.style.base.Render(strings.Repeat(" ", max(width-lipgloss.Width(s), 0)))
}

func (m Model) addRegion(y, x0, x1 int, action func(Model) (Model, tea.Cmd)) {
	*m.regions = append(*m.regions, region{y, x0, x1, action})
}

// render draws the page alone, without the opening or overlays.
func (m Model) render() string {
	if m.width == 0 || m.height == 0 {
		return ""
	}
	return strings.Join(m.renderLines(), "\n")
}

func (m Model) renderLines() []string {
	*m.regions = (*m.regions)[:0]

	lines := make([]string, 0, m.height)
	lines = append(lines, m.topBar(), m.pad("", m.width))

	if m.showHelp {
		lines = append(lines, m.helpLines()...)
	} else {
		bodyHeight := m.height - 5
		var body []string
		if m.panelOnly() {
			body = m.panel(m.tableX(), 2, m.contentWidth(), bodyHeight)
			for len(body) < bodyHeight {
				body = append(body, m.pad("", m.contentWidth()))
			}
		} else {
			body = m.tableLines()
		}
		if m.wide() {
			panelX := m.tableX() + m.tableWidth() + 1
			panel := m.panel(panelX, 2, m.panelWidth(), bodyHeight)
			for i := range body {
				right := m.pad("", m.panelWidth())
				if i < len(panel) {
					right = panel[i]
				}
				body[i] += m.style.base.Render(" ") + right
			}
		}
		if m.sidebarShown() {
			side := m.sidebarLines(len(body))
			for i := range body {
				body[i] = side[i] + m.style.line.Render("│") + body[i]
			}
		}
		lines = append(lines, body...)
	}

	for len(lines) < m.height-2 {
		lines = append(lines, m.pad("", m.width))
	}
	lines = lines[:min(len(lines), m.height-2)]
	lines = append(lines, m.pad("", m.width), m.statusLine())
	return lines
}

func (m Model) topBar() string {
	s := m.style
	out := s.base.Render(" ")
	x := 1
	for i := page(0); i < pageCount; i++ {
		name := pageNames[i]
		if i == pageInbox && len(m.entries) > 0 {
			name += " " + strconv.Itoa(len(m.entries))
		}
		label := s.dim.Render("  " + name + " ")
		if i == m.page {
			label = s.accent.Render("■ ") + s.bold.Render(name) + s.base.Render(" ")
		}
		width := lipgloss.Width(label)
		target := i
		m.addRegion(0, x, x+width, func(next Model) (Model, tea.Cmd) { return next.setPage(target) })
		out += label + s.base.Render(" ")
		x += width + 1
	}
	right := ""
	if !m.loading && m.loadErr == nil && m.listLength() > 0 {
		right = fmt.Sprintf("%d / %d", m.position().cursor+1, m.listLength())
	}
	if m.collection != allItems && m.page != pageInbox {
		// A deep path keeps its end, the collection you're in.
		path := m.collectionPath(m.collection)
		room := m.width - lipgloss.Width(out) - lipgloss.Width(right) - 7
		if w := lipgloss.Width(path); w > room && room > 4 {
			path = "…" + ansi.TruncateLeft(path, w-room+1, "")
		}
		if room > 4 {
			right = path + "   " + right
		}
	}
	gap := m.width - lipgloss.Width(out) - lipgloss.Width(right) - 1
	return m.pad(out+s.base.Render(strings.Repeat(" ", max(gap, 1)))+s.dim.Render(right), m.width)
}

func (m Model) tableLines() []string {
	s := m.style
	width := m.tableWidth()
	lines := []string{m.tableHeader(width), m.pad(s.line.Render(" "+strings.Repeat("─", max(width-2, 0))), width)}

	if m.loading && len(m.items) == 0 {
		return m.fill(append(lines, m.pad(s.dim.Render("   Reading your library…"), width)), width)
	}
	if m.loadErr != nil {
		return m.fill(append(lines, m.pad(s.accent.Render("   "+m.loadErr.Error()), width)), width)
	}
	if m.listLength() == 0 {
		return m.fill(append(lines, m.pad(s.dim.Render("   "+m.emptyMessage()), width)), width)
	}

	p := m.position()
	end := min(p.offset+m.pageItems(), m.listLength())
	first := len(lines) + 2
	bg := hexColor(themes[m.themeIndex].bg)
	banding := m.progress(m.bandAt, bandLength) < 1
	for i := p.offset; i < end; i++ {
		index := i
		selected := i == p.cursor
		leaving := banding && i == m.bandFrom && !selected
		var rows []string
		if m.page == pageInbox {
			rows = m.entryRow(i, width, selected || leaving)
		} else {
			rows = m.itemRow(i, width, selected || leaving)
		}
		slide, fade := m.rowIn(i - p.offset)
		for j, line := range rows {
			m.addRegion(first+len(lines)-tableHeaderLines, m.tableX(), m.tableX()+width, func(next Model) (Model, tea.Cmd) { return next.clickRow(index) })
			if banding && (selected || leaving) && j == 0 {
				line = m.bandLine(line, selected)
			}
			lines = append(lines, fadeLine(m.slideLine(line, slide, width), fade, bg))
		}
	}
	return m.fill(lines, width)
}

func (m Model) fill(lines []string, width int) []string {
	for len(lines) < tableHeaderLines+m.tableRows() {
		lines = append(lines, m.pad("", width))
	}
	return lines
}

func (m Model) emptyMessage() string {
	switch {
	case m.page == pageInbox:
		return "Papers you save from Chrome while Zotero is closed appear here"
	case m.query != "":
		return "Nothing matches that search"
	case len(m.items) == 0 && !m.loading:
		return "Your library is empty. Save a paper from Chrome, or press a to add one by arXiv ID or DOI"
	}
	return "No papers here"
}

func (m Model) tableHeader(width int) string {
	s := m.style
	switch m.page {
	case pageQueue:
		out := s.base.Render(" ")
		x := m.tableX() + 1
		for i, group := range zotdb.QueueGroups {
			label := fmt.Sprintf(" %s %s %d ", groupGlyphs[group], groupLabels[group], m.groupCounts[group])
			text := s.dim.Render(label)
			if i == m.group {
				text = s.buttonOn.Render(label)
			}
			index := i
			w := lipgloss.Width(label)
			m.addRegion(2, x, x+w, func(next Model) (Model, tea.Cmd) { return next.setGroup(index) })
			out += text + s.base.Render(" ")
			x += w + 1
		}
		return m.withSortLabel(out, width)
	case pageInbox:
		return m.pad(s.dim.Render("   Waiting to go into Zotero"), width)
	}

	// The sort label at the right takes its own clicks.
	m.addRegion(2, m.tableX(), m.tableX()+width-m.sortLabelWidth(), func(next Model) (Model, tea.Cmd) {
		next.searching = true
		next.sidebarFocused = false
		return next, nil
	})
	scope := s.dim.Render("   tab: inside PDFs")
	count := fmt.Sprintf("   %d matches", len(m.visible))
	if m.textScope {
		scope = s.accent.Render("   inside PDFs") + s.dim.Render(" · tab: titles and authors")
		count = fmt.Sprintf("   %d PDFs", len(m.visible))
		if m.textMatches == nil && strings.TrimSpace(m.query) != "" {
			count = "   searching…"
		}
	}
	switch {
	case m.searching:
		line := s.accent.Render(" / ") + s.base.Render(m.query) + s.accent.Render("▏")
		if m.query != "" {
			line += s.dim.Render(count)
		}
		return m.pad(line+scope, width)
	case m.query != "":
		return m.pad(s.dim.Render(" / ")+s.base.Render(m.query)+s.dim.Render(count)+s.dim.Render("   esc clears"), width)
	}
	return m.withSortLabel(s.dim.Render(" / Search your library"), width)
}

// statusGlyph draws reading state as a shape, so it survives every theme and
// doesn't depend on seeing color.
func (m Model) statusGlyph(status string, selected bool) string {
	s := m.style
	accent, dim := s.accent, s.dim
	if selected {
		accent, dim = s.selectedAccent, s.selectedDim
	}
	switch group := zotdb.GroupOf(status); group {
	case zotdb.GroupToRead, zotdb.GroupInProgress:
		return accent.Render(groupGlyphs[group])
	case zotdb.GroupNew, zotdb.GroupRead:
		return dim.Render(groupGlyphs[group])
	}
	return dim.Render("·")
}

func (m Model) itemRow(i, width int, selected bool) []string {
	it := m.visible[i]
	glyph := m.statusGlyph(it.Status, selected)
	return m.spaced(m.row(width, selected, i+1, cleanTitle(it.Title), it.FirstAuthor(), it.Year, glyph), width)
}

func (m Model) entryRow(i, width int, selected bool) []string {
	e := m.entries[i]
	mark := m.style.dim.Render("○")
	if e.ImportError != "" {
		mark = m.style.accent.Render("!")
	}
	if selected {
		mark = m.style.selectedDim.Render("○")
		if e.ImportError != "" {
			mark = m.style.selectedAccent.Render("!")
		}
	}
	return m.spaced(m.row(width, selected, i+1, e.Title, e.CreatedAt.Local().Format("Jan 02 15:04"), "", mark), width)
}

// spaced adds the blank line that keeps rows apart, unless they're packed.
func (m Model) spaced(line string, width int) []string {
	if m.compact {
		return []string{line}
	}
	return []string{line, m.pad("", width)}
}

func (m Model) row(width int, selected bool, number int, title, author, year, glyph string) string {
	s := m.style
	bar, text, dim := s.base.Render(" "), s.soft, s.dim
	if selected {
		bar, text, dim = s.selectedAccent.Render("▎"), s.selected, s.selectedDim
	}
	cell := func(style lipgloss.Style, value string, w int) string {
		value = ansi.Truncate(value, w, "…")
		return style.Render(value + strings.Repeat(" ", max(w-lipgloss.Width(value), 0)))
	}
	// In a narrow list the title keeps its room: the author goes first, then
	// the year.
	fixed := 1 + numberWidth + 1 + 3
	columns := ""
	switch rest := width - fixed; {
	case rest-3-authorWidth-2-yearWidth >= minTitleWidth:
		columns = dim.Render("   ") + cell(dim, author, authorWidth) + dim.Render("  ") + cell(dim, year, yearWidth)
	case rest-3-yearWidth >= minTitleWidth:
		columns = dim.Render("   ") + cell(dim, year, yearWidth)
	}
	titleWidth := max(width-fixed-lipgloss.Width(columns), 4)
	line := bar +
		dim.Render(fmt.Sprintf("%*d  ", numberWidth-1, number)) +
		cell(text, title, titleWidth) + columns +
		dim.Render(" ") + glyph + dim.Render(" ")
	return m.pad(line, width)
}

// panel draws the selected paper. x and y locate it on screen, for its buttons.
func (m Model) panel(x, y, width, height int) []string {
	if m.page == pageInbox {
		return m.entryPanel(width, height)
	}
	it, ok := m.selected()
	if !ok {
		return nil
	}
	s := m.style
	inner := width - 2
	add := func(lines []string, line string) []string { return append(lines, m.pad(line, width)) }

	lines := add(nil, s.accent.Render("■ ")+s.dim.Render("S E L E C T E D"))
	lines = add(lines, "")

	room := height - len(lines)
	if thumb, ok := m.thumbs[it.Key]; ok && room >= thumbRows+10 {
		indent := s.base.Render(strings.Repeat(" ", max((width-thumbCols)/2, 0)))
		for _, row := range m.flippedThumb(thumb.lines(themes[m.themeIndex])) {
			lines = append(lines, indent+row+s.base.Render(strings.Repeat(" ", max(width-thumbCols-(width-thumbCols)/2, 0))))
		}
		lines = add(lines, "")
	}
	// The text below the page fades in when the selection changes.
	textFrom := len(lines)

	for i, line := range strings.Split(ansi.Wrap(cleanTitle(it.Title), inner, " "), "\n") {
		if i == 3 {
			break
		}
		lines = add(lines, s.bold.Render(line))
	}
	lines = add(lines, s.dim.Render(ansi.Truncate(shortAuthors(it.Surnames), inner, "…")))
	citation := strings.TrimSpace(it.Venue + " " + it.Year)
	if citation != "" {
		lines = add(lines, s.dim.Render(ansi.Truncate(citation, inner, "…")))
	}
	lines = add(lines, "")

	statusLabel := it.Status
	if statusLabel == "" {
		statusLabel = "No state"
	}
	buttonY := y + len(lines)
	m.addRegion(buttonY, x, x+6, func(next Model) (Model, tea.Cmd) { return next.openSelected() })
	row := s.button.Render(" Open ") + s.base.Render("  ")
	chipX := x + 8
	// One button per reading state; the current one is lit.
	for _, status := range []string{"To Read", "In Progress", "New", "Read"} {
		style := s.button
		if zotdb.GroupOf(it.Status) == zotdb.GroupOf(status) {
			style = s.buttonOn
		}
		target := status
		m.addRegion(buttonY, chipX, chipX+3, func(next Model) (Model, tea.Cmd) { return next.setStatus(target) })
		row += style.Render(" "+groupGlyphs[zotdb.GroupOf(status)]+" ") + s.base.Render(" ")
		chipX += 4
	}
	m.addRegion(buttonY, chipX, chipX+3, func(next Model) (Model, tea.Cmd) { return next.paperMenu() })
	row += s.button.Render(" ⋯ ") + s.base.Render("  ")
	lines = add(lines, row+s.soft.Render(statusLabel))

	var extras []string
	if it.Notes > 0 {
		extras = append(extras, plural(it.Notes, "note"))
	}
	if it.Highlights > 0 {
		extras = append(extras, plural(it.Highlights, "highlight"))
	}
	if len(extras) > 0 {
		lines = add(lines, s.dim.Render(strings.Join(extras, " · ")))
	}
	lines = add(lines, "")

	if it.Abstract != "" {
		space := height - len(lines)
		abstract := strings.Split(ansi.Wrap(strings.Join(strings.Fields(it.Abstract), " "), inner, " "), "\n")
		for i, line := range abstract {
			if i >= space {
				break
			}
			if i == space-1 && i < len(abstract)-1 {
				line = ansi.Truncate(line, inner-1, "") + "…"
			}
			lines = add(lines, s.soft.Render(line))
		}
	}
	if t := easeOut(m.progress(m.detailAt, fadeLength)); t < 1 {
		bg := hexColor(themes[m.themeIndex].bg)
		for i := textFrom; i < len(lines); i++ {
			lines[i] = fadeLine(lines[i], 0.2+0.8*t, bg)
		}
	}
	return lines
}

func (m Model) entryPanel(width, height int) []string {
	entry, ok := m.selectedEntry()
	if !ok {
		return nil
	}
	s := m.style
	inner := width - 2
	add := func(lines []string, line string) []string { return append(lines, m.pad(line, width)) }
	lines := add(nil, s.accent.Render("■ ")+s.dim.Render("S A V E D  F R O M  C H R O M E"))
	lines = add(lines, "")
	for _, line := range strings.Split(ansi.Wrap(entry.Title, inner, " "), "\n") {
		lines = add(lines, s.bold.Render(line))
	}
	lines = add(lines, s.dim.Render(ansi.Truncate(entry.URL, inner, "…")))
	lines = add(lines, s.dim.Render(entry.CreatedAt.Local().Format(time.DateTime)))
	lines = add(lines, "")
	lines = add(lines, s.soft.Render(entryFiles(entry)))
	lines = add(lines, "")
	lines = add(lines, s.dim.Render("enter opens the PDF. x twice removes it"))
	if entry.ImportError != "" {
		lines = add(lines, "")
		for _, line := range strings.Split(ansi.Wrap("Import stopped partway. Check this paper in Zotero: "+entry.ImportError, inner, " "), "\n") {
			lines = add(lines, s.accent.Render(line))
		}
	}
	return lines[:min(len(lines), height)]
}

func entryFiles(e inbox.Entry) string {
	for _, a := range e.Attachments {
		if a.ContentType == "application/pdf" {
			return fmt.Sprintf("PDF saved, %.1f MB", float64(a.Size)/(1<<20))
		}
	}
	return "No PDF"
}

func plural(n int, word string) string {
	if n == 1 {
		return "1 " + word
	}
	return fmt.Sprintf("%d %ss", n, word)
}

func (m Model) statusLine() string {
	s := m.style
	if m.message != "" {
		// A message slides in from the left as it fades up.
		t := easeOut(m.progress(m.messageAt, toastLength))
		slide := 0
		if m.motion == motionFull {
			slide = int((1 - t) * 4)
		}
		line := m.slideLine(m.pad(s.accent.Render(" "+m.message), m.width), slide, m.width)
		return fadeLine(line, t, hexColor(themes[m.themeIndex].bg))
	}
	hints := " tab pages   space actions   : commands   ← collections   o sort   ? help"
	switch {
	case m.over.kind != overlayNone:
		hints = ""
	case m.searching:
		hints = " type to search   ↑ ↓ move   tab inside PDFs   enter done   esc clear"
	case m.treeSearching:
		hints = " type a collection's name   ↑ ↓ move   enter choose   esc clear"
	case m.sidebarFocused && m.sidebarShown():
		hints = " ↑ ↓ show a collection   → open or go to list   ← close   / search names   ? help"
	case m.page == pageQueue:
		hints = " tab pages   ← → groups   s next state   space actions   o sort   ? help"
	case m.page == pageInbox:
		hints = " tab pages   enter open PDF   x x remove   : commands   ? help"
	}
	return m.pad(s.dim.Render(hints), m.width)
}

func (m Model) helpLines() []string {
	s := m.style
	type entry struct{ key, text string }
	sections := []struct {
		title   string
		entries []entry
	}{
		{"E V E R Y T H I N G", []entry{
			{"space", "Actions for the selected paper"},
			{":", "Every command: sort, layout, theme, animation"},
		}},
		{"M O V E", []entry{
			{"↑ ↓", "Move. The wheel scrolls"},
			{"← →", "Between collections and the list, and through queue groups"},
			{"click", "Select. Double-click opens the PDF"},
			{"tab", "Library, Queue, Inbox"},
			{"c", "Show or hide collections"},
		}},
		{"F I N D", []entry{
			{"/", "Search. Names, years, and title words are recognized"},
			{"tab", "While searching, switch to searching inside PDFs"},
			{"/", "In the collections, search their names"},
			{"o", "Sort: date added or modified, year, title, author"},
			{"", "To be exact: a:picard  t:hydro  y:2026  s:read  c:qcd  #tag"},
		}},
		{"C H A N G E", []entry{
			{"enter", "Open the PDF"},
			{"s", "Next reading state, or click a state beside Open"},
			{"e", "Edit tags"},
			{"+ -", "Add to or remove from a collection"},
			{"n", "Notes and highlights"},
			{"a", "Add a paper by arXiv ID or DOI"},
			{"x x", "Remove a paper from the inbox"},
		}},
		{"R E A D I N G  S T A T E", []entry{
			{"◇ ◐ ○ ●", "To read, in progress, new, read.  · means none"},
		}},
		{"S E T T I N G S", []entry{
			{"t  m", "Next theme, animation on or off"},
			{"d  q", "Details in a narrow window, quit"},
		}},
	}
	var lines []string
	for _, section := range sections {
		lines = append(lines, m.pad(s.dim.Render("   "+section.title), m.width))
		for _, e := range section.entries {
			lines = append(lines, m.pad(s.key.Render(fmt.Sprintf("   %-14s", e.key))+s.soft.Render(e.text), m.width))
		}
		lines = append(lines, m.pad("", m.width))
	}
	return append(lines, m.pad(s.dim.Render("   Press any key to close"), m.width))
}

// withSortLabel adds the current order at the right of a header line. Clicking
// it offers the others.
func (m Model) sortLabel() string { return "Sort: " + sortOrders[m.sortBy].label + " ▾ " }

func (m Model) sortLabelWidth() int { return lipgloss.Width(m.sortLabel()) }

func (m Model) withSortLabel(left string, width int) string {
	label := m.sortLabel()
	gap := width - lipgloss.Width(left) - lipgloss.Width(label)
	if gap < 2 {
		return m.pad(left, width)
	}
	x := m.tableX() + width - lipgloss.Width(label)
	m.addRegion(2, x, x+lipgloss.Width(label), func(next Model) (Model, tea.Cmd) { return next.sortMenu() })
	return m.pad(left+m.style.base.Render(strings.Repeat(" ", gap))+m.style.dim.Render(label), width)
}
