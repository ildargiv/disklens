package scanner

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"testing"
)

func TestHardLinksCountedOnce(t *testing.T) {
	root := buildTree(t)
	orig := filepath.Join(root, "dir1", "file2") // 2000 B
	if err := os.Link(orig, filepath.Join(root, "dir2", "file2-link")); err != nil {
		t.Skipf("hard links not supported here: %v", err)
	}
	got, err := Scan(root, Options{KeepFiles: true})
	if err != nil {
		t.Fatal(err)
	}
	if got.Size != 3500 {
		t.Errorf("hard-linked bytes must count once: Size = %d, want 3500", got.Size)
	}
	if got.Files != 4 {
		t.Errorf("every link is still a listed file: Files = %d, want 4", got.Files)
	}
	links := 0
	var walk func(*Node)
	walk = func(n *Node) {
		for _, f := range n.FileEntries {
			if f.HardLink {
				links++
			}
		}
		for _, c := range n.Children {
			walk(c)
		}
	}
	walk(got)
	if links != 1 {
		t.Errorf("exactly one entry should be flagged HardLink, got %d", links)
	}

	all, err := Scan(root, Options{CountLinks: true})
	if err != nil {
		t.Fatal(err)
	}
	if all.Size != 5500 {
		t.Errorf("CountLinks should count every link: Size = %d, want 5500", all.Size)
	}
}

func TestExclude(t *testing.T) {
	root := buildTree(t)
	got, err := Scan(root, Options{Exclude: []string{"sub", "file1"}})
	if err != nil {
		t.Fatal(err)
	}
	if got.Size != 2000 || got.Files != 1 {
		t.Errorf("exclude by name: size=%d files=%d, want 2000/1", got.Size, got.Files)
	}
	got, err = Scan(root, Options{Exclude: []string{filepath.Join(root, "dir1", "*")}})
	if err != nil {
		t.Fatal(err)
	}
	if got.Size != 0 {
		t.Errorf("exclude by full path: size=%d, want 0", got.Size)
	}
}

func TestRemoveFileUpdatesTotals(t *testing.T) {
	root := buildTree(t)
	got, err := Scan(root, Options{KeepFiles: true})
	if err != nil {
		t.Fatal(err)
	}
	var dir1 *Node
	for _, c := range got.Children {
		if c.Name == "dir1" {
			dir1 = c
		}
	}
	if dir1 == nil || !RemoveChild(dir1, "file2") {
		t.Fatal("could not remove dir1/file2")
	}
	Reaggregate(got)
	if got.Size != 1500 || got.Files != 2 {
		t.Errorf("after removing a file: size=%d files=%d, want 1500/2", got.Size, got.Files)
	}
}

func TestJunctionNotCountedOrDescended(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("junctions are a Windows concept")
	}
	root := buildTree(t)
	link := filepath.Join(root, "junction-to-dir1")
	if out, err := exec.Command("cmd", "/c", "mklink", "/J", link, filepath.Join(root, "dir1")).CombinedOutput(); err != nil {
		t.Skipf("mklink /J failed: %v %s", err, out)
	}
	got, err := Scan(root, Options{DiskUsage: true, KeepFiles: true})
	if err != nil {
		t.Fatal(err)
	}
	if got.Files != 3 || got.Size != 3500 || got.Dirs != 3 {
		t.Errorf("junction must be neither a file nor descended: files=%d size=%d dirs=%d, want 3/3500/3",
			got.Files, got.Size, got.Dirs)
	}
	if got.Links != 1 {
		t.Errorf("skipped junction should be reported: Links=%d, want 1", got.Links)
	}
}

func TestFastAndGenericListingAgree(t *testing.T) {
	root := buildTree(t)
	dir := filepath.Join(root, "dir1")
	fast, err := listDir(dir, true)
	if err != nil {
		t.Fatal(err)
	}
	gen, err := listDirGeneric(dir, true)
	if err != nil {
		t.Fatal(err)
	}
	type sum struct {
		files, dirs int
		bytes       int64
	}
	tally := func(es []dirEnt) (s sum) {
		for _, e := range es {
			switch e.kind {
			case entFile:
				s.files++
				s.bytes += e.size
				if e.mtime == 0 {
					t.Errorf("%s: missing mtime", e.name)
				}
			case entDir:
				s.dirs++
			}
		}
		return
	}
	if a, b := tally(fast), tally(gen); a != b {
		t.Errorf("listings disagree: fast=%+v generic=%+v", a, b)
	}
}

func TestDiskUsageLargeFile(t *testing.T) {
	root := t.TempDir()
	f := filepath.Join(root, "big.bin")
	if err := os.WriteFile(f, make([]byte, 1<<20), 0o644); err != nil {
		t.Fatal(err)
	}
	got, err := Scan(root, Options{DiskUsage: true})
	if err != nil {
		t.Fatal(err)
	}
	// Allocated size must be measured (not zero) and close to the real size.
	if got.Disk < 1<<19 || got.Disk > 2<<20 {
		t.Errorf("disk size of a 1 MiB file = %d, want roughly 1 MiB", got.Disk)
	}
}
