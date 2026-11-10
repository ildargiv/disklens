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

func TestClassifyMLRules(t *testing.T) {
	// Synthetic tree: generic names like "models" must only classify when the
	// full path looks like ML tooling (pathAny context check).
	root := &scanner.Node{
		Name: "Pod", Path: `C:\Users\Pod`,
		Children: []*scanner.Node{
			{Name: "Comfy-Desktop", Path: `C:\Users\Pod\Comfy-Desktop`, Children: []*scanner.Node{
				{Name: "ComfyUI", Path: `C:\Users\Pod\Comfy-Desktop\ComfyUI`, Children: []*scanner.Node{
					{Name: "models", Path: `C:\Users\Pod\Comfy-Desktop\ComfyUI\models`, Disk: 90 << 30, Files: 40},
					{Name: "custom_nodes", Path: `C:\Users\Pod\Comfy-Desktop\ComfyUI\custom_nodes`, Disk: 2 << 30},
					{Name: "input", Path: `C:\Users\Pod\Comfy-Desktop\ComfyUI\input`, Disk: 1 << 30},
				}},
			}},
			{Name: "AppData", Path: `C:\Users\Pod\AppData`, Children: []*scanner.Node{
				{Name: "ollama", Path: `C:\Users\Pod\AppData\ollama`, Children: []*scanner.Node{
					{Name: "models", Path: `C:\Users\Pod\AppData\ollama\models`, Disk: 20 << 30},
				}},
			}},
			// A game's "models" directory must NOT be treated as ML models.
			{Name: "MyGame", Path: `C:\Users\Pod\MyGame`, Children: []*scanner.Node{
				{Name: "models", Path: `C:\Users\Pod\MyGame\models`, Disk: 3 << 30, Children: []*scanner.Node{
					{Name: "checkpoints", Path: `C:\Users\Pod\MyGame\models\checkpoints`, Disk: 1 << 30},
				}},
			}},
		},
	}

	got := Classify(root)
	status := map[string]string{}
	for _, c := range got {
		status[c.Path] = c.Status
	}
	want := map[string]string{
		`C:\Users\Pod\Comfy-Desktop\ComfyUI\models`:       "review_first",
		`C:\Users\Pod\Comfy-Desktop\ComfyUI\custom_nodes`: "review_first",
		`C:\Users\Pod\Comfy-Desktop\ComfyUI\input`:        "review_first",
		`C:\Users\Pod\AppData\ollama\models`:              "review_first",
	}
	for path, w := range want {
		if status[path] != w {
			t.Errorf("%s: got %q, want %q (all: %+v)", path, status[path], w, got)
		}
	}
	for _, bad := range []string{
		`C:\Users\Pod\MyGame\models`,
		`C:\Users\Pod\MyGame\models\checkpoints`,
	} {
		if _, found := status[bad]; found {
			t.Errorf("%s must not be classified as ML (no ML context in path)", bad)
		}
	}
	if len(status) != len(want) {
		t.Errorf("unexpected candidate set: %+v", got)
	}
}
