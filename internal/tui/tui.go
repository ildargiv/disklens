// Package tui implements an ncdu-style interactive browser on top of the
// scanner: live scan progress (esc stops it early and keeps what was found),
// arrow/vim navigation, sorting, filtering, and delete-to-Recycle-Bin behind
// an explicit confirmation prompt.
package tui

import (
	"fmt"
	"path/filepath"
	"strings"
	"time"

	"github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"disklens/internal/buildinfo"
	"disklens/internal/recycle"
	"disklens/internal/scanner"
)

// Options tunes the scans the browser runs.
type Options struct {
	Exclude    []string // glob patterns skipped by the scanner
	CountLinks bool     // count every hard link separately
}

// Run scans startDir (with live progress) and opens the interactive browser.
func Run(startDir string, opt Options) error {
	m := &model{startDir: startDir, opt: opt, sortBy: "size", scanning: true, scanStart: time.Now(), cancel: make(chan struct{})}
	p := tea.NewProgram(m, tea.WithAltScreen())
	m.p = p
	_, err := p.Run()
	return err
}

type progressMsg struct{ stats scanner.ProgressState }

type scanDoneMsg struct {
	root   *scanner.Node
	target *scanner.Node // non-nil for a rescan of a subtree
	err    error
}

type openDoneMsg struct{ err error }

type deleteDoneMsg struct {
	parent *scanner.Node
	name   string
	path   string
	err    error
}

// confirmState is a pending confirmation prompt: one item to the Recycle Bin,
// or emptying the whole bin.
type confirmState struct {
	kind   string // "item" or "empty"
	item   item
	parent *scanner.Node
	path   string
}

type recycleMsg struct {
	info recycle.QueryInfo
	err  error
}

type emptyDoneMsg struct{ err error }

type model struct {
	startDir string
	opt      Options
	p        *tea.Program

	root     *scanner.Node
	stack    []*scanner.Node
	items    []item
	cursor   int
	offset   int
	sortBy   string // size | name | files
	filter   string
	typing   bool
	apparent bool // show apparent (logical) sizes instead of size on disk
	help     bool

	cancel    chan struct{}
	canceled  bool
	scanning  bool
	scanStart time.Time
	scanPath  string
	rescan    *scanner.Node // node being rescanned (nil = initial scan)

	stats   scanner.ProgressState
	confirm *confirmState
	errLine error
	err     error

	recKnown bool
	rec      recycle.QueryInfo

	width, height int
}

var (
	headerStyle = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("12"))
	selected    = lipgloss.NewStyle().Background(lipgloss.Color("237"))
	dimStyle    = lipgloss.NewStyle().Foreground(lipgloss.Color("243"))
	brandStyle  = lipgloss.NewStyle().Foreground(lipgloss.Color("6"))
	fileStyle   = lipgloss.NewStyle().Foreground(lipgloss.Color("250"))
	errStyle    = lipgloss.NewStyle().Foreground(lipgloss.Color("1"))
	warnStyle   = lipgloss.NewStyle().Foreground(lipgloss.Color("3"))
	barFill     = "█"
	barGap      = "░"
)

func (m *model) Init() tea.Cmd {
	return tea.Batch(m.scanCmd(m.startDir, nil), m.recycleCmd())
}

func (m *model) scanCmd(path string, target *scanner.Node) tea.Cmd {
	m.scanPath = path
	cancel := m.cancel
	return func() tea.Msg {
		var last time.Time // touched only by this goroutine (the Progress callback)
		opts := scanner.Options{
			DiskUsage:  true,
			KeepFiles:  true,
			CountLinks: m.opt.CountLinks,
			Exclude:    m.opt.Exclude,
			Cancel:     cancel,
			Progress: func(s scanner.ProgressState) {
				if time.Since(last) < 120*time.Millisecond {
					return
				}
				last = time.Now()
				m.p.Send(progressMsg{stats: s})
			},
		}
		root, err := scanner.Scan(path, opts)
		return scanDoneMsg{root: root, target: target, err: err}
	}
}

func (m *model) recycleCmd() tea.Cmd {
	return func() tea.Msg {
		info, err := recycle.Query()
		return recycleMsg{info: info, err: err}
	}
}

func (m *model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width, m.height = msg.Width, msg.Height
	case progressMsg:
		m.stats = msg.stats
	case scanDoneMsg:
		m.scanning = false
		m.rescan = nil
		if msg.target != nil { // rescan of the current directory
			if msg.err != nil {
				m.errLine = msg.err
				return m, nil
			}
			scanner.Replace(msg.target, msg.root)
			scanner.Reaggregate(m.root)
			m.refresh()
			m.errLine = fmt.Errorf("rescanned %s", msg.target.Path)
			return m, m.recycleCmd()
		}
		if msg.err != nil {
			m.err = msg.err
			return m, nil
		}
		m.root = msg.root
		m.stack = []*scanner.Node{msg.root}
		m.rebuild()
	case openDoneMsg:
		if msg.err != nil {
			m.errLine = msg.err
		}
	case deleteDoneMsg:
		m.confirm = nil
		if msg.err != nil {
			m.errLine = msg.err
			return m, nil
		}
		scanner.RemoveChild(msg.parent, msg.name)
		scanner.Reaggregate(m.root)
		m.refresh()
		m.errLine = fmt.Errorf("moved to Recycle Bin: %s", msg.name)
		return m, m.recycleCmd()
	case recycleMsg:
		m.rec, m.recKnown, m.errLine = msg.info, msg.err == nil, nil
	case emptyDoneMsg:
		m.confirm = nil
		if msg.err != nil {
			m.errLine = msg.err
			return m, nil
		}
		m.errLine = fmt.Errorf("Recycle Bin emptied")
		return m, m.recycleCmd()
	case tea.KeyMsg:
		return m.handleKey(msg)
	}
	return m, nil
}

func (m *model) handleKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	if m.err != nil {
		return m, tea.Quit
	}
	if m.help {
		m.help = false // any key closes the help overlay
		if msg.String() == "q" || msg.String() == "ctrl+c" {
			return m, tea.Quit
		}
		return m, nil
	}
	if m.confirm != nil {
		switch msg.String() {
		case "y", "Y":
			c := m.confirm
			if c.kind == "empty" {
				m.confirm = nil
				return m, func() tea.Msg { return emptyDoneMsg{err: recycle.EmptyRecycleBin()} }
			}
			if err := recycle.Guard(c.path); err != nil {
				m.confirm = nil
				m.errLine = err
				return m, nil
			}
			return m, func() tea.Msg {
				return deleteDoneMsg{parent: c.parent, name: c.item.name, path: c.path, err: recycle.ToRecycleBin(c.path)}
			}
		default: // n, N, esc, q, anything else: cancel
			m.confirm = nil
			return m, nil
		}
	}
	if m.typing {
		switch msg.String() {
		case "esc":
			m.typing, m.filter = false, ""
			m.rebuild()
		case "enter":
			m.typing = false
		case "backspace":
			r := []rune(m.filter)
			if len(r) > 0 {
				m.filter = string(r[:len(r)-1])
			}
			m.rebuild()
		default:
			if len(msg.Runes) > 0 {
				m.filter += string(msg.Runes)
				m.rebuild()
			}
		}
		return m, nil
	}
	if m.scanning {
		switch msg.String() {
		case "q", "ctrl+c":
			return m, tea.Quit
		case "esc":
			if !m.canceled {
				m.canceled = true
				close(m.cancel)
			}
		}
		return m, nil
	}
	switch msg.String() {
	case "q", "ctrl+c":
		return m, tea.Quit
	case "up", "k":
		if m.cursor > 0 {
			m.cursor--
		}
	case "down", "j":
		if m.cursor < len(m.items)-1 {
			m.cursor++
		}
	case "enter", "right", "l":
		m.enterCurrent()
	case "esc", "left", "h", "backspace":
		if len(m.stack) > 1 {
			from := m.current()
			m.stack = m.stack[:len(m.stack)-1]
			m.filter = ""
			m.rebuild()
			m.selectNode(from)
		} else if m.filter != "" {
			m.filter = ""
			m.rebuild()
		} else {
			// At the top level esc does not quit: an accidental keypress must
			// never throw away a long scan. q quits.
			m.errLine = fmt.Errorf("top level — press q to quit")
		}
	case "home", "g":
		m.cursor = 0
	case "end", "G":
		if len(m.items) > 0 {
			m.cursor = len(m.items) - 1
		}
	case "pgdown", "ctrl+d":
		m.cursor = min(m.cursor+m.pageRows(), max(len(m.items)-1, 0))
	case "pgup", "ctrl+u":
		m.cursor = max(m.cursor-m.pageRows(), 0)
	case "a":
		m.apparent = !m.apparent
		m.refresh()
	case "?":
		m.help = true
	case "r":
		cur := m.current()
		m.scanning, m.canceled = true, false
		m.cancel = make(chan struct{})
		m.scanStart = time.Now()
		m.stats = scanner.ProgressState{}
		m.rescan = cur
		return m, m.scanCmd(cur.Path, cur)
	case "o":
		if path := m.cursorPath(); path != "" {
			return m, func() tea.Msg { return openDoneMsg{err: reveal(path)} }
		}
	case "s":
		m.sortBy = "size"
		m.rebuild()
	case "n":
		m.sortBy = "name"
		m.rebuild()
	case "c":
		m.sortBy = "files"
		m.rebuild()
	case "/":
		m.typing = true
		m.filter = ""
		m.rebuild()
	case "d", "delete":
		if len(m.items) > 0 {
			it := m.items[m.cursor]
			m.confirm = &confirmState{kind: "item", item: it, parent: m.current(), path: m.cursorPath()}
		}
	case "x":
		if m.recKnown {
			m.confirm = &confirmState{kind: "empty"}
		}
	}
	return m, nil
}

func (m *model) current() *scanner.Node { return m.stack[len(m.stack)-1] }

func (m *model) enterCurrent() {
	if m.cursor >= len(m.items) {
		return
	}
	it := m.items[m.cursor]
	if it.isDir && it.node != nil {
		m.stack = append(m.stack, it.node)
		m.filter = ""
		m.rebuild()
	}
}

// cursorPath is the full path of the item under the cursor ("" if none).
func (m *model) cursorPath() string {
	if m.cursor >= len(m.items) {
		return ""
	}
	it := m.items[m.cursor]
	if it.isDir && it.node != nil {
		return it.node.Path
	}
	return filepath.Join(m.current().Path, it.name)
}

// rebuild recomputes the rows and puts the cursor on top.
func (m *model) rebuild() {
	m.items = buildItems(m.current(), m.sortBy, m.filter, m.apparent)
	m.cursor, m.offset = 0, 0
}

// refresh recomputes the rows but keeps the cursor where it was (clamped),
// so deleting or toggling sizes does not throw the user back to the top.
func (m *model) refresh() {
	cur := m.cursor
	m.items = buildItems(m.current(), m.sortBy, m.filter, m.apparent)
	if cur >= len(m.items) {
		cur = len(m.items) - 1
	}
	m.cursor = max(cur, 0)
}

func (m *model) selectNode(n *scanner.Node) {
	for i, it := range m.items {
		if it.node == n {
			m.cursor = i
			return
		}
	}
}

func (m *model) pageRows() int { return max(m.height-8, 1) }

func (m *model) total(n *scanner.Node) int64 {
	if m.apparent {
		return n.Size
	}
	return n.Disk
}

func (m *model) View() string {
	if m.err != nil {
		return errStyle.Render("disklens: "+m.err.Error()) + "\n\npress q to quit\n"
	}
	if m.scanning {
		return m.scanView()
	}
	if m.help {
		return helpView()
	}
	cur := m.current()
	var b strings.Builder
	mode := "on disk"
	if m.apparent {
		mode = "apparent"
	}
	b.WriteString(brandStyle.Render(buildinfo.FullName) + dimStyle.Render("  "+buildinfo.Version) + "\n")
	title := filepath.Base(filepath.Clean(cur.Path)) + " — " + humanSize(m.total(cur)) + " " + mode
	if m.canceled {
		title += " (partial scan)"
	}
	b.WriteString(headerStyle.Render(title) + dimStyle.Render(fmt.Sprintf("  %s files, %s dirs  [sort: %s]",
		humanInt(cur.Files), humanInt(cur.Dirs), m.sortBy)) + "\n")
	b.WriteString(dimStyle.Render(cur.Path) + "\n")
	header := 3
	if m.filter != "" {
		f := m.filter
		if m.typing {
			f += "…"
		}
		b.WriteString(dimStyle.Render("filter: "+f) + "\n")
		header++
	}
	rows := m.height - header - 3
	if m.recKnown {
		rows-- // footer carries an extra Recycle Bin status line
	}
	if rows < 1 {
		rows = 1
	}
	if m.cursor < m.offset {
		m.offset = m.cursor
	}
	if m.cursor >= m.offset+rows {
		m.offset = m.cursor - rows + 1
	}
	nameWidth := m.width - 42
	if nameWidth < 8 {
		nameWidth = 8
	}
	parent := m.total(cur)
	for i := m.offset; i < len(m.items) && i < m.offset+rows; i++ {
		it := m.items[i]
		marker := "  "
		name := it.name
		if it.isDir {
			name += string(filepath.Separator)
		}
		line := fmt.Sprintf("%9s %5s %s %s", humanSize(it.size), percent(it.size, parent), bar(it.size, parent), truncate(name, nameWidth))
		if it.isDir {
			line += dimStyle.Render(fmt.Sprintf(" (%s f, %s d)", humanInt(it.files), humanInt(it.dirs)))
		} else {
			line = fileStyle.Render(line)
			if it.entry != nil && it.entry.HardLink {
				line += dimStyle.Render(" (hard link, counted once)")
			}
		}
		if it.isDir && it.node != nil && it.node.HasError {
			line += warnStyle.Render(" ! unreadable inside")
		}
		if i == m.cursor {
			marker = "> "
			line = selected.Render(line)
		}
		b.WriteString(marker + line + "\n")
	}
	if len(m.items) == 0 {
		if m.filter != "" {
			b.WriteString(dimStyle.Render("  (no matches)") + "\n")
		} else {
			b.WriteString(dimStyle.Render("  (empty)") + "\n")
		}
	}
	b.WriteString("\n")
	switch {
	case m.confirm != nil && m.confirm.kind == "empty":
		b.WriteString(warnStyle.Render(fmt.Sprintf("PERMANENTLY destroy everything in the Recycle Bin?  (%s, %s items)",
			humanSize(m.rec.SizeBytes), humanInt(m.rec.Items))) + "\n")
		b.WriteString(warnStyle.Render("[y] EMPTY IT — files cannot be recovered afterwards  [any other key] cancel") + "\n")
	case m.confirm != nil:
		b.WriteString(warnStyle.Render(fmt.Sprintf("move %q (%s) to the Recycle Bin?  [y] yes  [any other key] cancel",
			m.confirm.item.name, humanSize(m.confirm.item.size))) + "\n")
		b.WriteString(dimStyle.Render("recoverable — the item can be restored from the Recycle Bin") + "\n")
	case m.typing:
		b.WriteString(dimStyle.Render("type to filter  enter keeps filter  esc clears it") + "\n")
	default:
		if m.errLine != nil {
			b.WriteString(warnStyle.Render(m.errLine.Error()) + "\n")
		}
		if m.recKnown {
			recLine := fmt.Sprintf("Recycle Bin: %s, %s items", humanSize(m.rec.SizeBytes), humanInt(m.rec.Items))
			if m.rec.Items > 0 {
				recLine += "  x = empty it"
			}
			b.WriteString(dimStyle.Render(recLine) + "\n")
		}
		b.WriteString(dimStyle.Render("↑↓ move  enter open  esc back  / filter  s/n/c sort  a size  d recycle  r rescan  o explorer  ? help  q quit") + "\n")
	}
	return b.String()
}

func (m *model) scanView() string {
	var b strings.Builder
	b.WriteString(brandStyle.Render(buildinfo.FullName) + dimStyle.Render("  "+buildinfo.Version) + "\n\n")
	verb := "scanning "
	if m.rescan != nil {
		verb = "rescanning "
	}
	b.WriteString(headerStyle.Render(verb+m.scanPath) + "\n\n")
	secs := time.Since(m.scanStart).Seconds()
	rate := ""
	if secs >= 1 {
		rate = fmt.Sprintf("  (%s files/s)", humanInt(int64(float64(m.stats.Files)/secs)))
	}
	b.WriteString(fmt.Sprintf("  %s dirs, %s files, %s so far — %.1fs%s\n\n",
		humanInt(m.stats.DirsDone), humanInt(m.stats.Files), humanSize(m.stats.Bytes), secs, rate))
	if m.canceled {
		b.WriteString(dimStyle.Render("stopping… keeping what was found so far") + "\n")
	} else {
		b.WriteString(dimStyle.Render("esc = stop early and browse what was found  q = quit") + "\n")
	}
	return b.String()
}

func helpView() string {
	keys := [][2]string{
		{"↑/k  ↓/j", "move the cursor"},
		{"g / G, PgUp / PgDn", "jump to top / bottom, page up / down"},
		{"enter / → / l", "open the directory"},
		{"esc / ← / h", "back to the parent directory"},
		{"/", "filter by name (esc clears)"},
		{"s  n  c", "sort by size, name, file count"},
		{"a", "toggle size on disk / apparent size"},
		{"d / Delete", "move to the Recycle Bin (asks first, recoverable)"},
		{"x", "empty the Recycle Bin (asks first, PERMANENT)"},
		{"r", "rescan the current directory"},
		{"o", "show the item in Explorer"},
		{"q / ctrl+c", "quit"},
	}
	var b strings.Builder
	b.WriteString(brandStyle.Render(buildinfo.FullName) + dimStyle.Render("  "+buildinfo.Version) + "\n\n")
	b.WriteString(headerStyle.Render("Keys") + "\n\n")
	for _, k := range keys {
		b.WriteString(fmt.Sprintf("  %-22s %s\n", k[0], k[1]))
	}
	b.WriteString("\n" + dimStyle.Render("Sizes are what deleting would free (size on disk); hard links count once;") + "\n")
	b.WriteString(dimStyle.Render("junctions and symlinks are never followed. Deleting only ever uses the Recycle Bin.") + "\n\n")
	b.WriteString(dimStyle.Render("press any key to close") + "\n")
	return b.String()
}

func percent(size, parent int64) string {
	if parent <= 0 {
		return ""
	}
	p := float64(size) / float64(parent) * 100
	if p >= 99.95 {
		return "100%"
	}
	return fmt.Sprintf("%.1f%%", p)
}

func bar(size, parent int64) string {
	const width = 12
	filled := 0
	if parent > 0 {
		filled = int(float64(size) / float64(parent) * width)
	}
	if filled > width {
		filled = width
	}
	if filled == 0 && size > 0 {
		filled = 1
	}
	return "[" + strings.Repeat(barFill, filled) + strings.Repeat(barGap, width-filled) + "]"
}

func truncate(s string, w int) string {
	r := []rune(s)
	if len(r) <= w {
		return s
	}
	return string(r[:w-1]) + "…"
}

func humanSize(n int64) string {
	const unit = 1024
	if n < unit {
		return fmt.Sprintf("%d B", n)
	}
	div, exp := int64(unit), 0
	for v := n / unit; v >= unit; v /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.1f %ciB", float64(n)/float64(div), "KMGTPE"[exp])
}

func humanInt(n int64) string {
	s := fmt.Sprintf("%d", n)
	if n < 1000 {
		return s
	}
	var parts []string
	for len(s) > 3 {
		parts = append([]string{s[len(s)-3:]}, parts...)
		s = s[:len(s)-3]
	}
	parts = append([]string{s}, parts...)
	return strings.Join(parts, " ")
}
