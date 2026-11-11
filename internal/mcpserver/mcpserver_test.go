package mcpserver

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

func newTestSession(t *testing.T) *mcp.ClientSession {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	t.Cleanup(cancel)

	server := newServer()
	clientT, serverT := mcp.NewInMemoryTransports()
	if _, err := server.Connect(ctx, serverT, nil); err != nil {
		t.Fatal(err)
	}
	client := mcp.NewClient(&mcp.Implementation{Name: "disklens-test", Version: "0"}, nil)
	cs, err := client.Connect(ctx, clientT, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { cs.Close() })
	return cs
}

func call(t *testing.T, cs *mcp.ClientSession, tool string, args map[string]any) (string, bool) {
	t.Helper()
	res, err := cs.CallTool(context.Background(), &mcp.CallToolParams{Name: tool, Arguments: args})
	if err != nil {
		t.Fatalf("CallTool(%s): %v", tool, err)
	}
	txt := ""
	if len(res.Content) > 0 {
		if tc, ok := res.Content[0].(*mcp.TextContent); ok {
			txt = tc.Text
		}
	}
	return txt, res.IsError
}

func testTree(t *testing.T) string {
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
	mk("large.bin", 3000)
	mk("small.bin", 100)
	mk(filepath.Join("sub", "a.bin"), 500)
	if err := os.MkdirAll(filepath.Join(root, "sub", "empty"), 0o755); err != nil {
		t.Fatal(err)
	}
	return root
}

func TestToolsListed(t *testing.T) {
	cs := newTestSession(t)
	res, err := cs.ListTools(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]bool{"scan": false, "top": false, "children": false, "du": false, "classify": false, "delete_to_recycle": false, "empty_recycle_bin": false, "recycle_bin": false, "duplicates": false, "largest_files": false}
	for _, tl := range res.Tools {
		want[tl.Name] = true
	}
	for name, found := range want {
		if !found {
			t.Errorf("tool %q missing from server", name)
		}
	}
}

func TestScanTopChildrenDu(t *testing.T) {
	root := testTree(t)
	cs := newTestSession(t)

	// scan
	txt, isErr := call(t, cs, "scan", map[string]any{"path": root})
	if isErr {
		t.Fatalf("scan error: %s", txt)
	}
	var s summary
	if err := json.Unmarshal([]byte(txt), &s); err != nil {
		t.Fatalf("scan output: %v\n%s", err, txt)
	}
	if s.Apparent != 3600 || s.Files != 3 || s.Dirs != 2 {
		t.Errorf("scan summary wrong: %+v", s)
	}

	// top: the largest single entry must be large.bin
	txt, isErr = call(t, cs, "top", map[string]any{"path": root, "limit": 1})
	if isErr {
		t.Fatalf("top error: %s", txt)
	}
	var cr childrenResult
	if err := json.Unmarshal([]byte(txt), &cr); err != nil {
		t.Fatal(err)
	}
	if len(cr.Entries) != 1 || cr.Entries[0].Name != "large.bin" || cr.Entries[0].Type != "file" {
		t.Errorf("top wrong: %+v", cr.Entries)
	}

	// children of a nested cached dir
	txt, isErr = call(t, cs, "children", map[string]any{"path": filepath.Join(root, "sub")})
	if isErr {
		t.Fatalf("children error: %s", txt)
	}
	if err := json.Unmarshal([]byte(txt), &cr); err != nil {
		t.Fatal(err)
	}
	if len(cr.Entries) != 2 {
		t.Errorf("children(sub) = %+v, want a.bin + empty", cr.Entries)
	}

	// du on a file
	txt, isErr = call(t, cs, "du", map[string]any{"path": filepath.Join(root, "large.bin")})
	if isErr {
		t.Fatalf("du error: %s", txt)
	}
	if err := json.Unmarshal([]byte(txt), &s); err != nil {
		t.Fatal(err)
	}
	if s.Apparent != 3000 || s.Files != 1 {
		t.Errorf("du(file) wrong: %+v", s)
	}
}

func TestTopScansOnDemand(t *testing.T) {
	root := testTree(t)
	cs := newTestSession(t)
	txt, isErr := call(t, cs, "top", map[string]any{"path": root})
	if isErr {
		t.Fatalf("top on an unscanned path should scan on demand, got error: %s", txt)
	}
	var res childrenResult
	if err := json.Unmarshal([]byte(txt), &res); err != nil {
		t.Fatal(err)
	}
	if !res.AutoScanned || len(res.Entries) == 0 || res.Entries[0].Name != "large.bin" {
		t.Errorf("unexpected on-demand result: %+v", res)
	}
	// a second call is answered from the cache
	txt, _ = call(t, cs, "top", map[string]any{"path": root})
	res = childrenResult{}
	_ = json.Unmarshal([]byte(txt), &res)
	if res.AutoScanned {
		t.Error("second call should come from the cache")
	}
	// missing paths are still a clean tool error
	if _, isErr := call(t, cs, "top", map[string]any{"path": filepath.Join(root, "nope")}); !isErr {
		t.Error("missing path should be a tool error")
	}
}

func TestLargestFiles(t *testing.T) {
	root := testTree(t)
	cs := newTestSession(t)
	txt, isErr := call(t, cs, "largest_files", map[string]any{"path": root, "name": "*.BIN", "limit": 2})
	if isErr {
		t.Fatalf("largest_files error: %s", txt)
	}
	var res largestResult
	if err := json.Unmarshal([]byte(txt), &res); err != nil {
		t.Fatal(err)
	}
	if res.Matched != 3 || len(res.Files) != 2 || filepath.Base(res.Files[0].Path) != "large.bin" {
		t.Errorf("largest_files wrong: %+v", res)
	}
	txt, _ = call(t, cs, "largest_files", map[string]any{"path": root, "older_than_days": 1})
	res = largestResult{}
	_ = json.Unmarshal([]byte(txt), &res)
	if res.Matched != 0 {
		t.Errorf("fresh files must not match older_than_days: %+v", res)
	}
}

func TestClassifyTool(t *testing.T) {
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
	mk("package.json", 10)
	mk(filepath.Join("node_modules", "x.js"), 20000)
	mk(filepath.Join("__pycache__", "z.pyc"), 200)

	cs := newTestSession(t)
	if _, isErr := call(t, cs, "scan", map[string]any{"path": root}); isErr {
		t.Fatal("scan failed")
	}
	txt, isErr := call(t, cs, "classify", map[string]any{"path": root})
	if isErr {
		t.Fatalf("classify error: %s", txt)
	}
	var res classifyResult
	if err := json.Unmarshal([]byte(txt), &res); err != nil {
		t.Fatal(err)
	}
	if len(res.Candidates) != 2 {
		t.Fatalf("candidates = %+v, want node_modules + __pycache__", res.Candidates)
	}
	if res.Candidates[0].Name != "node_modules" || res.Candidates[0].Status != "safe" {
		t.Errorf("first candidate wrong: %+v", res.Candidates[0])
	}
	if res.SafeDiskBytes < 20200 {
		t.Errorf("SafeDiskBytes = %d, want >= 20200", res.SafeDiskBytes)
	}
}

func TestDuplicatesTool(t *testing.T) {
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
	mk("dup1.bin", 2000)
	mk(filepath.Join("sub", "dup2.bin"), 2000)
	mk("unique.bin", 1234)
	mk(filepath.Join("tiny1"), 50)
	mk(filepath.Join("sub", "tiny2"), 50) // below min_size, must be excluded

	cs := newTestSession(t)
	txt, isErr := call(t, cs, "duplicates", map[string]any{"path": root, "min_size_bytes": 1000})
	if isErr {
		t.Fatalf("duplicates error: %s", txt)
	}
	var res duplicatesResult
	if err := json.Unmarshal([]byte(txt), &res); err != nil {
		t.Fatal(err)
	}
	if len(res.Groups) != 1 {
		t.Fatalf("groups = %+v, want exactly one (the 2000 B pair)", res.Groups)
	}
	g := res.Groups[0]
	if g.Size != 2000 || g.Waste != 2000 || len(g.Paths) != 2 {
		t.Errorf("group wrong: %+v", g)
	}
	if res.TotalWasteBytes != 2000 {
		t.Errorf("total waste = %d, want 2000", res.TotalWasteBytes)
	}
}

func TestDeleteGuards(t *testing.T) {
	root := testTree(t)
	cs := newTestSession(t)

	// no confirm -> refused
	if _, isErr := call(t, cs, "delete_to_recycle", map[string]any{"path": root}); !isErr {
		t.Error("delete without confirm must be refused")
	}
	// protected path -> refused even with confirm
	gitDir := filepath.Join(root, ".git")
	if err := os.MkdirAll(gitDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if _, isErr := call(t, cs, "delete_to_recycle", map[string]any{"path": gitDir, "confirm": true}); !isErr {
		t.Error("deleting a .git path must be refused")
	}
	if _, isErr := call(t, cs, "delete_to_recycle", map[string]any{"path": `C:\`, "confirm": true}); !isErr {
		t.Error("deleting a drive root must be refused")
	}
}

func TestDeleteToRecycleWorks(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("recycle bin is a Windows concept")
	}
	root := t.TempDir()
	victim := filepath.Join(root, "victim.bin")
	if err := os.WriteFile(victim, []byte("throwaway"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "keeper.bin"), []byte("stays"), 0o644); err != nil {
		t.Fatal(err)
	}
	cs := newTestSession(t)
	if _, isErr := call(t, cs, "scan", map[string]any{"path": root}); isErr {
		t.Fatal("scan failed")
	}
	txt, isErr := call(t, cs, "delete_to_recycle", map[string]any{"path": victim, "confirm": true})
	if isErr {
		t.Fatalf("delete_to_recycle error: %s", txt)
	}
	if _, err := os.Stat(victim); !os.IsNotExist(err) {
		t.Errorf("victim still on disk after delete_to_recycle: %v", err)
	}
	// the cached tree must reflect the deletion
	txt, _ = call(t, cs, "children", map[string]any{"path": root})
	var res childrenResult
	_ = json.Unmarshal([]byte(txt), &res)
	if res.AutoScanned || len(res.Entries) != 1 || res.Entries[0].Name != "keeper.bin" {
		t.Errorf("cache not updated after delete: %+v", res)
	}
	fmt.Println("note: victim.bin is recoverable from the Windows Recycle Bin")
}
