package tui

import (
	"math"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
)

// Motion is on or off: on plays every animation, and off draws each state at
// once. Frames are only scheduled while something moves, so a still screen
// costs no CPU.

type motionLevel int

const (
	motionFull motionLevel = iota
	motionOff
)

var motionNames = []string{"on", "off"}

const (
	frameInterval = 16 * time.Millisecond

	startupLength = 1500 * time.Millisecond
	rowsInLength  = 240 * time.Millisecond
	rowsInStagger = 14 * time.Millisecond
	rowsInSlide   = 6
	flipLength    = 340 * time.Millisecond
	fadeLength    = 160 * time.Millisecond
	bandLength    = 160 * time.Millisecond
	toastLength   = 200 * time.Millisecond
	toastLife     = 4 * time.Second
	overlayLength = 170 * time.Millisecond
)

type frameMsg time.Time

func loadMotionLevel(dataDir string) motionLevel {
	data, err := os.ReadFile(filepath.Join(dataDir, "motion"))
	if err == nil {
		for i, name := range motionNames {
			if strings.TrimSpace(string(data)) == name {
				return motionLevel(i)
			}
		}
	}
	return motionFull
}

func saveMotionLevel(dataDir string, level motionLevel) error {
	if err := os.MkdirAll(dataDir, 0o700); err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(dataDir, "motion"), []byte(motionNames[level]+"\n"), 0o600)
}

// animate keeps frames coming for d.
func (m *Model) animate(d time.Duration) tea.Cmd {
	if m.motion == motionOff {
		return nil
	}
	if until := m.clock().Add(d); until.After(m.animUntil) {
		m.animUntil = until
	}
	if m.ticking {
		return nil
	}
	m.ticking = true
	return frameTick()
}

func frameTick() tea.Cmd {
	return tea.Tick(frameInterval, func(t time.Time) tea.Msg { return frameMsg(t) })
}

// clock is the time animations sample, the same for everything in a frame.
// Update sets it from the real time at every event, so an animation started
// by a key press begins at that moment rather than at the last frame drawn.
func (m Model) clock() time.Time {
	if m.now.IsZero() {
		return m.timeNow()
	}
	return m.now
}

func (m Model) timeNow() time.Time {
	if m.nowFunc != nil {
		return m.nowFunc()
	}
	return time.Now()
}

// progress is how far an animation that began at start has run, from 0 to 1.
func (m Model) progress(start time.Time, length time.Duration) float64 {
	if start.IsZero() || m.motion == motionOff {
		return 1
	}
	t := float64(m.clock().Sub(start)) / float64(length)
	return math.Max(0, math.Min(1, t))
}

func easeOut(t float64) float64 { return 1 - math.Pow(1-t, 3) }

func easeInOut(t float64) float64 {
	if t < 0.5 {
		return 4 * t * t * t
	}
	return 1 - math.Pow(-2*t+2, 3)/2
}

// easeOutBack overshoots a little and settles, for things arriving.
func easeOutBack(t float64) float64 {
	const c = 1.4
	return 1 + (c+1)*math.Pow(t-1, 3) + c*math.Pow(t-1, 2)
}

var sgr = regexp.MustCompile(`\x1b\[([0-9;:]*)m`)

// fadeLine blends every true color in an ANSI line towards bg: k=0 is all
// background, k=1 the line as drawn.
func fadeLine(line string, k float64, bg [3]int) string {
	if k >= 1 {
		return line
	}
	return recolor(line, func(c [3]int, _ bool) [3]int { return blend(bg, c, k) })
}

// recolor rewrites the 24-bit colors of SGR sequences. isBackground tells the
// function whether it sees a background color.
func recolor(line string, f func(c [3]int, isBackground bool) [3]int) string {
	return sgr.ReplaceAllStringFunc(line, func(seq string) string {
		params := strings.Split(seq[2:len(seq)-1], ";")
		for i := 0; i+4 < len(params); i++ {
			if (params[i] == "38" || params[i] == "48") && params[i+1] == "2" {
				var c [3]int
				for j := range c {
					c[j], _ = strconv.Atoi(params[i+2+j])
				}
				c = f(c, params[i] == "48")
				for j := range c {
					params[i+2+j] = strconv.Itoa(c[j])
				}
				i += 4
			}
		}
		return "\x1b[" + strings.Join(params, ";") + "m"
	})
}

func blend(from, to [3]int, k float64) [3]int {
	var out [3]int
	for i := range out {
		out[i] = int(math.Round(float64(from[i]) + (float64(to[i])-float64(from[i]))*k))
	}
	return out
}

func hexColor(s string) [3]int {
	v, _ := strconv.ParseUint(strings.TrimPrefix(s, "#"), 16, 32)
	return [3]int{int(v >> 16 & 0xff), int(v >> 8 & 0xff), int(v & 0xff)}
}

// slideLine shifts a line right by n cells, keeping its width.
func (m Model) slideLine(line string, n, width int) string {
	if n <= 0 {
		return line
	}
	return m.pad(m.style.base.Render(strings.Repeat(" ", n))+ansi.Truncate(line, width-n, ""), width)
}

// rowIn returns how a table row looks while rows come in: its slide and fade.
func (m Model) rowIn(i int) (slide int, fade float64) {
	if m.rowsAt.IsZero() || m.motion == motionOff {
		return 0, 1
	}
	start := m.rowsAt.Add(time.Duration(i) * rowsInStagger)
	t := easeOut(m.progress(start, rowsInLength))
	return int(math.Round((1 - t) * rowsInSlide)), t
}

// rowsInDuration is how long the rows of a full table take to come in.
func (m Model) rowsInDuration() time.Duration {
	return rowsInLength + time.Duration(m.tableRows())*rowsInStagger
}

// startRowsIn plays the row-by-row entrance of a list.
func (m *Model) startRowsIn() tea.Cmd {
	if m.motion == motionOff || m.startupActive() {
		return nil
	}
	m.rowsAt = m.clock()
	return m.animate(m.rowsInDuration())
}

func (m Model) startupActive() bool {
	return !m.startupDone && m.motion == motionFull
}

// startupScene draws the opening: a page turns in from the depth of the
// screen and settles, the wordmark comes in a letter at a time under it, and
// a hairline fills as the library loads.
func (m Model) startupScene() string {
	s := m.style
	th := themes[m.themeIndex]
	t := float64(m.clock().Sub(m.startedAt)) / float64(startupLength)
	t = math.Max(0, math.Min(1, t))

	// The page: 22 by 28 pixels at rest, two pixels to a cell.
	const pageW, pageH = 22, 28
	arrive := math.Min(t/0.55, 1)
	scale := 0.12 + 0.88*easeOutBack(arrive)
	turn := math.Cos((1 - easeOut(arrive)) * math.Pi * 0.45)
	w := max(int(math.Round(pageW*scale*turn)), 1)
	h := max(int(math.Round(pageH*scale)), 2)
	light := math.Min(arrive*1.4, 1)

	bg := hexColor(th.bg)
	paper := [3]int{int(th.paperLight[0]), int(th.paperLight[1]), int(th.paperLight[2])}
	ink := [3]int{int(th.paperDark[0]), int(th.paperDark[1]), int(th.paperDark[2])}
	pixel := func(x, y int) [3]int {
		if x < 0 || y < 0 || x >= w || y >= h {
			return bg
		}
		// Sample the resting page, so its lines stay put as it grows.
		px, py := x*pageW/w, y*pageH/h
		c := paper
		switch {
		case py >= 3 && py <= 4 && px >= 3 && px < 19:
			c = blend(paper, ink, 0.75) // the title
		case py == 6 && px >= 6 && px < 16:
			c = blend(paper, ink, 0.45) // the authors
		case py >= 9 && py < 25 && py%2 == 1 && (px >= 3 && px < 10 || px >= 12 && px < 19):
			c = blend(paper, ink, 0.3) // two columns of text
		}
		// The edge turned away from the light is darker while it turns.
		shade := 1 - (1-turn)*float64(x)/float64(max(w, 1))*0.6
		c = blend(ink, c, shade)
		return blend(bg, c, light)
	}

	lines := make([]string, 0, m.height)
	top := max((m.height-pageH/2-6)/2, 0)
	for len(lines) < top {
		lines = append(lines, m.pad("", m.width))
	}
	left := (m.width - w) / 2
	offsetY := (pageH - h) / 2
	for row := 0; row < pageH/2; row++ {
		var b strings.Builder
		b.WriteString(s.base.Render(strings.Repeat(" ", max(left, 0))))
		for x := 0; x < w; x++ {
			up, down := pixel(x, row*2-offsetY), pixel(x, row*2+1-offsetY)
			b.WriteString("\x1b[38;2;" + rgb(up) + ";48;2;" + rgb(down) + "m▀")
		}
		b.WriteString("\x1b[m")
		lines = append(lines, m.pad(b.String(), m.width))
	}

	lines = append(lines, m.pad("", m.width))
	word := "z o t t y"
	shown := int(math.Round(math.Max(0, math.Min(1, (t-0.45)/0.25)) * float64(len(word))))
	lines = append(lines, m.centered(s.bold.Render(word[:shown]), len(word)))
	tagline := "a quiet home for your papers"
	typed := int(math.Round(math.Max(0, math.Min(1, (t-0.6)/0.3)) * float64(len([]rune(tagline)))))
	lines = append(lines, m.centered(s.dim.Render(string([]rune(tagline)[:typed])), len(tagline)))
	lines = append(lines, m.pad("", m.width))

	// The hairline runs ahead of loading but only finishes once it's done.
	fill := math.Min(t, 0.8)
	if !m.loading {
		fill = math.Max(fill, math.Min(1, t*1.2))
	}
	const barWidth = 28
	done := int(math.Round(fill * barWidth))
	lines = append(lines, m.centered(s.accent.Render(strings.Repeat("━", done))+s.line.Render(strings.Repeat("─", barWidth-done)), barWidth))

	for len(lines) < m.height {
		lines = append(lines, m.pad("", m.width))
	}
	// The whole scene fades out in its last moments.
	out := lines[:m.height]
	if t > 0.88 {
		k := 1 - (t-0.88)/0.12
		for i := range out {
			out[i] = fadeLine(out[i], k, bg)
		}
	}
	return strings.Join(out, "\n")
}

func (m Model) centered(s string, width int) string {
	left := max((m.width-width)/2, 0)
	return m.pad(m.style.base.Render(strings.Repeat(" ", left))+s, m.width)
}

func rgb(c [3]int) string {
	return strconv.Itoa(c[0]) + ";" + strconv.Itoa(c[1]) + ";" + strconv.Itoa(c[2])
}

// flippedThumb draws the page turning from the last paper's to this one's:
// the old page narrows to its edge, then the new one opens.
func (m Model) flippedThumb(current []string) []string {
	t := m.progress(m.flipAt, flipLength)
	if t >= 1 || m.motion != motionFull {
		return current
	}
	page := current
	var k float64
	if t < 0.5 {
		page = m.flipFrom
		k = 1 - easeInOut(t*2)
	} else {
		k = easeInOut((t - 0.5) * 2)
	}
	if page == nil {
		page = current
	}
	return squeeze(page, k, thumbCols, m.style.base.Render(" "))
}

// squeeze narrows rows of half-block cells to k of their width, centered.
func squeeze(rows []string, k float64, width int, blank string) []string {
	w := int(math.Round(float64(width) * k))
	left := (width - w) / 2
	out := make([]string, len(rows))
	for i, row := range rows {
		cells := halfBlockCells(row)
		var b strings.Builder
		b.WriteString(strings.Repeat(blank, left))
		for x := 0; x < w; x++ {
			b.WriteString(cells[min(x*width/max(w, 1), len(cells)-1)])
		}
		b.WriteString("\x1b[m")
		b.WriteString(strings.Repeat(blank, width-w-left))
		out[i] = b.String()
	}
	return out
}

var halfBlock = regexp.MustCompile(`\x1b\[38;2;[0-9;]+m\x1b\[48;2;[0-9;]+m▀`)

func halfBlockCells(row string) []string {
	cells := halfBlock.FindAllString(row, -1)
	if len(cells) == 0 {
		return []string{" "}
	}
	return cells
}

// bandLine fades the selection band in on the row the cursor moved to, and
// out on the row it left.
func (m Model) bandLine(line string, entering bool) string {
	t := easeOut(m.progress(m.bandAt, bandLength))
	if t >= 1 {
		return line
	}
	k := t
	if !entering {
		k = 1 - t
	}
	th := themes[m.themeIndex]
	bg, hi := hexColor(th.bg), hexColor(th.highlight)
	return recolor(line, func(c [3]int, isBackground bool) [3]int {
		if isBackground && c == hi {
			return blend(bg, hi, k)
		}
		return c
	})
}
