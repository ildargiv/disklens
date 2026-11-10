package tui

import (
	"strings"
	"testing"

	"github.com/charmbracelet/bubbletea"

	"disklens/internal/scanner"
)

func testRoot() *scanner.Node {
	big := &scanner.Node{
		Name: "big", Path: "/t/big", Size: 4000, Disk: 5000, Files: 2,
		FileEntries: []scanner.FileEntry{
			{Name: "a.bin", Apparent: 2500, Disk: 3000},
			{Name: "b.bin", Apparent: 1500, Disk: 2000},
		},
	}
	small := &scanner.Node{Name: "small", Path: "/t/small", Size: 50, Disk: 100, Files: 1}
	root := &scanner.Node{
		Name: "t", Path: "/t", Children: []*scanner.Node{big, small},
		FileEntries: []scanner.FileEntry{{Name: "rootfile.bin", Apparent: 300, Disk: 300}},
	}
	return root
}

func names(items []item) []string {
	out := make([]string, len(items))
	for i, it := range items {
		out[i] = it.name
	}
	return out
}

func TestBuildItemsSize(t *testing.T) {
	got := names(buildItems(testRoot(), "size"))
	want := []string{"big", "rootfile.bin", "small"} // 5000 > 300 > 100, dirs+files mixed
	if len(got) != 3 || got[0] != want[0] || got[1] != want[1] || got[2] != want[2] {
		t.Errorf("size order = %v, want %v", got, want)
	}
}

func TestBuildItemsName(t *testing.T) {
	got := names(buildItems(testRoot(), "name"))
	want := []string{"big", "small", "rootfile.bin"} // dirs first alphabetically, then files
	if len(got) != 3 || got[0] != want[0] || got[1] != want[1] || got[2] != want[2] {
		t.Errorf("name order = %v, want %v", got, want)
	}
}

func TestBuildItemsFiles(t *testing.T) {
	got := names(buildItems(testRoot(), "files"))
	want := []string{"big", "rootfile.bin", "small"} // files 2 > 1, ties by size
	if len(got) != 3 || got[0] != want[0] || got[1] != want[1] || got[2] != want[2] {
		t.Errorf("files order = %v, want %v", got, want)
	}
}

func newBrowsedModel() *model {
	m := &model{sortBy: "size", stack: []*scanner.Node{testRoot()}, width: 80, height: 24}
	m.rebuild()
	return m
}

func key(m *model, km tea.KeyMsg) tea.Cmd {
	_, cmd := m.Update(km)
	return cmd
}

func TestNavigation(t *testing.T) {
	m := newBrowsedModel()
	if len(m.items) != 3 {
		t.Fatalf("items = %d, want 3", len(m.items))
	}

	key(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("j")})
	if m.cursor != 1 {
		t.Fatalf("cursor after j = %d, want 1", m.cursor)
	}
	key(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("k")})
	if m.cursor != 0 {
		t.Fatalf("cursor after k = %d, want 0", m.cursor)
	}

	// enter the first item (big, a directory)
	key(m, tea.KeyMsg{Type: tea.KeyEnter})
	if len(m.stack) != 2 || m.current().Name != "big" {
		t.Fatalf("enter failed: stack=%d current=%v", len(m.stack), m.current().Name)
	}
	if len(m.items) != 2 { // a.bin + b.bin
		t.Errorf("items in big = %v", names(m.items))
	}

	// esc goes to parent
	key(m, tea.KeyMsg{Type: tea.KeyEsc})
	if len(m.stack) != 1 || m.current().Name != "t" {
		t.Errorf("esc failed: stack=%d", len(m.stack))
	}

	// esc at root quits
	if cmd := key(m, tea.KeyMsg{Type: tea.KeyEsc}); cmd == nil {
		t.Error("esc at root should return a quit command")
	} else if _, ok := cmd().(tea.QuitMsg); !ok {
		t.Error("esc at root should return QuitMsg")
	}
}

func TestSortKeysAndQuit(t *testing.T) {
	m := newBrowsedModel()
	key(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("n")})
	if m.sortBy != "name" || names(m.items)[0] != "big" || names(m.items)[1] != "small" {
		t.Errorf("sort n wrong: %s %v", m.sortBy, names(m.items))
	}
	if cmd := key(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("q")}); cmd == nil {
		t.Error("q should return a quit command")
	}
}

func TestTruncate(t *testing.T) {
	if got := truncate("abcdef", 4); got != "abc…" {
		t.Errorf("truncate = %q", got)
	}
	if got := truncate("ab", 10); got != "ab" {
		t.Errorf("truncate short = %q", got)
	}
}

func TestViewRendersRows(t *testing.T) {
	m := newBrowsedModel()
	view := m.View()
	for _, want := range []string{"big", "small", "rootfile.bin", "4.9 KiB"} {
		if !strings.Contains(view, want) {
			t.Errorf("view missing %q:\n%s", want, view)
		}
	}
}
