package dupes

import (
	"testing"

	"disklens/internal/scanner"
)

func TestFind(t *testing.T) {
	root := &scanner.Node{
		Name: "photos", Path: "/photos",
		FileEntries: []scanner.FileEntry{
			{Name: "big1.jpg", Apparent: 5000},
			{Name: "big2.jpg", Apparent: 5000}, // duplicate pair, 5000 wasted
		},
		Children: []*scanner.Node{
			{Name: "sub", Path: "/photos/sub", FileEntries: []scanner.FileEntry{
				{Name: "copy.jpg", Apparent: 5000},
				{Name: "small.jpg", Apparent: 900},
			}},
			{Name: "tiny", Path: "/photos/tiny", FileEntries: []scanner.FileEntry{
				{Name: "t1", Apparent: 100}, {Name: "t2", Apparent: 100},
			}},
		},
	}

	groups := Find(root, 1000, 10)
	if len(groups) != 1 {
		t.Fatalf("groups = %d, want 1 (minBytes should exclude the 100 B pair)", len(groups))
	}
	g := groups[0]
	if g.Size != 5000 || g.Waste != 10000 {
		t.Errorf("group wrong: %+v", g)
	}
	wantPaths := 3
	if len(g.Paths) != wantPaths {
		t.Errorf("paths = %v, want %d entries", g.Paths, wantPaths)
	}
	for _, p := range g.Paths {
		if p == "" {
			t.Error("empty path in group")
		}
	}
}

func TestFindSkipsGit(t *testing.T) {
	root := &scanner.Node{
		Name: "repo", Path: "/repo",
		FileEntries: []scanner.FileEntry{{Name: "a", Apparent: 7000}},
		Children: []*scanner.Node{
			{Name: ".git", Path: "/repo/.git", FileEntries: []scanner.FileEntry{{Name: "obj", Apparent: 7000}}},
		},
	}
	if groups := Find(root, 1000, 10); len(groups) != 0 {
		t.Errorf("git objects must not be reported as duplicates: %+v", groups)
	}
}
