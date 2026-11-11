package main

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"disklens/internal/scanner"
)

func TestParseSize(t *testing.T) {
	cases := map[string]int64{
		"1500": 1500, "100K": 100 << 10, "1.5G": 3 << 29, "2TiB": 2 << 40, "300MB": 300 << 20, " 1 m ": 1 << 20,
	}
	for in, want := range cases {
		got, err := parseSize(in)
		if err != nil || got != want {
			t.Errorf("parseSize(%q) = %d, %v; want %d", in, got, err, want)
		}
	}
	for _, bad := range []string{"", "x", "-1G", "1Q"} {
		if _, err := parseSize(bad); err == nil {
			t.Errorf("parseSize(%q) should fail", bad)
		}
	}
}

func tree(t *testing.T) *scanner.Node {
	t.Helper()
	root := t.TempDir()
	for rel, size := range map[string]int{"a/a1/deep.bin": 3000, "a/top.bin": 100, "b/b.bin": 50} {
		p := filepath.Join(root, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, make([]byte, size), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	n, err := scanner.Scan(root, scanner.Options{KeepFiles: true})
	if err != nil {
		t.Fatal(err)
	}
	return n
}

func TestRenderDepth(t *testing.T) {
	n := tree(t)
	c := config{depth: 1, sortBy: "size", apparent: true, ascii: true}
	var buf bytes.Buffer
	render(&buf, n, c.depth, 0, c)
	out := buf.String()
	if strings.Contains(out, "a1") {
		t.Errorf("--depth 1 must show only the first level:\n%s", out)
	}
	buf.Reset()
	c.depth = 2
	render(&buf, n, c.depth, 0, c)
	if out = buf.String(); !strings.Contains(out, "a1") || strings.Contains(out, "deep.bin") {
		t.Errorf("--depth 2 must show two levels of directories:\n%s", out)
	}
	buf.Reset()
	c.depth, c.showFiles = 0, true
	render(&buf, n, c.depth, 0, c)
	if out = buf.String(); !strings.Contains(out, "deep.bin") {
		t.Errorf("--depth 0 --files must show everything:\n%s", out)
	}
}

func TestJSONOutput(t *testing.T) {
	n := tree(t)
	c := config{depth: 1, top: 1, sortBy: "size", apparent: true}
	var buf bytes.Buffer
	if err := writeJSON(&buf, n, c, time.Second); err != nil {
		t.Fatal(err)
	}
	var res jsonResult
	if err := json.Unmarshal(buf.Bytes(), &res); err != nil {
		t.Fatal(err)
	}
	if !res.Complete || res.Apparent != 3150 || res.Files != 3 {
		t.Errorf("totals wrong: %+v", res)
	}
	if len(res.Children) != 1 || res.Children[0].Name != "a" || res.More == nil || res.More.Count != 1 {
		t.Errorf("top/more wrong: %+v", res)
	}
	if res.Children[0].Children != nil {
		t.Error("depth 1 must not include grandchildren")
	}
}

func TestNormalizeDirArg(t *testing.T) {
	cases := map[string]string{
		"": ".", `C:"`: `C:\`, "d:": `d:\`, `C:\Users\me`: `C:\Users\me`, ".": ".",
	}
	for in, want := range cases {
		if got := normalizeDirArg(in); got != want {
			t.Errorf("normalizeDirArg(%q) = %q, want %q", in, got, want)
		}
	}
}
