package mcpserver

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
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
	want := map[string]bool{"scan": false, "top": false, "children": false, "du": false}
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

func TestTopWithoutScanFailsGracefully(t *testing.T) {
	root := testTree(t)
	cs := newTestSession(t)
	txt, isErr := call(t, cs, "top", map[string]any{"path": root})
	if !isErr {
		t.Errorf("top on unscanned path should be a tool error, got: %s", txt)
	}
}
