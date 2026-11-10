package cleanup

import (
	"os"
	"path/filepath"
	"testing"

	"disklens/internal/scanner"
)

func TestClassify(t *testing.T) {
	root := t.TempDir()
	mk := func(rel string, size int) {
		p := filepath.Join(root, rel)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, make([]byte, size), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	mk(filepath.Join("pkg", "package.json"), 10)
	mk(filepath.Join("pkg", "node_modules", "x.js"), 1000)
	mk(filepath.Join("py", "requirements.txt"), 10)
	mk(filepath.Join("py", ".venv", "lib.bin"), 500)
	mk(filepath.Join("other", "node_modules", "y.js"), 900) // no package.json next to it
	mk(filepath.Join("app", "__pycache__", "z.pyc"), 100)
	mk(filepath.Join("misc", "build", "b.bin"), 100)
	mk(filepath.Join("repo", ".git", "config"), 10)

	scanRoot, err := scanner.Scan(root, scanner.Options{DiskUsage: true})
	if err != nil {
		t.Fatal(err)
	}

	got := Classify(scanRoot)
	status := map[string]string{} // relative path suffix -> status
	for _, c := range got {
		rel, _ := filepath.Rel(root, c.Path)
		status[filepath.ToSlash(rel)] = c.Status
	}

	want := map[string]string{
		"pkg/node_modules":   "safe",
		"py/.venv":           "safe",
		"app/__pycache__":    "safe",
		"misc/build":         "review_first",
		"other/node_modules": "", // must NOT be classified: no package.json marker
		"repo/.git":          "", // must never appear
	}
	for rel, w := range want {
		g, found := status[rel]
		if w == "" {
			if found {
				t.Errorf("%s should not be classified, got status %q", rel, g)
			}
			continue
		}
		if !found {
			t.Errorf("%s missing from candidates; got %+v", rel, got)
			continue
		}
		if g != w {
			t.Errorf("%s status = %q, want %q", rel, g, w)
		}
	}
	if len(status) != 4 {
		t.Errorf("unexpected candidate set: %+v", status)
	}
	// sorted by disk size, largest first
	for i := 1; i < len(got); i++ {
		if got[i-1].Disk < got[i].Disk {
			t.Errorf("candidates not sorted by size: %+v", got)
			break
		}
	}
}
