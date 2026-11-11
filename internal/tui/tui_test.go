package tui

import (
	"strings"
	"testing"

	"github.com/charmbracelet/bubbletea"

	"disklens/internal/recycle"
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
	got := names(buildItems(testRoot(), "size", "", false))
	want := []string{"big", "rootfile.bin", "small"} // 5000 > 300 > 100, dirs+files mixed
	if len(got) != 3 || got[0] != want[0] || got[1] != want[1] || got[2] != want[2] {
		t.Errorf("size order = %v, want %v", got, want)
	}
}

func TestBuildItemsName(t *testing.T) {
	got := names(buildItems(testRoot(), "name", "", false))
	want := []string{"big", "small", "rootfile.bin"} // dirs first alphabetically, then files
	if len(got) != 3 || got[0] != want[0] || got[1] != want[1] || got[2] != want[2] {
		t.Errorf("name order = %v, want %v", got, want)
	}
}

func TestBuildItemsFiles(t *testing.T) {
	got := names(buildItems(testRoot(), "files", "", false))
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

	// esc at root must NOT quit (an accidental key must not discard a scan)
	if cmd := key(m, tea.KeyMsg{Type: tea.KeyEsc}); cmd != nil {
		t.Error("esc at root should not return a command")
	}
	if m.errLine == nil {
		t.Error("esc at root should hint that q quits")
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

func TestFilterFlow(t *testing.T) {
	m := newBrowsedModel()
	key(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("/")})
	if !m.typing {
		t.Fatal("slash should start filter typing")
	}
	key(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("sm")})
	if got := names(m.items); len(got) != 1 || got[0] != "small" {
		t.Errorf("filter 'sm' -> %v, want [small]", got)
	}
	key(m, tea.KeyMsg{Type: tea.KeyBackspace})
	if got := names(m.items); len(got) != 1 || got[0] != "small" {
		t.Errorf("filter 's' -> %v, want [small]", got)
	}
	key(m, tea.KeyMsg{Type: tea.KeyEsc})
	if m.filter != "" || m.typing || len(m.items) != 3 {
		t.Errorf("esc should clear filter: filter=%q typing=%v items=%d", m.filter, m.typing, len(m.items))
	}
}

func TestConfirmFlow(t *testing.T) {
	m := newBrowsedModel()
	key(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("d")})
	if m.confirm == nil || m.confirm.item.name != "big" {
		t.Fatalf("d should open confirm on cursor item: %+v", m.confirm)
	}
	// any key other than y cancels
	key(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("n")})
	if m.confirm != nil {
		t.Error("n should cancel the confirm prompt")
	}
	// protected path: guard must refuse before the recycle call
	m2 := newBrowsedModel()
	gitNode := &scanner.Node{Name: ".git", Path: `C:\t\.git`, Disk: 10}
	m2.stack[0].Children = append(m2.stack[0].Children, gitNode)
	m2.rebuild()
	for i, it := range m2.items {
		if it.name == ".git" {
			m2.cursor = i
		}
	}
	key(m2, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("d")})
	if m2.confirm == nil {
		t.Fatal("confirm should open for .git item (guard runs on y)")
	}
	cmd := key(m2, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("y")})
	if cmd != nil {
		t.Error("guard must refuse deleting .git before touching the Recycle Bin")
	}
	if m2.errLine == nil {
		t.Error("guard refusal should be reported to the user")
	}
	if m2.confirm != nil {
		t.Error("confirm should be resolved after y attempt")
	}
}

func TestRecycleBinStatusAndEmptyConfirm(t *testing.T) {
	m := newBrowsedModel()
	// x is inert until the bin status is known
	key(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("x")})
	if m.confirm != nil {
		t.Fatal("x should do nothing before the Recycle Bin status is known")
	}
	m.Update(recycleMsg{info: recycle.QueryInfo{SizeBytes: 1234, Items: 3}})
	if !m.recKnown || m.rec.Items != 3 {
		t.Fatalf("recycleMsg not applied: known=%v %+v", m.recKnown, m.rec)
	}
	view := m.View()
	if !strings.Contains(view, "Recycle Bin") || !strings.Contains(view, "x = empty it") {
		t.Errorf("view should show Recycle Bin status line:\n%s", view)
	}

	key(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("x")})
	if m.confirm == nil || m.confirm.kind != "empty" {
		t.Fatalf("x should open an empty-bin confirm: %+v", m.confirm)
	}
	view = m.View()
	if !strings.Contains(view, "PERMANENTLY") {
		t.Errorf("empty confirm must warn about permanent deletion:\n%s", view)
	}
	// any key cancels
	key(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("n")})
	if m.confirm != nil {
		t.Error("n should cancel the empty-bin prompt")
	}
	// y arms the command (we do NOT execute it: the test must never touch the
	// real Recycle Bin), and the prompt resolves.
	key(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("x")})
	cmd := key(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("y")})
	if cmd == nil {
		t.Error("y on empty-confirm should arm the empty-bin command")
	}
	if m.confirm != nil {
		t.Error("confirm should resolve after y")
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

func TestApparentToggleAndHelp(t *testing.T) {
	m := newBrowsedModel()
	key(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("j")})
	key(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("a")})
	if !m.apparent || m.items[0].size != 4000 {
		t.Errorf("a should switch to apparent sizes: apparent=%v first=%+v", m.apparent, m.items[0])
	}
	if m.cursor != 1 {
		t.Errorf("toggling sizes must keep the cursor, got %d", m.cursor)
	}
	key(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("?")})
	if !m.help || !strings.Contains(m.View(), "Recycle Bin") {
		t.Error("? should open the help overlay")
	}
	if cmd := key(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("j")}); cmd != nil || m.help {
		t.Error("any key should close help without acting")
	}
	if !strings.Contains(m.View(), "KopiTehnik") {
		t.Error("header should carry the product name")
	}
}
