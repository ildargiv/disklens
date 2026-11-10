package scanner

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

// buildTree creates a small known tree:
//
//	root/
//	  dir1/ file1 (1000 B), file2 (2000 B)
//	  dir1/sub/ file3 (500 B)
//	  dir2/            (empty)
func buildTree(t *testing.T) string {
	t.Helper()
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
	mk(filepath.Join("dir1", "file1"), 1000)
	mk(filepath.Join("dir1", "file2"), 2000)
	mk(filepath.Join("dir1", "sub", "file3"), 500)
	if err := os.MkdirAll(filepath.Join(root, "dir2"), 0o755); err != nil {
		t.Fatal(err)
	}
	return root
}

func TestScanTotals(t *testing.T) {
	for _, workers := range []int{1, 4, 64} {
		root := buildTree(t)
		got, err := Scan(root, Options{Workers: workers})
		if err != nil {
			t.Fatal(err)
		}
		if got.Size != 3500 {
			t.Errorf("workers=%d: Size = %d, want 3500", workers, got.Size)
		}
		if got.Files != 3 {
			t.Errorf("workers=%d: Files = %d, want 3", workers, got.Files)
		}
		if got.Dirs != 3 { // dir1, dir1/sub, dir2
			t.Errorf("workers=%d: Dirs = %d, want 3", workers, got.Dirs)
		}
		if got.Errors != 0 {
			t.Errorf("workers=%d: Errors = %d, want 0", workers, got.Errors)
		}
		// child aggregates must be correct too
		var dir1 *Node
		for _, c := range got.Children {
			if c.Name == "dir1" {
				dir1 = c
			}
		}
		if dir1 == nil || dir1.Size != 3500 || dir1.Files != 3 || dir1.Dirs != 1 {
			t.Errorf("workers=%d: dir1 aggregate wrong: %+v", workers, dir1)
		}
	}
}

func TestScanEmptyRoot(t *testing.T) {
	got, err := Scan(t.TempDir(), Options{})
	if err != nil {
		t.Fatal(err)
	}
	if got.Size != 0 || got.Files != 0 || got.Dirs != 0 {
		t.Errorf("empty root: got %+v, want zeros", got)
	}
}

func TestScanNotDir(t *testing.T) {
	root := buildTree(t)
	f := filepath.Join(root, "dir1", "file1")
	if _, err := Scan(f, Options{}); err == nil {
		t.Error("Scan(file) should return an error")
	}
}

func TestScanMissingRoot(t *testing.T) {
	if _, err := Scan(filepath.Join(t.TempDir(), "nope"), Options{}); err == nil {
		t.Error("Scan(missing) should return an error")
	}
}

func TestSymlinksSkippedByDefault(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("creating symlinks on Windows needs privileges or developer mode")
	}
	root := buildTree(t)
	real := filepath.Join(root, "dir1", "file1")
	link := filepath.Join(root, "link-to-file")
	if err := os.Symlink(real, link); err != nil {
		t.Skipf("symlink not supported: %v", err)
	}
	got, err := Scan(root, Options{})
	if err != nil {
		t.Fatal(err)
	}
	if got.Files != 3 || got.Size != 3500 {
		t.Errorf("symlink should not be counted by default: got %d files / %d bytes", got.Files, got.Size)
	}
}

func TestDeniedDirectory(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("chmod-based denial does not work on Windows")
	}
	root := buildTree(t)
	denied := filepath.Join(root, "dir2")
	if err := os.Chmod(denied, 0o000); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(denied, 0o755) })
	got, err := Scan(root, Options{})
	if err != nil {
		t.Fatal(err)
	}
	if got.Errors != 1 || !got.HasError {
		t.Errorf("denied dir should be reported: errors=%d hasError=%v", got.Errors, got.HasError)
	}
	if got.Size != 3500 {
		t.Errorf("scan should continue past denied dir: Size = %d, want 3500", got.Size)
	}
}
