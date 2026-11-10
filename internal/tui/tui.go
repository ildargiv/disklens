// Package tui implements an ncdu-style interactive browser on top of the
// scanner: live scan progress, arrow/vim navigation, size bars, sorting.
// Read-only by design; deletion lives in the MCP tools and will arrive here
// behind a confirmation prompt.
package tui

import (
	"fmt"
	"path/filepath"
	"strings"
	"time"

	"github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"disklens/internal/scanner"
)

// Run scans startDir (with live progress) and opens the interactive browser.
func Run(startDir string) error {
	m := &model{startDir: startDir, sortBy: "size", scanning: true}
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

type model struct {
	startDir string
	p        *tea.Program

	root   *scanner.Node
	stack  []*scanner.Node
	items  []item
	cursor int
	offset int
	sortBy string // size | name | files

	width, height int
	scanning      bool
	stats         scanner.ProgressState
	err           error
}

var (
	headerStyle     = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("12"))
	selected        = lipgloss.NewStyle().Background(lipgloss.Color("237"))
	dimStyle        = lipgloss.NewStyle().Foreground(lipgloss.Color("243"))
	fileStyle       = lipgloss.NewStyle().Foreground(lipgloss.Color("250"))
	errStyle        = lipgloss.NewStyle().Foreground(lipgloss.Color("1"))
	warnStyle       = lipgloss.NewStyle().Foreground(lipgloss.Color("3"))
	barFill, barGap = "█", "░"
)

func (m *model) Init() tea.Cmd {
	return func() tea.Msg {
		var last time.Time // touched only by this goroutine (the Progress callback)
		opts := scanner.Options{
			DiskUsage: true,
			KeepFiles: true,
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
	case tea.KeyMsg:
		if m.err != nil {
			return m, tea.Quit
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
	m.items = buildItems(m.current(), m.sortBy)
	m.cursor, m.offset = 0, 0
}

func (m *model) View() string {
	if m.err != nil {
		return errStyle.Render("disklens: "+m.err.Error()) + "\n\npress q to quit\n"
	}
	if m.scanning {
		return headerStyle.Render("disklens scanning "+m.startDir) + "\n\n" +
			fmt.Sprintf("  %s dirs scanned, %s files, %s so far…\n\n",
				humanInt(m.stats.DirsDone), humanInt(m.stats.Files), humanSize(m.stats.Bytes)) +
			dimStyle.Render("esc or q stops early? not yet — v0 waits for the scan") + "\n"
	}

	cur := m.current()
	var b strings.Builder
	b.WriteString(headerStyle.Render(filepath.Base(filepath.Clean(cur.Path))+" — "+humanSize(cur.Disk)) +
		dimStyle.Render("  [sort: "+m.sortBy+"]") + "\n")
	b.WriteString(dimStyle.Render(cur.Path) + "\n")

	rows := m.height - 5
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
		b.WriteString(dimStyle.Render("  (empty)") + "\n")
	}

	b.WriteString("\n")
	b.WriteString(dimStyle.Render("↑↓ navigate  enter open dir  h/esc parent  s size  n name  c count  q quit") + "\n")
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
