package tui

import (
	"errors"
	"fmt"
	"os/exec"
	"slices"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/xin-yi6/zotty/internal/adder"
	"github.com/xin-yi6/zotty/internal/importer"
	"github.com/xin-yi6/zotty/internal/zotapi"
	"github.com/xin-yi6/zotty/internal/zotdb"
)

// Everything zotty can do is reachable here: space opens the actions for the
// selected paper, and : opens every command. Each menu row shows its key, so
// the menus also teach the shortcuts.

type (
	writeDoneMsg struct {
		queued bool
		err    error
	}
	addDoneMsg struct {
		result adder.Result
		err    error
	}
	importDoneMsg struct {
		result importer.Result
		err    error
	}
	notesLoadedMsg struct {
		title   string
		details zotdb.Details
		err     error
	}
	textSearchMsg struct{ query string }
	textResultMsg struct {
		query string
		ids   map[int]bool
		err   error
	}
	toastExpiredMsg struct{ at time.Time }
)

const textSearchDelay = 300 * time.Millisecond

func (m Model) paperMenu() (Model, tea.Cmd) {
	it, ok := m.selected()
	if !ok {
		return m, nil
	}
	items := []menuItem{
		{label: "Open the PDF", key: "↵", action: func(m Model) (Model, tea.Cmd) { return m.openSelected() }},
		{label: "Notes and highlights", key: "n", action: func(m Model) (Model, tea.Cmd) { return m.showNotes() }},
	}
	for _, status := range []string{"To Read", "In Progress", "New", "Read"} {
		if zotdb.GroupOf(it.Status) == zotdb.GroupOf(status) {
			continue
		}
		target := status
		items = append(items, menuItem{
			label: "Mark as " + groupLabels[zotdb.GroupOf(status)], detail: groupGlyphs[zotdb.GroupOf(status)],
			action: func(m Model) (Model, tea.Cmd) { return m.setStatus(target) },
		})
	}
	items = append(items,
		menuItem{label: "Edit tags", key: "e", action: func(m Model) (Model, tea.Cmd) { return m.editTags() }},
		menuItem{label: "Add to a collection", key: "+", action: func(m Model) (Model, tea.Cmd) { return m.pickCollection(true) }},
	)
	if len(it.CollectionIDs) > 0 {
		items = append(items, menuItem{label: "Remove from a collection", key: "-", action: func(m Model) (Model, tea.Cmd) { return m.pickCollection(false) }})
	}
	items = append(items, menuItem{label: "Show in Zotero", key: "z", detail: "opens Zotero", action: func(m Model) (Model, tea.Cmd) {
		return m, showInZotero(it.Key)
	}})
	return m.openOverlay(overlay{kind: overlayMenu, title: ansi.Truncate(cleanTitle(it.Title), menuWidth-8, "…"), items: items})
}

func (m Model) palette() (Model, tea.Cmd) {
	items := []menuItem{
		{label: "Add a paper by arXiv ID or DOI", key: "a", action: func(m Model) (Model, tea.Cmd) { return m.addPrompt() }},
		{label: "Search titles, authors, years", key: "/", action: func(m Model) (Model, tea.Cmd) { return m.startSearch() }},
		{label: "Search inside PDFs", detail: "or tab while searching", action: func(m Model) (Model, tea.Cmd) { return m.startTextSearch() }},
		{label: "Sort by", key: "o", action: func(m Model) (Model, tea.Cmd) { return m.sortMenu() }},
		{label: "Show or hide collections", key: "c", action: func(m Model) (Model, tea.Cmd) { return m.toggleSidebar() }},
	}
	if len(m.entries) > 0 {
		items = append(items, menuItem{label: "Import the inbox into Zotero now", detail: "zotty does this itself when Zotero opens", action: func(m Model) (Model, tea.Cmd) { return m.importInbox() }})
	}
	if _, ok := m.selected(); ok {
		items = append(items, menuItem{label: "Actions for the selected paper", key: "space", action: func(m Model) (Model, tea.Cmd) { return m.paperMenu() }})
	}
	for i, th := range themes {
		index := i
		label := "Theme: " + th.name
		if i == m.themeIndex {
			label += "  ✓"
		}
		items = append(items, menuItem{label: label, action: func(m Model) (Model, tea.Cmd) { return m.setTheme(index) }})
	}
	if m.compact {
		items = append(items, menuItem{label: "Space out the rows", action: func(m Model) (Model, tea.Cmd) { return m.setCompact(false) }})
	} else {
		items = append(items, menuItem{label: "Pack the rows closer", action: func(m Model) (Model, tea.Cmd) { return m.setCompact(true) }})
	}
	if m.motion == motionOff {
		items = append(items, menuItem{label: "Turn animation on", key: "m", action: func(m Model) (Model, tea.Cmd) { return m.setMotion(motionFull) }})
	} else {
		items = append(items, menuItem{label: "Turn animation off", key: "m", action: func(m Model) (Model, tea.Cmd) { return m.setMotion(motionOff) }})
	}
	items = append(items,
		menuItem{label: "Reload the library", detail: "it reloads itself when Zotero changes it", action: func(m Model) (Model, tea.Cmd) { return m.reload() }},
		menuItem{label: "Help", key: "?", action: func(m Model) (Model, tea.Cmd) { m.showHelp = true; return m, nil }},
		menuItem{label: "Quit", key: "q", action: func(m Model) (Model, tea.Cmd) { return m, tea.Quit }},
	)
	return m.openOverlay(overlay{kind: overlayMenu, title: "Commands", items: items})
}

func (m Model) reload() (Model, tea.Cmd) {
	m.loading = true
	return m, m.loadItems()
}

func (m Model) setMotion(level motionLevel) (Model, tea.Cmd) {
	m.motion = level
	m.startupDone = true
	m.message = "Animation: " + motionNames[level]
	if err := saveMotionLevel(m.config.DataDir, level); err != nil {
		m.message = "Couldn't save the setting"
	}
	return m, m.startRowsIn()
}

func (m Model) startSearch() (Model, tea.Cmd) {
	next, cmd := m.setPage(pageLibrary)
	next.searching = true
	next.sidebarFocused = false
	return next, cmd
}

// startTextSearch searches inside PDFs with Zotero's full-text index.
func (m Model) startTextSearch() (Model, tea.Cmd) {
	next, cmd := m.startSearch()
	next.textScope = true
	next.textMatches = nil
	return next, tea.Batch(cmd, next.scheduleTextSearch())
}

func (m Model) scheduleTextSearch() tea.Cmd {
	if !m.textScope || strings.TrimSpace(m.query) == "" {
		return nil
	}
	query := m.query
	return tea.Tick(textSearchDelay, func(time.Time) tea.Msg { return textSearchMsg{query} })
}

func (m Model) runTextSearch(query string) tea.Cmd {
	dir := m.config.ZoteroDir
	return func() tea.Msg {
		ids, err := zotdb.SearchFullText(dir, strings.Fields(query))
		set := make(map[int]bool, len(ids))
		for _, id := range ids {
			set[id] = true
		}
		return textResultMsg{query, set, err}
	}
}

func (m Model) addPrompt() (Model, tea.Cmd) {
	where := zotdb.LibraryName
	if m.collection != allItems {
		where = m.collectionPath(m.collection)
	}
	return m.openOverlay(overlay{
		kind:  overlayPrompt,
		title: "Add a paper",
		hint:  "Paste an arXiv ID or link, or a DOI. It goes into " + where + ". arXiv papers come with their PDF.",
		onSubmit: func(m Model, value string) (Model, tea.Cmd) {
			if value == "" {
				return m, nil
			}
			target := ""
			if m.collection != allItems {
				target = fmt.Sprintf("C%d", m.collection)
			}
			options := adder.Options{ZoteroDir: m.config.ZoteroDir, Port: m.config.Port, Store: m.config.Store, Target: target}
			m.message = "Looking it up…"
			return m, func() tea.Msg {
				result, err := adder.Add(value, options)
				return addDoneMsg{result, err}
			}
		},
	})
}

func (m Model) editTags() (Model, tea.Cmd) {
	it, ok := m.selected()
	if !ok {
		return m, nil
	}
	return m.openOverlay(overlay{
		kind:  overlayPrompt,
		title: "Tags",
		value: strings.Join(it.Tags, ", "),
		hint:  "Separate tags with commas. Remove a tag by deleting it.",
		onSubmit: func(m Model, value string) (Model, tea.Cmd) {
			var tags []string
			for _, tag := range strings.FieldsFunc(value, func(r rune) bool { return r == ',' || r == ';' }) {
				if tag = strings.TrimSpace(tag); tag != "" && !slices.Contains(tags, tag) {
					tags = append(tags, tag)
				}
			}
			var changes []zotapi.Change
			for _, tag := range tags {
				if !slices.Contains(it.Tags, tag) {
					changes = append(changes, m.change(it, zotapi.AddTag, tag))
				}
			}
			for _, tag := range it.Tags {
				if !slices.Contains(tags, tag) {
					changes = append(changes, m.change(it, zotapi.RemoveTag, tag))
				}
			}
			m = m.updateItem(it.Key, func(item *zotdb.Item) { item.Tags = tags })
			return m.submit(changes)
		},
	})
}

// pickCollection lists collections to file the paper in, or the ones it's in
// to take it out of. Typing narrows the list, which helps with hundreds.
func (m Model) pickCollection(add bool) (Model, tea.Cmd) {
	it, ok := m.selected()
	if !ok {
		return m, nil
	}
	var items []menuItem
	for _, c := range m.collections {
		in := slices.Contains(it.CollectionIDs, c.ID)
		if in == add {
			continue
		}
		c := c
		parent := ""
		if c.ParentID != 0 {
			parent = m.collectionPath(c.ParentID)
		}
		items = append(items, menuItem{label: c.Name, detail: parent, action: func(m Model) (Model, tea.Cmd) {
			path := m.collectionPath(c.ID)
			if add {
				m = m.updateItem(it.Key, func(item *zotdb.Item) {
					item.CollectionIDs = append(slices.Clone(item.CollectionIDs), c.ID)
					item.Collections = append(slices.Clone(item.Collections), path)
				})
				return m.submit([]zotapi.Change{m.change(it, zotapi.AddCollection, c.Key)})
			}
			m = m.updateItem(it.Key, func(item *zotdb.Item) {
				item.CollectionIDs = slices.DeleteFunc(slices.Clone(item.CollectionIDs), func(id int) bool { return id == c.ID })
				item.Collections = slices.DeleteFunc(slices.Clone(item.Collections), func(p string) bool { return p == path })
			})
			return m.submit([]zotapi.Change{m.change(it, zotapi.RemoveCollection, c.Key)})
		}})
	}
	title := "Add to a collection"
	if !add {
		title = "Remove from a collection"
	}
	if len(items) == 0 {
		m.message = "It isn't in any collection"
		return m, nil
	}
	return m.openOverlay(overlay{kind: overlayMenu, title: title, items: items})
}

func (m Model) change(it zotdb.Item, kind, value string) zotapi.Change {
	return zotapi.Change{ItemKey: it.Key, Title: it.Title, Kind: kind, Value: value, Created: time.Now()}
}

// updateItem changes the copy of an item on screen right away. If saving
// fails, the library reloads to show what Zotero really has.
func (m Model) updateItem(key string, f func(*zotdb.Item)) Model {
	items := slices.Clone(m.items)
	for i := range items {
		if items[i].Key == key {
			f(&items[i])
		}
	}
	m.items = items
	m.refreshList()
	return m
}

// submit makes changes in order, in the background.
func (m Model) submit(changes []zotapi.Change) (Model, tea.Cmd) {
	if len(changes) == 0 {
		return m, nil
	}
	client := zotapi.New(m.config.Port, m.config.DataDir)
	queue := zotapi.OpenQueue(m.config.DataDir)
	m.message = "Saving…"
	if !client.HasKey() {
		m.message = "If Zotero asks, choose Always Allow so zotty can save changes"
	}
	return m, func() tea.Msg {
		queued := false
		for _, ch := range changes {
			q, err := zotapi.Submit(client, queue, ch, time.Now())
			if err != nil {
				return writeDoneMsg{err: err}
			}
			queued = queued || q
		}
		return writeDoneMsg{queued: queued}
	}
}

func (m Model) showNotes() (Model, tea.Cmd) {
	it, ok := m.selected()
	if !ok {
		return m, nil
	}
	dir, id, title := m.config.ZoteroDir, it.ID, cleanTitle(it.Title)
	m.message = "Reading notes…"
	return m, func() tea.Msg {
		details, err := zotdb.LoadDetails(dir, id)
		return notesLoadedMsg{title, details, err}
	}
}

func (m Model) notesReader(msg notesLoadedMsg) (Model, tea.Cmd) {
	s := m.style
	inner := m.overlayWidth() - 4
	var lines []string
	wrap := func(text string, style lipgloss.Style, indent string) {
		for _, para := range strings.Split(text, "\n") {
			for _, line := range strings.Split(ansi.Wrap(para, inner-len(indent), " "), "\n") {
				lines = append(lines, style.Render(indent+line))
			}
		}
	}
	if len(msg.details.Notes) > 0 {
		lines = append(lines, s.panelAccent.Render("N O T E S"), "")
		for _, note := range msg.details.Notes {
			wrap(note, s.panelText, "")
			lines = append(lines, "")
		}
	}
	if len(msg.details.Annotations) > 0 {
		lines = append(lines, s.panelAccent.Render("H I G H L I G H T S"), "")
		for _, a := range msg.details.Annotations {
			if a.Page != "" {
				lines = append(lines, s.panelDim.Render("Page "+a.Page))
			}
			if a.Text != "" {
				wrap("“"+a.Text+"”", s.panelText, "  ")
			}
			if a.Comment != "" {
				wrap(a.Comment, s.panelAccent, "  ↳ ")
			}
			lines = append(lines, "")
		}
	}
	if len(lines) == 0 {
		lines = []string{s.panelDim.Render("No notes or highlights yet. Highlights you make in Zotero's reader show up here.")}
	}
	m.message = ""
	return m.openOverlay(overlay{kind: overlayReader, title: ansi.Truncate(msg.title, m.overlayWidth()-8, "…"), lines: lines})
}

func (m Model) importInbox() (Model, tea.Cmd) {
	if len(m.entries) == 0 {
		m.message = "The inbox is empty"
		return m, nil
	}
	client := importer.NewClient(m.config.Port)
	store, dir := m.config.Store, m.config.ZoteroDir
	m.message = "Importing…"
	return m, func() tea.Msg {
		if err := client.Ping(); err != nil {
			return importDoneMsg{err: errors.New("open Zotero first. The inbox goes in by itself when it opens")}
		}
		result, err := importer.Run(client, store, zotdb.UserLibraryTarget(dir), func(string, ...any) {})
		return importDoneMsg{result, err}
	}
}

// showInZotero is kept separate from openFile's error message, which talks
// about PDFs.
func showInZotero(key string) tea.Cmd {
	return func() tea.Msg {
		exec.Command("open", "zotero://select/library/items/"+key).Run()
		return nil
	}
}

// handleActionMsg handles the results of background actions. It reports
// whether msg was one.
func (m Model) handleActionMsg(msg tea.Msg) (Model, tea.Cmd, bool) {
	switch msg := msg.(type) {
	case writeDoneMsg:
		switch {
		case msg.err != nil:
			m.message = "Not saved: " + msg.err.Error()
			next, cmd := m.reload()
			return next, cmd, true
		case msg.queued:
			m.message = "Zotero is closed, so this is saved when it opens"
		default:
			m.message = "Saved in Zotero"
		}
		return m, nil, true

	case addDoneMsg:
		var dup adder.ErrDuplicate
		switch {
		case errors.As(msg.err, &dup):
			m.message = "Already in your library: " + dup.Title
			return m, nil, true
		case msg.err != nil:
			m.message = "Not added: " + msg.err.Error()
			return m, m.loadInbox(), true
		}
		note := ""
		if msg.result.PDFError != nil {
			note = ", without a PDF"
		}
		m.message = "Added " + ansi.Truncate(msg.result.Title, 50, "…") + note
		if !msg.result.Imported {
			m.message = "In the inbox until Zotero opens: " + ansi.Truncate(msg.result.Title, 40, "…") + note
		}
		next, cmd := m.reload()
		return next, tea.Batch(cmd, next.loadInbox()), true

	case importDoneMsg:
		if msg.err != nil {
			m.message = "Not imported: " + msg.err.Error()
			return m, nil, true
		}
		m.message = fmt.Sprintf("Imported %d into Zotero", msg.result.Imported)
		if msg.result.Failed > 0 {
			m.message += fmt.Sprintf(", %d failed and wait in the inbox", msg.result.Failed)
		}
		next, cmd := m.reload()
		return next, tea.Batch(cmd, next.loadInbox()), true

	case notesLoadedMsg:
		if msg.err != nil {
			m.message = "Couldn't read notes: " + msg.err.Error()
			return m, nil, true
		}
		next, cmd := m.notesReader(msg)
		return next, cmd, true

	case textSearchMsg:
		if !m.textScope || msg.query != m.query {
			return m, nil, true
		}
		return m, m.runTextSearch(msg.query), true

	case textResultMsg:
		if !m.textScope || msg.query != m.query {
			return m, nil, true
		}
		if msg.err != nil {
			m.message = msg.err.Error()
		}
		m.textMatches = msg.ids
		m.refreshList()
		return m, m.selectionChanged(), true

	case toastExpiredMsg:
		if msg.at.Equal(m.messageAt) {
			m.message = ""
		}
		return m, nil, true
	}
	return m, nil, false
}
