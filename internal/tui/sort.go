package tui

import (
	"slices"
	"strings"

	tea "charm.land/bubbletea/v2"

	"github.com/xin-yi6/zotty/internal/zotdb"
)

// sortOrder is how lists are ordered. A search ranks by match instead, since
// the best match matters more there than any order.
type sortOrder struct {
	name  string
	label string
	less  func(a, b zotdb.Item) int
}

var sortOrders = []sortOrder{
	{"added", "Date added", func(a, b zotdb.Item) int { return strings.Compare(b.DateAdded, a.DateAdded) }},
	{"modified", "Date modified", func(a, b zotdb.Item) int { return strings.Compare(b.DateModified, a.DateModified) }},
	{"year", "Year, newest first", func(a, b zotdb.Item) int { return strings.Compare(b.Year, a.Year) }},
	{"title", "Title", func(a, b zotdb.Item) int {
		return strings.Compare(strings.ToLower(cleanTitle(a.Title)), strings.ToLower(cleanTitle(b.Title)))
	}},
	{"author", "First author", func(a, b zotdb.Item) int {
		return strings.Compare(strings.ToLower(a.FirstAuthor()), strings.ToLower(b.FirstAuthor()))
	}},
}

func sortIndex(name string) int {
	for i, o := range sortOrders {
		if o.name == name {
			return i
		}
	}
	return 0
}

// sorted orders items, keeping the library's own order, newest added first,
// among items that tie.
func (m Model) sorted(items []zotdb.Item) []zotdb.Item {
	if m.sortBy == 0 {
		return items
	}
	out := slices.Clone(items)
	slices.SortStableFunc(out, sortOrders[m.sortBy].less)
	return out
}

func (m Model) sortMenu() (Model, tea.Cmd) {
	var items []menuItem
	for i, o := range sortOrders {
		index := i
		label := o.label
		if i == m.sortBy {
			label += "  ✓"
		}
		items = append(items, menuItem{label: label, action: func(m Model) (Model, tea.Cmd) { return m.setSort(index) }})
	}
	return m.openOverlay(overlay{kind: overlayMenu, title: "Sort by", items: items})
}

func (m Model) setSort(index int) (Model, tea.Cmd) {
	m.sortBy = index
	m.message = "Sorted by " + strings.ToLower(sortOrders[index].label)
	if err := saveSetting(m.config.DataDir, "sort", sortOrders[index].name); err != nil {
		m.message = "Couldn't save the setting"
	}
	m.resetListPositions()
	m.refreshList()
	return m.listChanged()
}

func (m Model) setCompact(compact bool) (Model, tea.Cmd) {
	m.compact = compact
	value, label := "spaced", "Rows spaced out"
	if compact {
		value, label = "packed", "Rows packed closer"
	}
	m.message = label
	if err := saveSetting(m.config.DataDir, "rows", value); err != nil {
		m.message = "Couldn't save the setting"
	}
	m.clampCursor()
	return m.listChanged()
}
