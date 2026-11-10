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

	"disklens/internal/recycle"
	"disklens/internal/scanner"
)

// Run scans startDir (with live progress) and opens the interactive browser.
func Run(startDir string) error {
	m := &model{startDir: startDir, sortBy: "size", scanning: true, cancel: make(chan struct{})}
	p := tea.NewProgram(m, tea.WithAltScreen())
	m.p = p
	_, err := p.Run()
	return err
}

type progressMsg struct{ stats scanner.ProgressState }

type scanDoneMsg struct {
	root *scanner.Node
	err  error
}

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
	p        *tea.Program

	root   *scanner.Node
	stack  []*scanner.Node
	items  []item
	cursor int
	offset int
	sortBy string // size | name | files
	filter string
	typing bool

	cancel   chan struct{}
	canceled bool

	scanning bool
	stats    scanner.ProgressState
	confirm  *confirmState
	errLine  error
	err      error

	recKnown bool
	rec      recycle.QueryInfo

	width, height int
}

var (
	headerStyle = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("12"))
	selected    = lipgloss.NewStyle().Background(lipgloss.Color("237"))
	dimStyle    = lipgloss.NewStyle().Foreground(lipgloss.Color("243"))
	fileStyle   = lipgloss.NewStyle().Foreground(lipgloss.Color("250"))
	errStyle    = lipgloss.NewStyle().Foreground(lipgloss.Color("1"))
	warnStyle   = lipgloss.NewStyle().Foreground(lipgloss.Color("3"))
	barFill     = "█"
	barGap      = "░"
)

func (m *model) Init() tea.Cmd {
	return tea.Batch(m.scanCmd(), m.recycleCmd())
}

func (m *model) scanCmd() tea.Cmd {
	return func() tea.Msg {
		var last time.Time // touched only by this goroutine (the Progress callback)
		opts := scanner.Options{
			DiskUsage: true,
			KeepFiles: true,
			Cancel:    m.cancel,
			Progress: func(s scanner.ProgressState) {
				if time.Since(last) < 120*time.Millisecond {
					return
				}
				last = time.Now()
				m.p.Send(progressMsg{stats: s})
			},
		}
		root, err := scanner.Scan(m.startDir, opts)
		return scanDoneMsg{root: root, err: err}
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
		if msg.err != nil {
			m.err = msg.err
			return m, nil
		}
		m.root = msg.root
		m.stack = []*scanner.Node{msg.root}
		m.rebuild()
	case deleteDoneMsg:
		m.confirm = nil
		if msg.err != nil {
			m.errLine = msg.err
			return m, nil
		}
		scanner.RemoveChild(msg.parent, msg.name)
		scanner.Reaggregate(m.root)
		m.rebuild()
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
	case "esc", "left", "h":
		if len(m.stack) > 1 {
			m.stack = m.stack[:len(m.stack)-1]
			m.rebuild()
		} else {
			return m, tea.Quit
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
	case "d":
		if len(m.items) > 0 {
			it := m.items[m.cursor]
			path := ""
			if it.isDir && it.node != nil {
				path = it.node.Path
			} else {
				path = filepath.Join(m.current().Path, it.name)
			}
			m.confirm = &confirmState{kind: "item", item: it, parent: m.current(), path: path}
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
		m.rebuild()
	}
}

func (m *model) rebuild() {
	m.items = buildItems(m.current(), m.sortBy, m.filter)
	m.cursor, m.offset = 0, 0
}

func (m *model) View() string {
	if m.err != nil {
		return errStyle.Render("disklens: "+m.err.Error()) + "\n\npress q to quit\n"
	}
	if m.scanning {
		var b strings.Builder
		b.WriteString(headerStyle.Render("disklens scanning "+m.startDir) + "\n\n")
		b.WriteString(fmt.Sprintf("  %s dirs scanned, %s files, %s so far\n\n",
			humanInt(m.stats.DirsDone), humanInt(m.stats.Files), humanSize(m.stats.Bytes)))
		if m.canceled {
			b.WriteString(dimStyle.Render("stopping… keeping what was found so far") + "\n")
		} else {
			b.WriteString(dimStyle.Render("esc = stop early and browse what was found  q = quit") + "\n")
		}
		return b.String()
	}

	cur := m.current()
	var b strings.Builder
	title := filepath.Base(filepath.Clean(cur.Path)) + " — " + humanSize(cur.Disk)
	if m.canceled {
		title += " (partial scan)"
	}
	b.WriteString(headerStyle.Render(title) + dimStyle.Render("  [sort: "+m.sortBy+"]") + "\n")
	b.WriteString(dimStyle.Render(cur.Path) + "\n")
	if m.filter != "" {
		f := m.filter
		if m.typing {
			f += "…"
		}
		b.WriteString(dimStyle.Render("filter: "+f) + "\n")
	}

	rows := m.height - 6
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
	nameWidth := m.width - 34
	if nameWidth < 8 {
		nameWidth = 8
	}

	for i := m.offset; i < len(m.items) && i < m.offset+rows; i++ {
		it := m.items[i]
		marker := "   "
		line := fmt.Sprintf("%9s %s %s", humanSize(it.size), bar(it.size, cur.Disk), truncate(it.name, nameWidth))
		if it.isDir {
			line += dimStyle.Render(fmt.Sprintf(" (%s f, %s d)", humanInt(it.files), humanInt(it.dirs)))
		} else {
			line = fileStyle.Render(line)
		}
		if it.isDir && it.node != nil && it.node.HasError {
			line += warnStyle.Render(" !")
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
		b.WriteString(dimStyle.Render("↑↓ navigate  enter open  h/esc parent  / filter  s/n/c sort  d recycle  q quit") + "\n")
	}
	return b.String()
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
