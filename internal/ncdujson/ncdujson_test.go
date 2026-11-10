package ncdujson

import (
	"bytes"
	"encoding/json"
	"path/filepath"
	"testing"

	"disklens/internal/scanner"
)

func sampleTree() *scanner.Node {
	return &scanner.Node{
		Name: "photos", Path: "/test/photos",
		Size: 3500, Disk: 3500, Files: 3, Dirs: 2,
		FileEntries: []scanner.FileEntry{
			{Name: "a.bin", Apparent: 1000, Disk: 1000},
			{Name: "b.bin", Apparent: 2000, Disk: 2000},
		},
		Children: []*scanner.Node{
			{
				Name: "sub", Path: "/test/photos/sub",
				Size: 500, Disk: 500, Files: 1,
				FileEntries: []scanner.FileEntry{{Name: "c.bin", Apparent: 500, Disk: 500}},
			},
			{Name: "empty", Path: "/test/photos/empty"},
		},
	}
}

func TestExportImportRoundTrip(t *testing.T) {
	var buf bytes.Buffer
	if err := Export(&buf, sampleTree(), ExportOptions{DiskMeasured: true}); err != nil {
		t.Fatal(err)
	}

	got, err := Import(&buf)
	if err != nil {
		t.Fatalf("Import: %v\nexport was: %s", err, buf.String())
	}
	if got.Path != "/test/photos" || got.Name != "photos" {
		t.Errorf("root identity wrong: %+v", got)
	}
	if got.Size != 3500 || got.Files != 3 || got.Dirs != 2 {
		t.Errorf("totals wrong: size=%d files=%d dirs=%d", got.Size, got.Files, got.Dirs)
	}
	if got.Disk != 3500 {
		t.Errorf("Disk = %d, want 3500", got.Disk)
	}
	var sub *scanner.Node
	for _, c := range got.Children {
		if c.Name == "sub" {
			sub = c
		}
	}
	if sub == nil || sub.Files != 1 || sub.Size != 500 {
		t.Errorf("sub wrong: %+v", sub)
	}
	if sub != nil && sub.Path != filepath.Join("/test/photos", "sub") {
		t.Errorf("sub.Path = %q", sub.Path)
	}
}

func TestExportShapeIsNcduCompatible(t *testing.T) {
	var buf bytes.Buffer
	if err := Export(&buf, sampleTree(), ExportOptions{DiskMeasured: true}); err != nil {
		t.Fatal(err)
	}
	var top []any
	if err := json.Unmarshal(buf.Bytes(), &top); err != nil {
		t.Fatalf("export is not valid JSON: %v", err)
	}
	if len(top) != 4 {
		t.Fatalf("top-level array has %d elements, want 4", len(top))
	}
	if v, ok := top[0].(float64); !ok || v != 1 {
		t.Errorf("major version = %v, want 1", top[0])
	}
	meta, ok := top[2].(map[string]any)
	if !ok || meta["progname"] != "disklens" {
		t.Errorf("metadata wrong: %v", top[2])
	}
	dir, ok := top[3].([]any)
	if !ok || len(dir) == 0 {
		t.Fatalf("directory element wrong: %v", top[3])
	}
	head, ok := dir[0].(map[string]any)
	if !ok || head["name"] != "/test/photos" {
		t.Errorf("root infoblock name = %v, want full path", dir[0])
	}
}

// TestImportNcduExample parses the canonical example from the ncdu format
// docs (https://dev.yorhel.nl/ncdu/jsonfmt). Directory self-sizes in infoblocks
// (422 for the root, 10 for EmptyDir) are intentionally not counted by
// disklens, matching how disklens scans compute totals.
func TestImportNcduExample(t *testing.T) {
	const src = `[
	  1, 0,
	  {"progname":"ncdu","progver":"1.9","timestamp":1354477149},
	  [
	    {"name":"/media/harddrive","dsize":4096,"asize":422,"dev":39123423,"ino":29342345},
	    {"name":"SomeFile","dsize":32768,"asize":32414,"ino":91245479284},
	    [{"name":"EmptyDir","dsize":4096,"asize":10,"ino":3924}]
	  ]
	]`
	got, err := Import(bytes.NewBufferString(src))
	if err != nil {
		t.Fatal(err)
	}
	if got.Path != "/media/harddrive" {
		t.Errorf("root path = %q", got.Path)
	}
	if got.Size != 32414 || got.Disk != 32768 || got.Files != 1 || got.Dirs != 1 {
		t.Errorf("totals wrong: size=%d disk=%d files=%d dirs=%d", got.Size, got.Disk, got.Files, got.Dirs)
	}
}

func TestImportGarbage(t *testing.T) {
	for _, src := range []string{"", "not json", "{}", "[1,0,{}]", `[1,0,{},{}]`} {
		if _, err := Import(bytes.NewBufferString(src)); err == nil {
			t.Errorf("Import(%q) should fail", src)
		}
	}
}
