package tui

import (
	"fmt"
	"strings"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/xin-yi6/zotty/internal/zotdb"
)

// The sidebar shows the collection tree, like Zotero's left pane. Choosing a
// collection filters the Library and Queue pages to the papers directly in it,
// which is also what Zotero shows by default.

const (
	sidebarMinWidth   = 28
	sidebarMaxWidth   = 34
	sidebarAutoWidth  = 150
	allItems          = 0
	sidebarHeaderRows = 2
)

// treeRow is one visible line of the tree. ID 0 is the whole library.
type treeRow struct {
	id, depth   int
	name        string
	items       int
	hasChildren bool
}

func (m Model) sidebarShown() bool {
	return m.sidebarOpen && m.page != pageInbox && m.width >= 60
}

func (m Model) sidebarWidth() int {
	if !m.sidebarShown() {
		return 0
	}
	return min(max(m.width/5, sidebarMinWidth), sidebarMaxWidth)
}

// tableX is where the table starts, after the sidebar and its gap.
func (m Model) tableX() int {
	if w := m.sidebarWidth(); w > 0 {
		return w + 1
	}
	return 0
}

// treeRows lists the rows of the tree, hiding the children of collapsed nodes.
func (m Model) treeRows() []treeRow {
	rows := []treeRow{{id: allItems, name: zotdb.LibraryName, items: len(m.items), hasChildren: len(m.collections) > 0}}
	if m.treeQuery != "" {
		return append(rows, m.matchingTreeRows()...)
	}
	hiddenBelow := -1
	for _, c := range m.collections {
		if hiddenBelow >= 0 && c.Depth > hiddenBelow {
			continue
		}
		hiddenBelow = -1
		rows = append(rows, treeRow{id: c.ID, depth: c.Depth + 1, name: c.Name, items: c.Items, hasChildren: c.HasChildren})
		if c.HasChildren && !m.expanded[c.ID] {
			hiddenBelow = c.Depth
		}
	}
	return rows
}

// matchingTreeRows lists the collections whose names contain the tree's
// search, each under its parents so it's clear where it lives.
func (m Model) matchingTreeRows() []treeRow {
	query := strings.ToLower(m.treeQuery)
	byID := map[int]zotdb.Collection{}
	for _, c := range m.collections {
		byID[c.ID] = c
	}
	keep := map[int]bool{}
	for _, c := range m.collections {
		if !strings.Contains(strings.ToLower(c.Name), query) {
			continue
		}
		for p, ok := c, true; ok; p, ok = byID[p.ParentID] {
			keep[p.ID] = true
		}
	}
	var rows []treeRow
	for _, c := range m.collections {
		if keep[c.ID] {
			rows = append(rows, treeRow{id: c.ID, depth: c.Depth + 1, name: c.Name, items: c.Items, hasChildren: c.HasChildren})
		}
	}
	return rows
}

func (m Model) sidebarRows() int {
	return max(m.tableRows(), 1)
}

func (m *Model) clampSidebar() {
	rows := len(m.treeRows())
	m.sideCursor = max(min(m.sideCursor, rows-1), 0)
	if m.sideCursor < m.sideOffset {
		m.sideOffset = m.sideCursor
	}
	if m.sideCursor >= m.sideOffset+m.sidebarRows() {
		m.sideOffset = m.sideCursor - m.sidebarRows() + 1
	}
	m.sideOffset = max(min(m.sideOffset, rows-m.sidebarRows()), 0)
}

// collectionPath names a collection with its parents, as in "QCD / Chiral PT".
func (m Model) collectionPath(id int) string {
	return zotdb.CollectionPath(m.collections, id)
}

func (m Model) toggleSidebar() (Model, tea.Cmd) {
	m.sidebarOpen = !m.sidebarOpen
	m.sidebarChosen = true
	m.sidebarFocused = m.sidebarOpen
	m.clampCursor()
	m.clampSidebar()
	return m, nil
}

// moveSidebar moves through the tree, showing each collection's papers as
// the cursor reaches it, as clicking through Zotero's pane does.
func (m Model) moveSidebar(delta int) (Model, tea.Cmd) {
	m.sideCursor += delta
	m.clampSidebar()
	rows := m.treeRows()
	if m.sideCursor < len(rows) && rows[m.sideCursor].id != m.collection {
		m.collection = rows[m.sideCursor].id
		m.resetListPositions()
		m.refreshList()
		return m, m.selectionChanged()
	}
	return m, nil
}

func (m *Model) resetListPositions() {
	for i := range m.positions {
		if i != inboxPosition {
			m.positions[i] = listPosition{}
		}
	}
}

// chooseCollection filters the lists to a collection and starts them at the top.
func (m Model) chooseCollection(id int) (Model, tea.Cmd) {
	m.collection = id
	m.resetListPositions()
	m.refreshList()
	return m.listChanged()
}

func (m Model) setExpanded(id int, open bool) (Model, tea.Cmd) {
	expanded := make(map[int]bool, len(m.expanded))
	for k, v := range m.expanded {
		expanded[k] = v
	}
	expanded[id] = open
	m.expanded = expanded
	m.clampSidebar()
	return m, nil
}

func (m Model) handleSidebarKey(msg tea.KeyPressMsg) (Model, tea.Cmd, bool) {
	key := msg.String()
	if m.treeSearching {
		switch key {
		case "esc":
			m.treeSearching, m.treeQuery = false, ""
		case "backspace":
			if r := []rune(m.treeQuery); len(r) > 0 {
				m.treeQuery = string(r[:len(r)-1])
			}
		case "up", "down", "enter":
			m.treeSearching = false
			return m.handleSidebarKey(msg)
		default:
			if msg.Text == "" {
				return m, nil, true
			}
			m.treeQuery += msg.Text
		}
		// Land on the first collection that matches, not on its parents.
		m.sideCursor, m.sideOffset = 0, 0
		query := strings.ToLower(m.treeQuery)
		for i, row := range m.treeRows() {
			if i > 0 && strings.Contains(strings.ToLower(row.name), query) {
				m.sideCursor = i
				break
			}
		}
		next, cmd := m.moveSidebar(0)
		return next, cmd, true
	}

	rows := m.treeRows()
	row := rows[min(m.sideCursor, len(rows)-1)]
	focusList := func(m Model) (Model, tea.Cmd, bool) {
		m.sidebarFocused = false
		return m, nil, true
	}
	switch key {
	case "down":
		next, cmd := m.moveSidebar(1)
		return next, cmd, true
	case "up":
		next, cmd := m.moveSidebar(-1)
		return next, cmd, true
	case "home":
		next, cmd := m.moveSidebar(-len(rows))
		return next, cmd, true
	case "end":
		next, cmd := m.moveSidebar(len(rows))
		return next, cmd, true
	case "/":
		m.treeSearching = true
		return m, nil, true
	case "enter", "space", " ":
		next, cmd := m.chooseCollection(row.id)
		next.sidebarFocused = false
		return next, cmd, true
	case "right":
		// Open a closed branch; otherwise step over into the list.
		if row.hasChildren && row.id != allItems && !m.expanded[row.id] && m.treeQuery == "" {
			next, cmd := m.setExpanded(row.id, true)
			return next, cmd, true
		}
		return focusList(m)
	case "left":
		if row.id != allItems && m.expanded[row.id] && m.treeQuery == "" {
			next, cmd := m.setExpanded(row.id, false)
			return next, cmd, true
		}
		for i := m.sideCursor - 1; i >= 0; i-- {
			if rows[i].depth < row.depth {
				next, cmd := m.moveSidebar(i - m.sideCursor)
				return next, cmd, true
			}
		}
		return m, nil, true
	case "esc":
		if m.treeQuery != "" {
			m.treeQuery = ""
			m.clampSidebar()
			return m, nil, true
		}
		return focusList(m)
	case "tab":
		return focusList(m)
	}
	return m, nil, false
}

// focusSidebar moves the keyboard to the tree, opening it if it's closed.
func (m Model) focusSidebar() (Model, tea.Cmd) {
	if !m.sidebarShown() {
		m.sidebarOpen = true
		m.sidebarChosen = true
	}
	m.sidebarFocused = true
	for i, row := range m.treeRows() {
		if row.id == m.collection {
			m.sideCursor = i
		}
	}
	m.clampSidebar()
	m.clampCursor()
	return m, nil
}

// sidebarLines draws the tree. Clicking a name chooses the collection, and
// clicking its arrow opens or closes it.
func (m Model) sidebarLines(height int) []string {
	s := m.style
	width := m.sidebarWidth()
	title := s.dim.Render(" C O L L E C T I O N S")
	if m.sidebarFocused {
		title = s.accent.Render(" C O L L E C T I O N S")
	}
	switch {
	case m.treeSearching:
		title = s.accent.Render(" / ") + s.base.Render(m.treeQuery) + s.accent.Render("▏")
	case m.treeQuery != "":
		title = s.dim.Render(" / ") + s.base.Render(m.treeQuery) + s.dim.Render("  esc clears")
	}
	m.addRegion(2, 0, width, func(next Model) (Model, tea.Cmd) {
		next, cmd := next.focusSidebar()
		next.treeSearching = true
		return next, cmd
	})
	lines := []string{m.pad(title, width), m.pad(s.line.Render(" "+strings.Repeat("─", max(width-2, 0))), width)}

	rows := m.treeRows()
	end := min(m.sideOffset+m.sidebarRows(), len(rows))
	for i := m.sideOffset; i < end; i++ {
		row := rows[i]
		y := 2 + len(lines)
		index := i
		indent := strings.Repeat("  ", row.depth)
		arrow := "  "
		if row.hasChildren && row.id != allItems {
			arrow = "▸ "
			if m.expanded[row.id] {
				arrow = "▾ "
			}
			arrowX := 1 + lipgloss.Width(indent)
			id, open := row.id, !m.expanded[row.id]
			m.addRegion(y, arrowX, arrowX+2, func(next Model) (Model, tea.Cmd) {
				next.sidebarFocused = true
				next.sideCursor = index
				return next.setExpanded(id, open)
			})
		}
		id := row.id
		m.addRegion(y, 0, width, func(next Model) (Model, tea.Cmd) {
			next.sidebarFocused = true
			next.sideCursor = index
			next.clampSidebar()
			return next.chooseCollection(id)
		})

		text, dim := s.soft, s.dim
		if row.id == m.collection {
			text = s.accent
		}
		if m.sidebarFocused && i == m.sideCursor {
			text, dim = s.selected, s.selectedDim
			if row.id == m.collection {
				text = s.selectedAccent
			}
		}
		count := fmt.Sprintf(" %d ", row.items)
		nameWidth := max(width-1-lipgloss.Width(indent)-2-lipgloss.Width(count), 1)
		name := ansi.Truncate(row.name, nameWidth, "…")
		name += strings.Repeat(" ", max(nameWidth-lipgloss.Width(name), 0))
		lines = append(lines, m.pad(dim.Render(" "+indent+arrow)+text.Render(name)+dim.Render(count), width))
	}
	for len(lines) < height {
		lines = append(lines, m.pad("", width))
	}
	return lines
}
