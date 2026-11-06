package tui

import (
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/xin-yi6/zotty/internal/inbox"
	"github.com/xin-yi6/zotty/internal/zotapi"
	"github.com/xin-yi6/zotty/internal/zotdb"
)

func sampleItems() []zotdb.Item {
	statuses := []string{"New", "In Progress", "Read", "To Read", "Read, useful", ""}
	items := []zotdb.Item{{
		ID: 1000, Key: "CJKTITLE", Title: "全息量子色动力学的相结构研究，一个很长很长很长很长很长很长很长的中文标题",
		Creators: []string{"王"}, Year: "2026", Status: "To Read",
		Abstract: strings.Repeat("A long abstract about holography. ", 30),
	}}
	for i := range 40 {
		items = append(items, zotdb.Item{
			ID: i, Key: fmt.Sprintf("KEY%05d", i), Title: fmt.Sprintf("Holographic paper %d", i),
			Creators: []string{"Jean-Luc Picard", "William Riker", "Deanna Troi"}, Surnames: []string{"Picard", "Riker", "Troi"},
			Year: "2025", Status: statuses[i%len(statuses)],
			Tags: []string{"QCD"}, Abstract: "An abstract.",
		})
	}
	return items
}

func newModel(t *testing.T, width, height int) Model {
	t.Helper()
	store, err := inbox.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	m := New(Config{DataDir: t.TempDir(), Store: store})
	// Most tests check layout and behavior, so they run without animation;
	// the motion tests turn it on with a clock they control.
	m.motion, m.startupDone, m.ticking, m.animUntil = motionOff, true, false, time.Time{}
	m.compact = true
	m = send(t, m, tea.WindowSizeMsg{Width: width, Height: height})
	m = send(t, m, itemsLoadedMsg{items: sampleItems()})
	m.render()
	return m
}

func send(t *testing.T, m Model, msg tea.Msg) Model {
	t.Helper()
	next, _ := m.Update(msg)
	return next.(Model)
}

func press(t *testing.T, m Model, key string) Model {
	t.Helper()
	msg := tea.KeyPressMsg{Text: key}
	if len([]rune(key)) == 1 {
		msg.Code = []rune(key)[0]
	}
	switch key {
	case "tab":
		msg = tea.KeyPressMsg{Code: tea.KeyTab}
	case "esc":
		msg = tea.KeyPressMsg{Code: tea.KeyEscape}
	case "enter":
		msg = tea.KeyPressMsg{Code: tea.KeyEnter}
	}
	m = send(t, m, msg)
	m.render()
	return m
}

// showPage presses tab until the page shows.
func showPage(t *testing.T, m Model, p page) Model {
	t.Helper()
	for range pageCount {
		if m.page == p {
			break
		}
		m = press(t, m, "tab")
	}
	return m
}

func click(t *testing.T, m Model, x, y int) Model {
	t.Helper()
	m = send(t, m, tea.MouseClickMsg{X: x, Y: y, Button: tea.MouseLeft})
	m.render()
	return m
}

func screen(m Model) []string { return strings.Split(ansi.Strip(m.render()), "\n") }

func TestEveryLineFitsTheWindow(t *testing.T) {
	for _, size := range [][2]int{{60, 16}, {80, 24}, {100, 30}, {120, 40}, {200, 60}} {
		m := newModel(t, size[0], size[1])
		for _, p := range []page{pageLibrary, pageQueue, pageInbox} {
			next, _ := m.setPage(p)
			lines := strings.Split(next.render(), "\n")
			if len(lines) != size[1] {
				t.Errorf("%dx%d page %d: %d lines", size[0], size[1], p, len(lines))
			}
			for i, line := range lines {
				if w := lipgloss.Width(line); w != size[0] {
					t.Errorf("%dx%d page %d line %d: width %d", size[0], size[1], p, i, w)
				}
			}
		}
	}
}

func TestKeyboardMovesAndScrolls(t *testing.T) {
	m := newModel(t, 120, 30)
	for range 30 {
		m = arrow(t, m, tea.KeyDown)
	}
	if got := m.position().cursor; got != 30 {
		t.Fatalf("cursor = %d", got)
	}
	if !strings.Contains(strings.Join(screen(m), "\n"), "Holographic paper 29") {
		t.Error("selected row scrolled out of view")
	}
	m = arrow(t, m, tea.KeyHome)
	if m.position().cursor != 0 || m.position().offset != 0 {
		t.Errorf("position = %+v", *m.position())
	}
	m = arrow(t, m, tea.KeyEnd)
	if m.position().cursor != len(m.visible)-1 {
		t.Errorf("cursor = %d", m.position().cursor)
	}
}

func TestLastRowIsVisibleAndClickable(t *testing.T) {
	m := newModel(t, 120, 30)
	m = arrow(t, m, tea.KeyEnd)
	rendered := strings.Join(screen(m), "\n")
	if !strings.Contains(rendered, "Holographic paper 39") {
		t.Fatal("last row hidden")
	}
	lines := screen(m)
	for y, line := range lines {
		if strings.Contains(line, "Holographic paper 38") {
			m = click(t, m, 10, y)
			if m.position().cursor != 39 {
				t.Errorf("clicked row selected %d", m.position().cursor)
			}
			return
		}
	}
	t.Fatal("row not on screen")
}

func TestClickSelectsTheRowUnderTheMouse(t *testing.T) {
	m := newModel(t, 120, 30)
	for y, line := range screen(m) {
		if strings.Contains(line, "Holographic paper 4 ") {
			m = click(t, m, 10, y)
			it, ok := m.selected()
			if !ok || it.Key != "KEY00004" {
				t.Fatalf("selected %+v", it)
			}
			return
		}
	}
	t.Fatal("row not on screen")
}

func TestClickingATabChangesPage(t *testing.T) {
	m := newModel(t, 120, 30)
	x := strings.Index(screen(m)[0], "Queue")
	m = click(t, m, x+1, 0)
	if m.page != pageQueue {
		t.Fatalf("page = %d", m.page)
	}
	if !strings.Contains(strings.Join(screen(m), "\n"), "In progress") {
		t.Error("queue chips not shown")
	}
}

func TestClickingAGroupChip(t *testing.T) {
	m := newModel(t, 120, 30)
	m = showPage(t, m, pageQueue)
	x, y := rowY(t, m, "○ New")
	m = click(t, m, x+1, y)
	if zotdb.QueueGroups[m.group] != zotdb.GroupNew {
		t.Fatalf("group = %d", m.group)
	}
	for _, it := range m.visible {
		if it.Status != "New" {
			t.Fatalf("visible = %+v", it)
		}
	}
}

func TestDoubleClickOpens(t *testing.T) {
	m := newModel(t, 120, 30)
	y := -1
	for i, line := range screen(m) {
		if strings.Contains(line, "Holographic paper 2 ") {
			y = i
		}
	}
	m = click(t, m, 10, y)
	if m.message != "" {
		t.Fatalf("message after one click: %q", m.message)
	}
	m = click(t, m, 10, y)
	if m.message != "This paper has no PDF" {
		t.Errorf("message = %q", m.message)
	}
}

func TestSlowClicksDoNotOpen(t *testing.T) {
	m := newModel(t, 120, 30)
	y := -1
	for i, line := range screen(m) {
		if strings.Contains(line, "Holographic paper 2 ") {
			y = i
		}
	}
	m = click(t, m, 10, y)
	m.lastClickAt = time.Now().Add(-time.Second)
	m = click(t, m, 10, y)
	if m.message != "" {
		t.Errorf("message = %q", m.message)
	}
}

func TestWheelScrolls(t *testing.T) {
	m := newModel(t, 120, 30)
	m = send(t, m, tea.MouseWheelMsg{Button: tea.MouseWheelDown})
	if m.position().cursor != wheelStep {
		t.Errorf("cursor = %d", m.position().cursor)
	}
}

func TestOpenButton(t *testing.T) {
	m := newModel(t, 120, 30)
	m = arrow(t, m, tea.KeyDown)
	for y, line := range screen(m) {
		if x := strings.Index(line, " Open "); x >= 0 {
			m = click(t, m, m.tableWidth()+2, y)
			if m.message != "This paper has no PDF" {
				t.Errorf("message = %q", m.message)
			}
			return
		}
	}
	t.Fatal("Open button not on screen")
}

func TestSearchFiltersAsYouType(t *testing.T) {
	m := newModel(t, 120, 30)
	m = press(t, m, "/")
	for _, r := range "paper 39" {
		m = press(t, m, string(r))
	}
	if len(m.visible) != 1 || m.visible[0].Key != "KEY00039" {
		t.Fatalf("visible = %d", len(m.visible))
	}
	m = press(t, m, "esc")
	if m.searching || m.query != "" || len(m.visible) != len(m.items) {
		t.Errorf("searching=%v query=%q", m.searching, m.query)
	}
}

func TestSearchKeepsKeysFromActing(t *testing.T) {
	m := newModel(t, 120, 30)
	m = press(t, m, "/")
	m = press(t, m, "q")
	if m.query != "q" {
		t.Errorf("query = %q", m.query)
	}
}

func TestPagesKeepTheirPlace(t *testing.T) {
	m := newModel(t, 120, 30)
	m = arrow(t, m, tea.KeyDown)
	m = arrow(t, m, tea.KeyDown)
	m = showPage(t, m, pageQueue)
	m = arrow(t, m, tea.KeyDown)
	m = showPage(t, m, pageLibrary)
	if m.position().cursor != 2 {
		t.Errorf("cursor = %d", m.position().cursor)
	}
}

func TestNarrowWindowHidesPanelUntilAsked(t *testing.T) {
	m := newModel(t, 80, 24)
	joined := strings.Join(screen(m), "\n")
	if strings.Contains(joined, "S E L E C T E D") {
		t.Fatal("panel shown in a narrow window")
	}
	m = press(t, m, "d")
	if !strings.Contains(strings.Join(screen(m), "\n"), "S E L E C T E D") {
		t.Error("d did not show details")
	}
}

// withMotion turns animation on with a clock the test moves by hand.
func withMotion(m Model, level motionLevel, now time.Time) (Model, *time.Time) {
	clock := &now
	m.motion, m.now = level, now
	m.nowFunc = func() time.Time { return *clock }
	return m, clock
}

// TestAnimationsStartWhenTheKeyIsPressed guards against starting them at the
// time of the last frame drawn, which made them finish before they began.
func TestAnimationsStartWhenTheKeyIsPressed(t *testing.T) {
	start := time.Date(2026, 11, 2, 9, 0, 0, 0, time.UTC)
	m, clock := withMotion(newModel(t, 80, 24), motionFull, start)
	*clock = start.Add(10 * time.Minute)
	m = showPage(t, m, pageQueue)
	if !m.rowsAt.Equal(start.Add(10 * time.Minute)) {
		t.Fatalf("entrance began at %v, not when the key was pressed", m.rowsAt)
	}
	if _, fade := m.rowIn(0); fade >= 1 {
		t.Error("the entrance had already finished")
	}
}

func TestRowsSlideInAndSettle(t *testing.T) {
	start := time.Date(2026, 11, 2, 9, 0, 0, 0, time.UTC)
	m, clock := withMotion(newModel(t, 80, 24), motionFull, start)
	m = showPage(t, m, pageQueue)
	if m.rowsAt != start {
		t.Fatalf("rows entrance not started: %v", m.rowsAt)
	}
	first := func(m Model) string {
		_, y := rowY(t, m, "To read")
		return screen(m)[y+2]
	}
	shifted := strings.Repeat(" ", rowsInSlide) + "▎"
	if !strings.HasPrefix(first(m), shifted) {
		t.Errorf("first row should start shifted right: %q", first(m))
	}
	m.now = start.Add(time.Second)
	*clock = m.now
	if !strings.HasPrefix(first(m), "▎") {
		t.Errorf("first row should have settled: %q", first(m))
	}
}

func TestFramesStopWhenStill(t *testing.T) {
	start := time.Date(2026, 11, 2, 9, 0, 0, 0, time.UTC)
	m, _ := withMotion(newModel(t, 80, 24), motionFull, start)
	m = showPage(t, m, pageQueue)
	if !m.ticking {
		t.Fatal("no frames scheduled for the entrance")
	}
	next, cmd := m.Update(frameMsg(start.Add(5 * time.Second)))
	m = next.(Model)
	if m.ticking || cmd == nil && m.ticking {
		t.Error("frames kept coming after everything settled")
	}
}

func TestAnimationOffDrawsAtOnce(t *testing.T) {
	m := newModel(t, 80, 24)
	m = showPage(t, m, pageQueue)
	if !strings.Contains(strings.Join(screen(m), "\n"), "Holographic paper") {
		t.Error("rows should appear at once with animation off")
	}
	if m.ticking {
		t.Error("animation off scheduled frames")
	}
}

func TestMotionLevelsCycleAndSave(t *testing.T) {
	m := newModel(t, 80, 24)
	m.motion = motionFull
	for _, want := range []motionLevel{motionOff, motionFull} {
		m = press(t, m, "m")
		if m.motion != want {
			t.Fatalf("motion = %d, want %d", m.motion, want)
		}
		if got := loadMotionLevel(m.config.DataDir); got != want {
			t.Errorf("saved %d, want %d", got, want)
		}
	}
}

func TestStartupPlaysAndAnyKeySkipsIt(t *testing.T) {
	store, _ := inbox.Open(t.TempDir())
	m := New(Config{DataDir: t.TempDir(), Store: store})
	m = send(t, m, tea.WindowSizeMsg{Width: 100, Height: 30})
	if !m.startupActive() {
		t.Fatal("startup not playing")
	}
	m.now = m.startedAt.Add(1300 * time.Millisecond)
	content := ansi.Strip(m.View().Content)
	if !strings.Contains(content, "z o t t y") || !strings.Contains(content, "▀") {
		t.Errorf("startup scene missing its page or wordmark:\n%s", content)
	}
	for i, line := range strings.Split(m.View().Content, "\n") {
		if w := lipgloss.Width(line); w != 100 {
			t.Fatalf("startup line %d is %d wide", i, w)
		}
	}
	m = send(t, m, tea.KeyPressMsg{Code: 'j', Text: "j"})
	if m.startupActive() {
		t.Error("a key did not skip the startup")
	}
}

func TestStartupWaitsForTheLibrary(t *testing.T) {
	store, _ := inbox.Open(t.TempDir())
	m := New(Config{DataDir: t.TempDir(), Store: store})
	m = send(t, m, tea.WindowSizeMsg{Width: 100, Height: 30})
	next, _ := m.Update(frameMsg(m.startedAt.Add(startupLength + time.Second)))
	if !next.(Model).startupActive() {
		t.Error("startup ended before the library loaded")
	}
	m = send(t, next.(Model), itemsLoadedMsg{items: sampleItems()})
	next, _ = m.Update(frameMsg(m.startedAt.Add(startupLength + 2*time.Second)))
	if next.(Model).startupActive() {
		t.Error("startup didn't end once loaded")
	}
}

func TestPageFlipKeepsItsWidth(t *testing.T) {
	cell := "\x1b[38;2;1;2;3m\x1b[48;2;4;5;6m▀"
	rows := []string{strings.Repeat(cell, thumbCols) + "\x1b[0m"}
	for _, k := range []float64{0, 0.3, 0.7, 1} {
		got := squeeze(rows, k, thumbCols, " ")
		if w := lipgloss.Width(got[0]); w != thumbCols {
			t.Errorf("k=%v width %d", k, w)
		}
	}
}

func TestFadeLineBlendsTowardBackground(t *testing.T) {
	line := "\x1b[1;38;2;200;100;0;48;2;20;20;20mHi\x1b[m"
	got := fadeLine(line, 0.5, [3]int{0, 0, 0})
	if !strings.Contains(got, "38;2;100;50;0") || !strings.Contains(got, "48;2;10;10;10") {
		t.Errorf("faded = %q", got)
	}
}

func TestCleanTitle(t *testing.T) {
	got := cleanTitle(`Bootstrapping in $$ \mathcal{N} $$ = 4 <i>SYM</i>`)
	if got != "Bootstrapping in N = 4 SYM" {
		t.Errorf("title = %q", got)
	}
}

func TestThemeSwitchIsSaved(t *testing.T) {
	m := newModel(t, 120, 30)
	m = press(t, m, "T")
	if m.themeIndex != 1 {
		t.Fatalf("theme = %d", m.themeIndex)
	}
	if got := loadTheme(m.config.DataDir); got != 1 {
		t.Errorf("saved theme = %d", got)
	}
}

func TestLoadErrorIsShown(t *testing.T) {
	m := newModel(t, 120, 30)
	m = send(t, m, itemsLoadedMsg{err: fmt.Errorf("no Zotero library at /nowhere")})
	if !strings.Contains(strings.Join(screen(m), "\n"), "no Zotero library") {
		t.Error("error not shown")
	}
}

func TestStaleThumbnailRequestIgnored(t *testing.T) {
	m := newModel(t, 120, 30)
	if cmd := m.loadExtras("KEY00030"); cmd != nil {
		t.Error("rendered a thumbnail for a row no longer selected")
	}
}

// TestDumpScreen writes screens drawn from the real library to the directory
// in ZOT_DUMP, for checking how they look. It runs only when that is set.
func TestDumpScreen(t *testing.T) {
	dir := os.Getenv("ZOT_DUMP")
	if dir == "" {
		t.Skip("set ZOT_DUMP to write screen dumps")
	}
	home, _ := os.UserHomeDir()
	zotero := home + "/Zotero"
	items, err := zotdb.LoadItems(zotero)
	if err != nil {
		t.Fatal(err)
	}
	collections, err := zotdb.LoadCollections(zotero)
	if err != nil {
		t.Fatal(err)
	}
	write := func(name, content string) {
		if err := os.WriteFile(dir+"/"+name+".ansi", []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
	}

	store, _ := inbox.Open(t.TempDir())
	opening := New(Config{DataDir: t.TempDir(), Store: store, ZoteroDir: zotero})
	opening = send(t, opening, tea.WindowSizeMsg{Width: 160, Height: 44})
	for _, ms := range []int{250, 700, 1250} {
		opening.now = opening.startedAt.Add(time.Duration(ms) * time.Millisecond)
		write(fmt.Sprintf("startup-%d", ms), opening.View().Content)
	}

	m := newModel(t, 160, 44)
	m.config.ZoteroDir = zotero
	m = send(t, m, itemsLoadedMsg{items: items, collections: collections})
	for range 2 {
		m = arrow(t, m, tea.KeyDown)
	}
	it, _ := m.selected()
	if thumb, err := renderThumbnail(it.PDF); err == nil {
		m.thumbs[it.Key] = thumb
	}
	write("library", m.View().Content)

	menu := press(t, m, " ")
	write("menu", menu.View().Content)
	palette := press(t, m, ":")
	write("palette", palette.View().Content)

	for i, item := range m.visible {
		if item.Notes > 0 || item.Highlights > 0 {
			m.position().cursor = i
			break
		}
	}
	it, _ = m.selected()
	details, err := zotdb.LoadDetails(zotero, it.ID)
	if err != nil {
		t.Fatal(err)
	}
	reader := send(t, m, notesLoadedMsg{title: cleanTitle(it.Title), details: details})
	write("notes", reader.View().Content)

	text, _ := m.startTextSearch()
	for _, r := range "glueball" {
		text = press(t, text, string(r))
	}
	text = send(t, text, text.runTextSearch("glueball")())
	write("fulltext", text.View().Content)

	// The window from the screenshot, rows spaced out, with a collection
	// found by searching the tree.
	tall := newModel(t, 105, 64)
	tall.config.ZoteroDir = zotero
	tall.compact = false
	tall = send(t, tall, itemsLoadedMsg{items: items, collections: collections})
	tall = send(t, tall, tea.KeyPressMsg{Code: tea.KeyLeft})
	tall = press(t, tall, "/")
	for _, r := range "entropy" {
		tall = press(t, tall, string(r))
	}
	write("tree-search", tall.View().Content)
	tall = press(t, tall, "enter")
	tall = send(t, tall, tea.KeyPressMsg{Code: tea.KeyDown})
	write("spaced", tall.View().Content)

	// A row-by-row entrance caught partway through.
	moving, _ := withMotion(m, motionFull, time.Date(2026, 11, 2, 9, 0, 0, 0, time.UTC))
	moving = showPage(t, moving, pageQueue)
	moving.now = moving.rowsAt.Add(110 * time.Millisecond)
	write("rows-in", moving.View().Content)
}

func sampleCollections() []zotdb.Collection {
	return []zotdb.Collection{
		{ID: 10, Name: "QCD", Items: 2, HasChildren: true},
		{ID: 11, ParentID: 10, Name: "Chiral PT", Depth: 1, Items: 3},
		{ID: 20, Name: "Cosmology", Items: 1},
	}
}

func withCollections(t *testing.T, width, height int) Model {
	t.Helper()
	m := newModel(t, width, height)
	items := sampleItems()
	items[1].CollectionIDs = []int{10}
	items[2].CollectionIDs = []int{11}
	items[3].CollectionIDs = []int{11}
	items[4].CollectionIDs = []int{11, 20}
	m = send(t, m, itemsLoadedMsg{items: items, collections: sampleCollections()})
	m.render()
	return m
}

func rowY(t *testing.T, m Model, text string) (int, int) {
	t.Helper()
	for y, line := range screen(m) {
		if x := strings.Index(line, text); x >= 0 {
			return lipgloss.Width(line[:x]), y
		}
	}
	t.Fatalf("%q not on screen:\n%s", text, strings.Join(screen(m), "\n"))
	return 0, 0
}

func TestSidebarOpensOnWideWindows(t *testing.T) {
	if !withCollections(t, 180, 40).sidebarShown() {
		t.Error("sidebar closed on a wide window")
	}
	if withCollections(t, 120, 40).sidebarShown() {
		t.Error("sidebar open on a medium window")
	}
}

func TestSidebarFollowsResizeUntilChosen(t *testing.T) {
	m := withCollections(t, 80, 24)
	m = send(t, m, tea.WindowSizeMsg{Width: 200, Height: 50})
	if !m.sidebarShown() {
		t.Fatal("sidebar stayed closed after the window grew")
	}
	m = press(t, m, "c")
	m = send(t, m, tea.WindowSizeMsg{Width: 210, Height: 50})
	if m.sidebarShown() {
		t.Error("a resize reopened a sidebar the user closed")
	}
}

func TestSidebarTreeExpandsAndFilters(t *testing.T) {
	m := withCollections(t, 180, 40)
	if strings.Contains(strings.Join(screen(m), "\n"), "Chiral PT") {
		t.Fatal("child shown under a closed parent")
	}
	x, y := rowY(t, m, "▸ QCD")
	m = click(t, m, x, y)
	if !m.expanded[10] {
		t.Fatal("arrow click did not open QCD")
	}
	if m.collection != allItems {
		t.Error("arrow click should not choose the collection")
	}
	x, y = rowY(t, m, "Chiral PT")
	m = click(t, m, x, y)
	if m.collection != 11 || len(m.visible) != 3 {
		t.Fatalf("collection %d with %d items", m.collection, len(m.visible))
	}
	if !strings.Contains(screen(m)[0], "QCD / Chiral PT") {
		t.Errorf("breadcrumb missing: %q", screen(m)[0])
	}
	m = showPage(t, m, pageQueue)
	total := 0
	for _, n := range m.groupCounts {
		total += n
	}
	if total != 3 {
		t.Errorf("queue counts cover %d items, want the collection's 3", total)
	}
}

func arrow(t *testing.T, m Model, code rune) Model {
	t.Helper()
	m = send(t, m, tea.KeyPressMsg{Code: code})
	m.render()
	return m
}

func TestSidebarKeyboard(t *testing.T) {
	m := withCollections(t, 120, 40)
	m = press(t, m, "c")
	if !m.sidebarShown() || !m.sidebarFocused {
		t.Fatal("c did not open and focus the sidebar")
	}
	m = arrow(t, m, tea.KeyDown)
	if m.collection != 10 || len(m.visible) != 1 {
		t.Fatalf("moving onto QCD should show its papers: collection %d, %d papers", m.collection, len(m.visible))
	}
	m = arrow(t, m, tea.KeyRight)
	if !m.expanded[10] || !m.sidebarFocused {
		t.Fatal("→ on a closed branch should open it")
	}
	m = arrow(t, m, tea.KeyDown)
	m = press(t, m, "enter")
	if m.collection != 11 || m.sidebarFocused {
		t.Fatalf("enter should choose Chiral PT and move to the list: collection %d focused %v", m.collection, m.sidebarFocused)
	}
	m = arrow(t, m, tea.KeyDown)
	if m.position().cursor != 1 {
		t.Errorf("↓ should move the list now, cursor = %d", m.position().cursor)
	}
	m = arrow(t, m, tea.KeyLeft)
	if !m.sidebarFocused || m.sideCursor != 2 {
		t.Fatalf("← should return to the tree at Chiral PT: focused %v cursor %d", m.sidebarFocused, m.sideCursor)
	}
	m = arrow(t, m, tea.KeyLeft)
	if m.sideCursor != 1 {
		t.Errorf("← in the tree should go to the parent, cursor = %d", m.sideCursor)
	}
	m = arrow(t, m, tea.KeyRight)
	m = arrow(t, m, tea.KeyRight)
	if m.sidebarFocused {
		t.Error("→ on an open branch should step into the list")
	}
	m = press(t, m, "c")
	if m.sidebarShown() {
		t.Error("c did not close the sidebar")
	}
}

func TestLeftOpensTheTreeWhenHidden(t *testing.T) {
	m := withCollections(t, 120, 40)
	m = arrow(t, m, tea.KeyLeft)
	if !m.sidebarShown() || !m.sidebarFocused {
		t.Error("← should open and focus the collections")
	}
}

func TestQueueGroupsThenTree(t *testing.T) {
	m := withCollections(t, 180, 40)
	m = showPage(t, m, pageQueue)
	m = arrow(t, m, tea.KeyRight)
	if m.group != 1 {
		t.Fatalf("→ should go to the next group, group = %d", m.group)
	}
	m = arrow(t, m, tea.KeyLeft)
	m = arrow(t, m, tea.KeyLeft)
	if m.group != 0 || !m.sidebarFocused {
		t.Errorf("← from the first group should enter the tree: group %d focused %v", m.group, m.sidebarFocused)
	}
}

func TestTreeSearch(t *testing.T) {
	m := withCollections(t, 180, 40)
	m = arrow(t, m, tea.KeyLeft)
	m = press(t, m, "/")
	for _, r := range "chir" {
		m = press(t, m, string(r))
	}
	rows := m.treeRows()
	if len(rows) != 3 || rows[1].name != "QCD" || rows[2].name != "Chiral PT" {
		t.Fatalf("rows = %+v", rows)
	}
	if m.collection != 11 {
		t.Errorf("the first match should be shown, collection = %d", m.collection)
	}
	m = press(t, m, "enter")
	if m.collection != 11 || m.sidebarFocused {
		t.Errorf("enter should choose it and go to the list: %d %v", m.collection, m.sidebarFocused)
	}
	if !strings.Contains(strings.Join(screen(m), "\n"), "/ chir") {
		t.Error("the tree search isn't shown")
	}
}

func TestClicksLineUpBesideTheSidebar(t *testing.T) {
	m := withCollections(t, 180, 40)
	x, y := rowY(t, m, "Holographic paper 4 ")
	m = click(t, m, x, y)
	if it, _ := m.selected(); it.Key != "KEY00004" {
		t.Errorf("selected %s", it.Key)
	}
	_, y = rowY(t, m, " Open ")
	m = click(t, m, m.tableX()+m.tableWidth()+2, y)
	if m.message != "This paper has no PDF" {
		t.Errorf("Open button missed: %q", m.message)
	}
	m = showPage(t, m, pageQueue)
	x, y = rowY(t, m, "New")
	m = click(t, m, x, y)
	if zotdb.QueueGroups[m.group] != zotdb.GroupNew {
		t.Errorf("chip click chose group %d", m.group)
	}
}

func TestHelpLetsKeysThrough(t *testing.T) {
	m := newModel(t, 120, 40)
	m = press(t, m, "?")
	if !strings.Contains(strings.Join(screen(m), "\n"), "To read, in progress, new, read") {
		t.Fatal("help lacks the state legend")
	}
	m = press(t, m, "t")
	if m.showHelp || m.themeIndex != 1 {
		t.Errorf("t in help: showHelp=%v theme=%d", m.showHelp, m.themeIndex)
	}
	m = press(t, m, "m")
	if m.motion == motionOff {
		t.Error("lowercase m did not change the animation level")
	}
}

func TestStatusKeyCyclesAndQueuesWhileZoteroIsClosed(t *testing.T) {
	m := newModel(t, 120, 30)
	m.config.Port = 1 // nothing listens here, as when Zotero is closed
	it, _ := m.selected()
	want := nextStatus(it.Status)
	m, cmd := m.setStatus(want)
	updated, _ := m.selected()
	if updated.Key != it.Key || updated.Status != want {
		t.Fatalf("%s status = %q, want %q", updated.Key, updated.Status, want)
	}
	msg := cmd()
	m = send(t, m, msg)
	if !strings.Contains(m.message, "Zotero is closed") {
		t.Errorf("message = %q", m.message)
	}
	pending, err := zotapi.OpenQueue(m.config.DataDir).List()
	if err != nil || len(pending) != 1 || pending[0].Value != want {
		t.Errorf("queue = %+v, %v", pending, err)
	}
}

func TestStatusButtonsClick(t *testing.T) {
	m := newModel(t, 120, 30)
	m.config.Port = 1
	x, y := rowY(t, m, " Open ")
	m = click(t, m, x+8+4, y) // second button: In Progress
	if it, _ := m.selected(); it.Status != "In Progress" {
		t.Errorf("status = %q", it.Status)
	}
}

func TestNextStatus(t *testing.T) {
	for current, want := range map[string]string{
		"": "To Read", "To Read": "In Progress", "In Progress": "Read", "Read": "New", "New": "To Read", "Read, useful": "New",
	} {
		if got := nextStatus(current); got != want {
			t.Errorf("%q -> %q, want %q", current, got, want)
		}
	}
}

func TestInboxRemoveNeedsTwoPresses(t *testing.T) {
	m := newModel(t, 120, 30)
	e, err := m.config.Store.Create(inbox.Entry{Kind: "saveItems", Title: "Saved paper"})
	if err != nil {
		t.Fatal(err)
	}
	m = send(t, m, m.loadInbox()())
	m = showPage(t, m, pageInbox)
	m = press(t, m, "x")
	if entries, _ := m.config.Store.List(); len(entries) != 1 {
		t.Fatal("removed after one press")
	}
	m = arrow(t, m, tea.KeyDown)
	m = press(t, m, "x")
	if entries, _ := m.config.Store.List(); len(entries) != 1 {
		t.Fatal("a different key should reset the confirmation")
	}
	m = press(t, m, "x")
	if entries, _ := m.config.Store.List(); len(entries) != 0 {
		t.Errorf("entry %s not removed", e.ID)
	}
}

func TestArrowsMoveWhileSearching(t *testing.T) {
	m := newModel(t, 120, 30)
	m = press(t, m, "/")
	for _, r := range "paper" {
		m = press(t, m, string(r))
	}
	m = send(t, m, tea.KeyPressMsg{Code: tea.KeyDown})
	m = send(t, m, tea.KeyPressMsg{Code: tea.KeyDown})
	if !m.searching || m.position().cursor != 2 {
		t.Fatalf("searching=%v cursor=%d", m.searching, m.position().cursor)
	}
	m = press(t, m, "enter")
	m = arrow(t, m, tea.KeyDown)
	if m.searching || m.position().cursor != 3 || m.query != "paper" {
		t.Errorf("after enter: searching=%v cursor=%d query=%q", m.searching, m.position().cursor, m.query)
	}
}

func TestSpaceOpensActionsAndKeysRunThem(t *testing.T) {
	m := newModel(t, 120, 30)
	m.config.Port = 1
	m = arrow(t, m, tea.KeyDown)
	m = press(t, m, " ")
	if m.over.kind != overlayMenu {
		t.Fatal("space did not open the actions")
	}
	joined := strings.Join(strings.Split(ansi.Strip(m.View().Content), "\n"), "\n")
	for _, want := range []string{"Open the PDF", "Edit tags", "Add to a collection", "Notes and highlights"} {
		if !strings.Contains(joined, want) {
			t.Errorf("menu lacks %q", want)
		}
	}
	m = press(t, m, "e")
	if m.over.kind != overlayPrompt || m.over.value != "QCD" {
		t.Fatalf("e should open the tag editor with the current tags, got %d %q", m.over.kind, m.over.value)
	}
	for _, r := range ", to-cite" {
		m = press(t, m, string(r))
	}
	next, cmd := m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	m = next.(Model)
	if it, _ := m.selected(); strings.Join(it.Tags, ",") != "QCD,to-cite" {
		t.Errorf("tags on screen = %v", it.Tags)
	}
	m = drain(t, m, cmd)
	pending, _ := zotapi.OpenQueue(m.config.DataDir).List()
	if len(pending) != 1 || pending[0].Kind != zotapi.AddTag || pending[0].Value != "to-cite" {
		t.Errorf("queued = %+v", pending)
	}
}

// drain runs a command and the commands it returns, as the program would,
// skipping timers.
func drain(t *testing.T, m Model, cmd tea.Cmd) Model {
	t.Helper()
	queue := []tea.Cmd{cmd}
	for len(queue) > 0 {
		c := queue[0]
		queue = queue[1:]
		if c == nil {
			continue
		}
		done := make(chan tea.Msg, 1)
		go func() { done <- c() }()
		var msg tea.Msg
		select {
		case msg = <-done:
		case <-time.After(300 * time.Millisecond):
			continue // a timer
		}
		if batch, ok := msg.(tea.BatchMsg); ok {
			queue = append(queue, batch...)
			continue
		}
		if msg == nil {
			continue
		}
		next, more := m.Update(msg)
		m = next.(Model)
		queue = append(queue, more)
	}
	return m
}

func TestTypingFiltersAMenu(t *testing.T) {
	m := withCollections(t, 120, 40)
	m = press(t, m, "+")
	if m.over.kind != overlayMenu {
		t.Fatal("+ did not open the collection picker")
	}
	for _, r := range "chiral" {
		m = press(t, m, string(r))
	}
	matches := m.menuMatches()
	if len(matches) != 1 || matches[0].label != "Chiral PT" {
		t.Fatalf("matches = %+v", matches)
	}
	m.config.Port = 1
	m = press(t, m, "enter")
	if it, _ := m.selected(); !strings.Contains(strings.Join(it.Collections, ";"), "QCD / Chiral PT") {
		t.Errorf("collections on screen = %v", it.Collections)
	}
}

func TestPaletteChangesTheme(t *testing.T) {
	m := newModel(t, 120, 30)
	m = press(t, m, ":")
	for _, r := range "theme paper" {
		m = press(t, m, string(r))
	}
	m = press(t, m, "enter")
	if themes[m.themeIndex].name != "Paper" || m.over.kind != overlayNone {
		t.Errorf("theme %q, overlay %d", themes[m.themeIndex].name, m.over.kind)
	}
}

func TestClickOutsideClosesOverlay(t *testing.T) {
	m := newModel(t, 120, 30)
	m = press(t, m, ":")
	m.View()
	m = click(t, m, 1, 1)
	m.View()
	if m.over.kind != overlayNone {
		t.Error("click outside left the overlay open")
	}
}

func TestClickAMenuRow(t *testing.T) {
	m := newModel(t, 120, 30)
	m = press(t, m, ":")
	content := strings.Split(ansi.Strip(m.View().Content), "\n")
	for y, line := range content {
		if x := strings.Index(line, "Theme: Moss"); x >= 0 {
			m = send(t, m, tea.MouseClickMsg{X: lipgloss.Width(line[:x]), Y: y, Button: tea.MouseLeft})
			if themes[m.themeIndex].name != "Moss" {
				t.Errorf("theme = %s", themes[m.themeIndex].name)
			}
			return
		}
	}
	t.Fatal("menu row not on screen")
}

func TestNotesReaderScrolls(t *testing.T) {
	m := newModel(t, 100, 24)
	var notes []string
	for i := range 40 {
		notes = append(notes, fmt.Sprintf("Note line %d", i))
	}
	m = send(t, m, notesLoadedMsg{title: "A paper", details: zotdb.Details{
		Notes:       notes,
		Annotations: []zotdb.Annotation{{Page: "3", Text: "a highlighted sentence", Comment: "why"}},
	}})
	if m.over.kind != overlayReader {
		t.Fatal("reader not open")
	}
	if !strings.Contains(ansi.Strip(m.View().Content), "Note line 0") {
		t.Error("first note not shown")
	}
	m = arrow(t, m, tea.KeyEnd)
	content := ansi.Strip(m.View().Content)
	if !strings.Contains(content, "↳ why") || !strings.Contains(content, "Page 3") {
		t.Errorf("end of reader:\n%s", content)
	}
	for i, line := range strings.Split(m.View().Content, "\n") {
		if w := lipgloss.Width(line); w != 100 {
			t.Fatalf("line %d is %d wide", i, w)
		}
	}
	m = press(t, m, "esc")
	if m.over.kind != overlayNone {
		t.Error("esc did not close the reader")
	}
}

func TestTextSearchShowsMatchingPDFs(t *testing.T) {
	m := newModel(t, 120, 30)
	m = press(t, m, "/")
	m = press(t, m, "tab")
	for _, r := range "glueball" {
		m = press(t, m, string(r))
	}
	if !m.textScope || m.textMatches != nil || len(m.visible) != 0 {
		t.Fatalf("scope=%v matches=%v visible=%d", m.textScope, m.textMatches, len(m.visible))
	}
	if !strings.Contains(strings.Join(screen(m), "\n"), "searching") {
		t.Error("no sign the search is running")
	}
	m = send(t, m, textResultMsg{query: "glueball", ids: map[int]bool{3: true, 5: true}})
	if len(m.visible) != 2 {
		t.Errorf("visible = %d", len(m.visible))
	}
	m = send(t, m, textResultMsg{query: "glue", ids: map[int]bool{1: true}})
	if len(m.visible) != 2 {
		t.Error("a stale result replaced the current one")
	}
	m = press(t, m, "tab")
	if m.textScope {
		t.Error("tab did not switch back to the library")
	}
}

func TestOverlayAndStartupFitEveryWindow(t *testing.T) {
	for _, size := range [][2]int{{60, 16}, {80, 24}, {140, 45}, {200, 60}} {
		m := withCollections(t, size[0], size[1])
		for _, open := range []string{" ", ":"} {
			next := press(t, m, open)
			for i, line := range strings.Split(next.View().Content, "\n") {
				if w := lipgloss.Width(line); w != size[0] {
					t.Errorf("%v %q line %d is %d wide", size, open, i, w)
				}
			}
		}
	}
}

func TestSortMenuOrdersTheList(t *testing.T) {
	m := newModel(t, 120, 30)
	m = press(t, m, "o")
	if m.over.kind != overlayMenu || m.over.title != "Sort by" {
		t.Fatal("o should offer the sort orders")
	}
	for _, r := range "title" {
		m = press(t, m, string(r))
	}
	m = press(t, m, "enter")
	if sortOrders[m.sortBy].name != "title" {
		t.Fatalf("sort = %s", sortOrders[m.sortBy].name)
	}
	for i := 1; i < len(m.visible); i++ {
		if strings.ToLower(cleanTitle(m.visible[i-1].Title)) > strings.ToLower(cleanTitle(m.visible[i].Title)) {
			t.Fatalf("not in title order at %d", i)
		}
	}
	if loadSetting(m.config.DataDir, "sort") != "title" {
		t.Error("sort not saved")
	}
	if !strings.Contains(strings.Join(screen(m), "\n"), "Sort: Title") {
		t.Error("the header doesn't show the order")
	}
}

func TestSearchRanksRegardlessOfSort(t *testing.T) {
	m := newModel(t, 120, 30)
	m, _ = m.setSort(sortIndex("title"))
	m = press(t, m, "/")
	for _, r := range "paper 7" {
		m = press(t, m, string(r))
	}
	if len(m.visible) == 0 || m.visible[0].Title != "Holographic paper 7" {
		t.Errorf("best match should come first, got %q", m.visible[0].Title)
	}
}

func TestRowsAreSpacedOut(t *testing.T) {
	m := newModel(t, 120, 30)
	m.compact = false
	m.clampCursor()
	_, y := rowY(t, m, "Holographic paper 1 ")
	if below := ansi.Strip(ansi.Cut(screen(m)[y+1], 0, 60)); strings.TrimSpace(below) != "" {
		t.Errorf("the line under a paper should be blank: %q", below)
	}
	if !strings.Contains(screen(m)[y+2], "Holographic paper 2 ") {
		t.Errorf("the next paper should follow the blank line: %q", screen(m)[y+2])
	}
	m = click(t, m, 20, y+1)
	if it, _ := m.selected(); it.Key != "KEY00001" {
		t.Errorf("clicking the gap selected %s", it.Key)
	}
	for range 25 {
		m = arrow(t, m, tea.KeyDown)
	}
	if !strings.Contains(strings.Join(screen(m), "\n"), "Holographic paper 24 ") {
		t.Error("the selected paper scrolled out of view")
	}
}

func TestVimKeysAreGone(t *testing.T) {
	m := newModel(t, 120, 30)
	for _, key := range []string{"j", "l", "h", "G"} {
		before := *m.position()
		m = press(t, m, key)
		if *m.position() != before || m.sidebarFocused {
			t.Errorf("%s still moves", key)
		}
	}
}

func TestDeepBreadcrumbKeepsTabsAndEnd(t *testing.T) {
	m := withCollections(t, 105, 30)
	m.collections = append(m.collections, zotdb.Collection{ID: 30, ParentID: 11, Depth: 2,
		Name: "Entanglement entropy and phase transitions in holographic models"})
	m, _ = m.chooseCollection(30)
	top := screen(m)[0]
	if !strings.Contains(top, "Inbox") || !strings.Contains(top, "holographic models") || !strings.Contains(top, "…") {
		t.Errorf("top bar = %q", top)
	}
	if w := lipgloss.Width(top); w != 105 {
		t.Errorf("width %d", w)
	}
}

func TestReloadsWhenZoteroWritesAndKeepsSelection(t *testing.T) {
	m := newModel(t, 120, 30)
	m = send(t, m, itemsLoadedMsg{stamp: "v1", items: sampleItems()})
	m = arrow(t, m, tea.KeyDown)
	m = arrow(t, m, tea.KeyDown)
	kept, _ := m.selected()

	if next, _ := m.Update(watchMsg{stamp: "v1"}); next.(Model).loading {
		t.Error("an unchanged library was loaded again")
	}
	next, cmd := m.Update(watchMsg{stamp: "v2"})
	m = next.(Model)
	if cmd == nil || !m.loading {
		t.Fatal("a changed library was not loaded again")
	}

	// A new paper arrives at the top; the selection stays on the same paper.
	items := append([]zotdb.Item{{ID: 9999, Key: "NEWPAPER", Title: "Just saved", DateAdded: "2099-01-01 00:00:00"}}, sampleItems()...)
	m = send(t, m, itemsLoadedMsg{stamp: "v2", items: items})
	if it, _ := m.selected(); it.Key != kept.Key {
		t.Errorf("selection moved from %s to %s", kept.Key, it.Key)
	}
	if m.stamp != "v2" || m.loading {
		t.Errorf("stamp %q loading %v", m.stamp, m.loading)
	}
}

func TestNarrowListKeepsTitles(t *testing.T) {
	for _, width := range []int{40, 50, 60, 70, 120} {
		m := newModel(t, width, 20)
		line := m.row(width, false, 1, "Holographic QCD phase transitions and more words", "Picard", "2025", "○")
		if w := lipgloss.Width(line); w != width {
			t.Errorf("width %d: row is %d wide", width, w)
		}
		if width <= 60 && strings.Contains(line, "Picard") {
			t.Errorf("width %d: the author should give way to the title: %q", width, ansi.Strip(line))
		}
		if !strings.Contains(ansi.Strip(line), "Holographic QCD") {
			t.Errorf("width %d: title squeezed out: %q", width, ansi.Strip(line))
		}
	}
}
