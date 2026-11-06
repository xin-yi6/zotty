// Package tui is the full-screen interface: the library, the reading queue, and
// the Chrome inbox, usable with the keyboard or the mouse.
package tui

import (
	"fmt"
	"os"
	"os/exec"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/xin-yi6/zotty/internal/inbox"
	"github.com/xin-yi6/zotty/internal/zotapi"
	"github.com/xin-yi6/zotty/internal/zotdb"
)

const (
	doubleClickWindow = 400 * time.Millisecond
	selectionSettle   = 150 * time.Millisecond
	inboxRefresh      = 2 * time.Second
	wheelStep         = 3
)

type page int

const (
	pageLibrary page = iota
	pageQueue
	pageInbox
	pageCount
)

var pageNames = [pageCount]string{"Library", "Queue", "Inbox"}

// Config says where the interface finds its data.
type Config struct {
	ZoteroDir string
	DataDir   string
	Store     *inbox.Store
	// Port is where Zotero takes changes.
	Port int
}

// Run shows the interface until the user quits.
func Run(c Config) error {
	_, err := tea.NewProgram(New(c)).Run()
	return err
}

type (
	itemsLoadedMsg struct {
		stamp       string
		items       []zotdb.Item
		collections []zotdb.Collection
		err         error
	}
	inboxLoadedMsg struct{ entries []inbox.Entry }
	// watchMsg reports that the library or the inbox changed on disk.
	watchMsg struct {
		stamp, inbox string
		entries      []inbox.Entry
	}
	thumbWantedMsg struct{ key string }
	thumbReadyMsg  struct {
		key   string
		thumb thumbnail
		err   error
	}
	detailsLoadedMsg struct {
		key     string
		details zotdb.Details
	}
	openedMsg struct{ err error }
)

// listPosition is the cursor and scroll position of one list.
type listPosition struct {
	cursor, offset int
}

// region is a clickable area of the screen, recorded while drawing so clicks
// always match what is actually on screen.
type region struct {
	y, x0, x1 int
	action    func(Model) (Model, tea.Cmd)
}

// Model is the interface state.
type Model struct {
	config        Config
	width, height int

	loading bool
	loadErr error
	items   []zotdb.Item
	entries []inbox.Entry
	// groupCounts counts the queue groups within the chosen collection.
	groupCounts map[string]int
	visible     []zotdb.Item
	page        page
	group       int
	positions   [pageCount + 4]listPosition

	collections    []zotdb.Collection
	collection     int
	expanded       map[int]bool
	sidebarOpen    bool
	sidebarFocused bool
	sidebarChosen  bool
	sideCursor     int
	sideOffset     int
	// treeQuery narrows the tree to collections whose names contain it.
	treeQuery     string
	treeSearching bool

	query     string
	searching bool
	showHelp  bool
	showPanel bool
	message   string

	themeIndex int
	style      styles
	sortBy     int
	// compact drops the blank line between papers.
	compact bool
	motion  motionLevel

	// Animation state. Each effect remembers when it began.
	now         time.Time
	nowFunc     func() time.Time
	animUntil   time.Time
	ticking     bool
	startedAt   time.Time
	startupDone bool
	rowsAt      time.Time
	bandAt      time.Time
	bandFrom    int
	flipAt      time.Time
	flipFrom    []string
	shownThumb  string
	detailAt    time.Time
	messageAt   time.Time

	over   overlay
	overAt time.Time

	// textScope searches inside PDFs; textMatches is nil while it runs.
	textScope   bool
	textMatches map[int]bool

	thumbs  map[string]thumbnail
	noThumb map[string]bool
	details map[string]zotdb.Details

	confirmRemove string

	// stamp marks the version of the library on screen.
	stamp string

	lastClickRow int
	lastClickAt  time.Time

	regions *[]region
}

// New returns the initial model.
func New(c Config) Model {
	index := loadTheme(c.DataDir)
	level := loadMotionLevel(c.DataDir)
	return Model{
		config:       c,
		loading:      true,
		groupCounts:  map[string]int{},
		expanded:     map[int]bool{},
		themeIndex:   index,
		sortBy:       sortIndex(loadSetting(c.DataDir, "sort")),
		compact:      loadSetting(c.DataDir, "rows") == "packed",
		motion:       level,
		startedAt:    time.Now(),
		startupDone:  level != motionFull,
		ticking:      level == motionFull,
		animUntil:    time.Now().Add(startupLength),
		bandFrom:     -1,
		style:        newStyles(themes[index]),
		thumbs:       map[string]thumbnail{},
		noThumb:      map[string]bool{},
		details:      map[string]zotdb.Details{},
		lastClickRow: -1,
		regions:      &[]region{},
	}
}

// Init starts loading the library in the background so the screen appears at once.
func (m Model) Init() tea.Cmd {
	cmds := []tea.Cmd{m.loadItems(), m.loadInbox(), m.watch("", "")}
	if m.ticking {
		cmds = append(cmds, frameTick())
	}
	return tea.Batch(cmds...)
}

func (m Model) loadItems() tea.Cmd {
	dir := m.config.ZoteroDir
	return func() tea.Msg {
		// The stamp is taken first, so a write during the load shows up next time.
		stamp := zotdb.Stamp(dir)
		items, err := zotdb.LoadItems(dir)
		if err != nil {
			return itemsLoadedMsg{stamp: stamp, err: err}
		}
		// The tree is a convenience: without it the library still works.
		collections, _ := zotdb.LoadCollections(dir)
		return itemsLoadedMsg{stamp: stamp, items: items, collections: collections}
	}
}

func (m Model) loadInbox() tea.Cmd {
	store := m.config.Store
	return func() tea.Msg {
		entries, err := store.List()
		if err != nil {
			return inboxLoadedMsg{}
		}
		return inboxLoadedMsg{entries}
	}
}

// watch looks at the library and the inbox every few seconds, and wakes the
// program only when one of them has changed, so a still screen isn't redrawn.
func (m Model) watch(stamp, sig string) tea.Cmd {
	dir, store := m.config.ZoteroDir, m.config.Store
	return func() tea.Msg {
		for {
			time.Sleep(inboxRefresh)
			entries, err := store.List()
			now := zotdb.Stamp(dir)
			if err == nil && inboxSignature(entries) != sig || now != stamp {
				if err != nil {
					entries = nil
				}
				return watchMsg{stamp: now, inbox: inboxSignature(entries), entries: entries}
			}
		}
	}
}

func inboxSignature(entries []inbox.Entry) string {
	var b strings.Builder
	for _, e := range entries {
		fmt.Fprintf(&b, "%s %d %s;", e.ID, len(e.Attachments), e.ImportError)
	}
	return b.String()
}

// position returns the scroll state of the current list. Each page, and each
// queue group, keeps its own place.
func (m *Model) position() *listPosition {
	switch m.page {
	case pageLibrary:
		return &m.positions[0]
	case pageInbox:
		return &m.positions[inboxPosition]
	}
	return &m.positions[2+m.group]
}

const inboxPosition = 1

// inCollection returns the items in the chosen collection, or all of them.
func (m *Model) inCollection() []zotdb.Item {
	if m.collection == allItems {
		return m.items
	}
	var out []zotdb.Item
	for _, it := range m.items {
		for _, id := range it.CollectionIDs {
			if id == m.collection {
				out = append(out, it)
				break
			}
		}
	}
	return out
}

func (m *Model) refreshList() {
	base := m.inCollection()
	m.groupCounts = zotdb.CountGroups(base)
	switch m.page {
	case pageLibrary:
		if m.textScope && strings.TrimSpace(m.query) != "" {
			m.visible = nil
			for _, it := range base {
				if m.textMatches[it.ID] {
					m.visible = append(m.visible, it)
				}
			}
			break
		}
		if strings.TrimSpace(m.query) == "" {
			m.visible = m.sorted(base)
			break
		}
		m.visible = zotdb.Filter(base, m.query)
	case pageQueue:
		m.visible = m.sorted(zotdb.InGroup(base, zotdb.QueueGroups[m.group]))
	default:
		m.visible = nil
	}
	m.clampCursor()
}

func (m *Model) listLength() int {
	if m.page == pageInbox {
		return len(m.entries)
	}
	return len(m.visible)
}

func (m *Model) clampCursor() {
	p := m.position()
	length := m.listLength()
	p.cursor = min(p.cursor, length-1)
	p.cursor = max(p.cursor, 0)
	rows := m.pageItems()
	if p.cursor < p.offset {
		p.offset = p.cursor
	}
	if rows > 0 && p.cursor >= p.offset+rows {
		p.offset = p.cursor - rows + 1
	}
	p.offset = min(p.offset, max(length-rows, 0))
	p.offset = max(p.offset, 0)
}

func (m Model) selected() (zotdb.Item, bool) {
	if m.page == pageInbox || len(m.visible) == 0 {
		return zotdb.Item{}, false
	}
	p := m.position()
	if p.cursor >= len(m.visible) {
		return zotdb.Item{}, false
	}
	return m.visible[p.cursor], true
}

func (m Model) selectedEntry() (inbox.Entry, bool) {
	if m.page != pageInbox || len(m.entries) == 0 {
		return inbox.Entry{}, false
	}
	p := m.position()
	if p.cursor >= len(m.entries) {
		return inbox.Entry{}, false
	}
	return m.entries[p.cursor], true
}

// selectionChanged asks for the selected paper's extras once the cursor stops,
// so holding j doesn't start a render for every row it passes.
func (m Model) selectionChanged() tea.Cmd {
	it, ok := m.selected()
	if !ok || m.hasThumbState(it.Key) {
		return nil
	}
	key := it.Key
	return tea.Tick(selectionSettle, func(time.Time) tea.Msg { return thumbWantedMsg{key} })
}

func (m Model) hasThumbState(key string) bool {
	_, done := m.thumbs[key]
	return done || m.noThumb[key]
}

func (m Model) move(delta int) (Model, tea.Cmd) {
	p := m.position()
	p.cursor += delta
	m.clampCursor()
	return m, m.selectionChanged()
}

func (m Model) moveTo(index int) (Model, tea.Cmd) {
	p := m.position()
	p.cursor = index
	m.clampCursor()
	return m, m.selectionChanged()
}

func (m Model) setPage(next page) (Model, tea.Cmd) {
	m.page = next
	m.searching = false
	m.refreshList()
	return m.listChanged()
}

func (m Model) setGroup(group int) (Model, tea.Cmd) {
	m.group = group
	m.page = pageQueue
	m.refreshList()
	return m.listChanged()
}

// listChanged plays the row-by-row entrance for a new list.
func (m Model) listChanged() (Model, tea.Cmd) {
	rows := m.startRowsIn()
	return m, tea.Batch(m.selectionChanged(), rows)
}

func (m Model) setTheme(index int) (Model, tea.Cmd) {
	m.themeIndex = index
	m.style = newStyles(themes[index])
	m.message = themes[index].name
	if err := saveTheme(m.config.DataDir, index); err != nil {
		m.message = "Couldn't save the theme"
	}
	return m, nil
}

func (m Model) openSelected() (Model, tea.Cmd) {
	if m.page == pageInbox {
		entry, ok := m.selectedEntry()
		if !ok {
			return m, nil
		}
		for _, a := range entry.Attachments {
			if a.ContentType != "text/html" {
				return m, openFile(m.config.Store.Path(entry.ID, a.File))
			}
		}
		m.message = "This save has no PDF"
		return m, nil
	}
	it, ok := m.selected()
	if !ok {
		return m, nil
	}
	if it.PDF == "" {
		m.message = "This paper has no PDF"
		return m, nil
	}
	if _, err := os.Stat(it.PDF); err != nil {
		m.message = "The PDF file is missing"
		return m, nil
	}
	m.message = "Opening PDF"
	return m, openFile(it.PDF)
}

func openFile(path string) tea.Cmd {
	return func() tea.Msg {
		return openedMsg{exec.Command("open", path).Run()}
	}
}

// update handles one event; Update wraps it with the animations events start.
func (m Model) update(msg tea.Msg) (Model, tea.Cmd) {
	if next, cmd, ok := m.handleActionMsg(msg); ok {
		return next, cmd
	}
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width, m.height = msg.Width, msg.Height
		if !m.sidebarChosen {
			// Until the user opens or closes the tree, it follows the window:
			// open when there's room beside the list and panel.
			m.sidebarOpen = m.width >= sidebarAutoWidth
			m.sidebarFocused = m.sidebarFocused && m.sidebarOpen
		}
		m.clampCursor()
		m.clampSidebar()
		return m, nil

	case itemsLoadedMsg:
		// A reload keeps the same paper selected, even if it moved.
		kept, hadSelection := m.selected()
		m.loading = false
		m.loadErr = msg.err
		m.stamp = msg.stamp
		m.items = msg.items
		m.collections = msg.collections
		m.refreshList()
		if hadSelection {
			for i, it := range m.visible {
				if it.Key == kept.Key {
					m.position().cursor = i
					m.clampCursor()
					break
				}
			}
		}
		m.clampSidebar()
		return m, m.selectionChanged()

	case inboxLoadedMsg:
		m.entries = msg.entries
		m.clampCursor()
		return m, nil

	case watchMsg:
		m.entries = msg.entries
		m.clampCursor()
		watch := m.watch(msg.stamp, msg.inbox)
		if m.loading || m.stamp == "" || msg.stamp == m.stamp {
			return m, watch
		}
		next, cmd := m.reload()
		return next, tea.Batch(cmd, watch)

	case thumbWantedMsg:
		return m, m.loadExtras(msg.key)

	case thumbReadyMsg:
		if msg.err != nil {
			m.noThumb[msg.key] = true
		} else {
			m.thumbs[msg.key] = msg.thumb
		}
		return m, nil

	case detailsLoadedMsg:
		m.details[msg.key] = msg.details
		return m, nil

	case tea.PasteMsg:
		switch {
		case m.over.kind != overlayNone:
			m = m.handleOverlayPaste(msg.Content)
		case m.searching:
			m.query += strings.Join(strings.Fields(msg.Content), " ")
			m.refreshList()
			return m, tea.Batch(m.selectionChanged(), m.scheduleTextSearch())
		}
		return m, nil

	case openedMsg:
		m.message = ""
		if msg.err != nil {
			m.message = "Couldn't open the PDF"
		}
		return m, nil

	case tea.KeyPressMsg:
		return m.handleKey(msg)

	case tea.MouseClickMsg:
		return m.handleClick(msg.Mouse())

	case tea.MouseWheelMsg:
		mouse := msg.Mouse()
		step := 0
		switch mouse.Button {
		case tea.MouseWheelUp:
			step = -wheelStep
		case tea.MouseWheelDown:
			step = wheelStep
		}
		if step != 0 && mouse.X < m.sidebarWidth() {
			m.sideOffset += step
			m.sideCursor = max(min(m.sideCursor, m.sideOffset+m.sidebarRows()-1), m.sideOffset)
			m.clampSidebar()
			return m, nil
		}
		if step != 0 {
			return m.move(step)
		}
	}
	return m, nil
}

// loadExtras renders the thumbnail for key if it's still the selected paper.
func (m Model) loadExtras(key string) tea.Cmd {
	it, ok := m.selected()
	if !ok || it.Key != key || m.hasThumbState(key) {
		return nil
	}
	if it.PDF == "" {
		return func() tea.Msg { return thumbReadyMsg{key: key, err: fmt.Errorf("no PDF")} }
	}
	pdf := it.PDF
	return func() tea.Msg {
		thumb, err := renderThumbnail(pdf)
		return thumbReadyMsg{key, thumb, err}
	}
}

func (m Model) handleKey(msg tea.KeyPressMsg) (Model, tea.Cmd) {
	key := msg.String()
	if key == "ctrl+c" {
		return m, tea.Quit
	}
	if m.over.kind != overlayNone {
		return m.handleOverlayKey(msg)
	}
	if m.showHelp {
		// Any key leaves help, and keys other than these also do their job,
		// so pressing T after reading about it changes the theme.
		m.showHelp = false
		if key == "?" || key == "esc" || key == "q" || key == "enter" {
			return m, nil
		}
	}
	if m.searching {
		return m.handleSearchKey(msg)
	}
	m.message = ""
	if key != "x" {
		m.confirmRemove = ""
	}
	if m.sidebarFocused && m.sidebarShown() {
		if next, cmd, handled := m.handleSidebarKey(msg); handled {
			return next, cmd
		}
	}

	switch key {
	case "q":
		return m, tea.Quit
	case "?":
		m.showHelp = true
	case "down":
		return m.move(1)
	case "up":
		return m.move(-1)
	case "pgdown":
		return m.move(max(m.pageItems()/2, 1))
	case "pgup":
		return m.move(-max(m.pageItems()/2, 1))
	case "home":
		return m.moveTo(0)
	case "end":
		return m.moveTo(m.listLength() - 1)
	case "enter":
		return m.openSelected()
	case "o":
		return m.sortMenu()
	case "/":
		return m.startSearch()
	case "esc":
		if m.query != "" || m.textScope {
			m.query = ""
			m.textScope = false
			m.refreshList()
			return m, m.selectionChanged()
		}
	case "space", " ":
		return m.paperMenu()
	case ":":
		return m.palette()
	case "a":
		return m.addPrompt()
	case "e":
		return m.editTags()
	case "+", "=":
		return m.pickCollection(true)
	case "-":
		return m.pickCollection(false)
	case "n":
		return m.showNotes()
	case "tab":
		return m.setPage((m.page + 1) % pageCount)
	case "shift+tab":
		return m.setPage((m.page + pageCount - 1) % pageCount)
	case "left":
		// Left walks back through the queue's groups, then into the tree.
		if m.page == pageQueue && m.group > 0 {
			return m.setGroup(m.group - 1)
		}
		if m.page != pageInbox {
			return m.focusSidebar()
		}
	case "right":
		if m.page == pageQueue && m.group < len(zotdb.QueueGroups)-1 {
			return m.setGroup(m.group + 1)
		}
	case "d":
		m.showPanel = !m.showPanel
		m.clampCursor()
	case "s":
		it, ok := m.selected()
		if !ok {
			return m, nil
		}
		return m.setStatus(nextStatus(it.Status))
	case "x":
		return m.removeEntry()
	case "c":
		if m.page == pageInbox {
			return m, nil
		}
		return m.toggleSidebar()
	case "t", "T":
		return m.setTheme((m.themeIndex + 1) % len(themes))
	case "m", "M":
		return m.setMotion((m.motion + 1) % motionLevel(len(motionNames)))
	}
	return m, nil
}

func (m Model) handleSearchKey(msg tea.KeyPressMsg) (Model, tea.Cmd) {
	switch msg.String() {
	case "esc":
		m.searching = false
		m.query = ""
		m.textScope = false
	case "enter":
		// Leave the box and keep the results, so the arrows move through them.
		m.searching = false
		return m, m.selectionChanged()
	case "up":
		return m.move(-1)
	case "down":
		return m.move(1)
	case "pgup":
		return m.move(-max(m.pageItems()/2, 1))
	case "pgdown":
		return m.move(max(m.pageItems()/2, 1))
	case "tab":
		// Switch between searching the library and searching inside PDFs.
		m.textScope = !m.textScope
		m.textMatches = nil
	case "backspace":
		if runes := []rune(m.query); len(runes) > 0 {
			m.query = string(runes[:len(runes)-1])
		}
		m.textMatches = nil
	default:
		if msg.Text == "" {
			return m, nil
		}
		m.query += msg.Text
		m.textMatches = nil
	}
	m.position().cursor = 0
	m.position().offset = 0
	m.refreshList()
	return m, tea.Batch(m.selectionChanged(), m.scheduleTextSearch())
}

func (m Model) handleClick(mouse tea.Mouse) (Model, tea.Cmd) {
	if mouse.Button != tea.MouseLeft {
		return m, nil
	}
	if m.showHelp {
		m.showHelp = false
		return m, nil
	}
	for _, r := range *m.regions {
		if mouse.Y == r.y && mouse.X >= r.x0 && mouse.X < r.x1 {
			return r.action(m)
		}
	}
	return m, nil
}

// clickRow selects a row, and opens it when it's the second click in a row.
func (m Model) clickRow(index int) (Model, tea.Cmd) {
	m.sidebarFocused = false
	now := time.Now()
	double := index == m.lastClickRow && now.Sub(m.lastClickAt) < doubleClickWindow
	m.lastClickRow = index
	m.lastClickAt = now
	next, cmd := m.moveTo(index)
	if double {
		next.lastClickRow = -1
		return next.openSelected()
	}
	return next, cmd
}

func shortAuthors(creators []string) string {
	switch len(creators) {
	case 0:
		return ""
	case 1:
		return creators[0]
	case 2:
		return creators[0] + " and " + creators[1]
	}
	return creators[0] + " et al."
}

// cleanTitle removes the HTML and LaTeX that titles often carry, so
// "$$ \\mathcal{N} $$ = 4" reads as "N = 4".
func cleanTitle(title string) string {
	title = titleMarkup.ReplaceAllString(title, "")
	title = latexCommand.ReplaceAllString(title, "$1")
	title = strings.ReplaceAll(title, "$", "")
	return strings.Join(strings.Fields(title), " ")
}

// nextStatus is the state after current when pressing s.
func nextStatus(current string) string {
	for i, status := range zotapi.Statuses {
		if status == current {
			return zotapi.Statuses[(i+1)%len(zotapi.Statuses)]
		}
	}
	if zotdb.GroupOf(current) == zotdb.GroupRead {
		return "New"
	}
	return zotapi.Statuses[0]
}

// setStatus shows the new state at once and saves it in the background.
func (m Model) setStatus(status string) (Model, tea.Cmd) {
	it, ok := m.selected()
	if !ok || it.Status == status {
		return m, nil
	}
	m = m.updateItem(it.Key, func(item *zotdb.Item) { item.Status = status })
	return m.submit([]zotapi.Change{m.change(it, zotapi.SetStatus, status)})
}

// removeEntry deletes an inbox entry after a second press of x, since the
// paper isn't in Zotero yet and would be gone for good.
func (m Model) removeEntry() (Model, tea.Cmd) {
	entry, ok := m.selectedEntry()
	if !ok {
		return m, nil
	}
	if m.confirmRemove != entry.ID {
		m.confirmRemove = entry.ID
		m.message = "Press x again to remove this paper from the inbox"
		return m, nil
	}
	m.confirmRemove = ""
	if err := m.config.Store.Delete(entry.ID); err != nil {
		m.message = "Couldn't remove it: " + err.Error()
		return m, nil
	}
	m.message = "Removed from the inbox"
	return m, m.loadInbox()
}

// Update handles one event, then starts the animations it calls for: the
// selection band and page flip when the paper changes, and a toast when a
// message appears.
func (m Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	if frame, ok := msg.(frameMsg); ok {
		return m.onFrame(time.Time(frame))
	}
	if m.startupActive() {
		switch msg.(type) {
		case tea.KeyPressMsg, tea.MouseClickMsg:
			// Any key or click skips the opening.
			m.now = m.timeNow()
			m.startupDone = true
			return m, m.startRowsIn()
		}
	}

	m.now = m.timeNow()
	before, _ := m.selected()
	page, cursor, message := m.page, m.position().cursor, m.message
	next, cmd := m.update(msg)
	cmds := []tea.Cmd{cmd}

	after, _ := next.selected()
	if after.Key != before.Key {
		if next.page == page && next.position().cursor != cursor {
			next.bandFrom = cursor
			next.bandAt = next.clock()
			cmds = append(cmds, next.animate(bandLength))
		}
		next.detailAt = next.clock()
		cmds = append(cmds, next.animate(fadeLength))
	}
	cmds = append(cmds, next.flipToSelected())
	if next.message != message && next.message != "" {
		next.messageAt = next.clock()
		at := next.messageAt
		cmds = append(cmds, next.animate(toastLength),
			tea.Tick(toastLife, func(time.Time) tea.Msg { return toastExpiredMsg{at} }))
	}
	return next, tea.Batch(cmds...)
}

// flipToSelected turns the page over once the selected paper's thumbnail is
// ready and differs from the one showing.
func (m *Model) flipToSelected() tea.Cmd {
	it, ok := m.selected()
	if !ok {
		return nil
	}
	thumb, ready := m.thumbs[it.Key]
	if !ready || it.Key == m.shownThumb {
		return nil
	}
	m.flipFrom = nil
	if old, ok := m.thumbs[m.shownThumb]; ok {
		m.flipFrom = old.lines(themes[m.themeIndex])
	}
	_ = thumb
	m.shownThumb = it.Key
	m.flipAt = m.clock()
	return m.animate(flipLength)
}

func (m Model) onFrame(t time.Time) (tea.Model, tea.Cmd) {
	m.now = t
	var cmds []tea.Cmd
	if m.startupActive() && t.Sub(m.startedAt) >= startupLength && !m.loading {
		m.startupDone = true
		cmds = append(cmds, m.startRowsIn())
	}
	if t.Before(m.animUntil) || m.startupActive() {
		cmds = append(cmds, frameTick())
		return m, tea.Batch(cmds...)
	}
	m.ticking = false
	return m, tea.Batch(cmds...)
}
