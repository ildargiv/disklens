// Package mcpserver exposes disklens scans as MCP tools for AI agents.
//
// Safety model: this stage is read-only measurement. Destructive tools
// (delete-to-recycle-bin) will only ever move files to the Windows Recycle
// Bin, never delete outright; they are gated behind explicit confirmation.
package mcpserver

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"disklens/internal/buildinfo"
	"disklens/internal/cleanup"
	"disklens/internal/dupes"
	"disklens/internal/recycle"
	"disklens/internal/scanner"
)

type scanEntry struct {
	root      *scanner.Node
	index     map[string]*scanner.Node // normalized absolute path -> node
	seconds   float64
	scannedAt time.Time
}

type cache struct {
	mu    sync.Mutex
	scans map[string]*scanEntry
}

func (c *cache) put(e *scanEntry) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.scans == nil {
		c.scans = make(map[string]*scanEntry)
	}
	c.scans[normPath(e.root.Path)] = e
}

// find returns the node for an absolute path: first in freshly scanned tree,
// then in any cached scan.
func (c *cache) find(path string) (*scanner.Node, bool) {
	key := normPath(path)
	c.mu.Lock()
	defer c.mu.Unlock()
	for _, e := range c.scans {
		if n, ok := e.index[key]; ok {
			return n, true
		}
	}
	return nil, false
}

func normPath(p string) string {
	p = filepath.Clean(p)
	if runtime.GOOS == "windows" {
		p = strings.ToLower(p)
	}
	return p
}

func buildIndex(n *scanner.Node) map[string]*scanner.Node {
	idx := make(map[string]*scanner.Node)
	var add func(*scanner.Node)
	add = func(n *scanner.Node) {
		idx[normPath(n.Path)] = n
		for _, c := range n.Children {
			add(c)
		}
	}
	add(n)
	return idx
}

// Run starts the MCP stdio server until ctx is done or stdin closes.
func Run(ctx context.Context, _ []string) error {
	return newServer().Run(ctx, &mcp.StdioTransport{})
}

func newServer() *mcp.Server {
	c := &cache{}

	server := mcp.NewServer(&mcp.Implementation{Name: "disklens", Version: buildinfo.Version}, &mcp.ServerOptions{
		Instructions: "disklens measures disk usage in parallel. scan() caches a tree; " +
			"top/children/du then query the cache without re-reading the disk. " +
			"All sizes are in bytes; size_disk is cluster-accurate on Windows. " +
			"delete_to_recycle moves items to the Recycle Bin only (recoverable) and requires confirm:true; " +
			"empty_recycle_bin is permanent and requires the user's explicit request.",
	})

	mcp.AddTool(server, &mcp.Tool{
		Name: "scan",
		Description: "Scan a directory tree in parallel and cache it for top/children/du. " +
			"Returns totals only. Overwrites the cached scan of the same root.",
	}, func(ctx context.Context, req *mcp.CallToolRequest, a struct {
		Path    string `json:"path" jsonschema:"absolute directory path to scan"`
		Workers int    `json:"workers,omitempty" jsonschema:"optional number of scanning goroutines"`
	}) (*mcp.CallToolResult, summary, error) {
		root, err := filepath.Abs(a.Path)
		if err != nil {
			return nil, summary{}, errors.New("invalid path: " + a.Path)
		}
		if _, err := os.Stat(root); err != nil {
			return nil, summary{}, errors.New("cannot read " + root + ": " + err.Error())
		}
		start := time.Now()
		rootNode, err := scanner.Scan(root, scanner.Options{Workers: a.Workers, DiskUsage: true, KeepFiles: true})
		if err != nil {
			return nil, summary{}, errors.New("scan failed: " + err.Error())
		}
		secs := time.Since(start).Seconds()
		e := &scanEntry{root: rootNode, index: buildIndex(rootNode), seconds: secs, scannedAt: time.Now()}
		c.put(e)
		return nil, summarize(rootNode, secs), nil
	})

	mcp.AddTool(server, &mcp.Tool{
		Name:        "top",
		Description: "Largest children of a scanned directory (from cache). Requires a prior scan() of it or an ancestor.",
	}, func(ctx context.Context, req *mcp.CallToolRequest, a struct {
		Path  string `json:"path" jsonschema:"directory previously covered by a scan"`
		Limit int    `json:"limit,omitempty" jsonschema:"max entries, default 20"`
	}) (*mcp.CallToolResult, childrenResult, error) {
		res, toolErr := c.children(a.Path, a.Limit, defaultLimit, "size")
		if toolErr != "" {
			return nil, childrenResult{}, errors.New(toolErr)
		}
		return nil, res, nil
	})

	mcp.AddTool(server, &mcp.Tool{
		Name:        "children",
		Description: "Direct subdirectories of a cached path, sorted by size, name or files.",
	}, func(ctx context.Context, req *mcp.CallToolRequest, a struct {
		Path  string `json:"path" jsonschema:"directory previously covered by a scan"`
		Limit int    `json:"limit,omitempty" jsonschema:"max entries, default 50"`
		Sort  string `json:"sort,omitempty" jsonschema:"size (default), name or files"`
	}) (*mcp.CallToolResult, childrenResult, error) {
		res, toolErr := c.children(a.Path, a.Limit, 50, a.Sort)
		if toolErr != "" {
			return nil, childrenResult{}, errors.New(toolErr)
		}
		return nil, res, nil
	})

	mcp.AddTool(server, &mcp.Tool{
		Name: "du",
		Description: "Totals for one path: answered instantly from cache when available, otherwise scanned on the spot " +
			"(not cached). Use scan first for repeated work.",
	}, func(ctx context.Context, req *mcp.CallToolRequest, a struct {
		Path string `json:"path" jsonschema:"absolute directory or file path"`
	}) (*mcp.CallToolResult, summary, error) {
		root, err := filepath.Abs(a.Path)
		if err != nil {
			return nil, summary{}, errors.New("invalid path: " + a.Path)
		}
		if n, ok := c.find(root); ok {
			return nil, summarize(n, 0), nil
		}
		info, err := os.Stat(root)
		if err != nil {
			return nil, summary{}, errors.New("cannot read " + root + ": " + err.Error())
		}
		if !info.IsDir() {
			return nil, summary{
				Path: root, Apparent: info.Size(), Disk: info.Size(), Files: 1, Seconds: 0,
			}, nil
		}
		start := time.Now()
		n, err := scanner.Scan(root, scanner.Options{DiskUsage: true})
		if err != nil {
			return nil, summary{}, errors.New("scan failed: " + err.Error())
		}
		return nil, summarize(n, time.Since(start).Seconds()), nil
	})

	mcp.AddTool(server, &mcp.Tool{
		Name: "classify",
		Description: "Cleanup candidates in a cached scan: status 'safe' (caches and dependency dirs " +
			"that their tools recreate) vs 'review_first'. Read-only heuristic; sizes are subtree totals.",
	}, func(ctx context.Context, req *mcp.CallToolRequest, a struct {
		Path string `json:"path" jsonschema:"directory previously covered by a scan"`
	}) (*mcp.CallToolResult, classifyResult, error) {
		root, err := filepath.Abs(a.Path)
		if err != nil {
			return nil, classifyResult{}, errors.New("invalid path: " + a.Path)
		}
		n, ok := c.find(root)
		if !ok {
			return nil, classifyResult{}, errors.New("path is not covered by a cached scan; call scan() first: " + root)
		}
		cands := cleanup.Classify(n)
		var safe, review int64
		for _, cd := range cands {
			if cd.Status == "safe" {
				safe += cd.Disk
			} else {
				review += cd.Disk
			}
		}
		return nil, classifyResult{
			Path: n.Path, Candidates: cands, SafeDiskBytes: safe, ReviewDiskBytes: review,
			Note: "candidates are not descended into; sizes are complete subtree totals",
		}, nil
	})

	mcp.AddTool(server, &mcp.Tool{
		Name: "delete_to_recycle",
		Description: "Move a file or directory to the Windows Recycle Bin (recoverable; never a hard delete). " +
			"Requires confirm:true. Refuses drive roots, Windows system directories and .git paths.",
	}, func(ctx context.Context, req *mcp.CallToolRequest, a struct {
		Path    string `json:"path" jsonschema:"absolute file or directory path"`
		Confirm bool   `json:"confirm" jsonschema:"must be true: deliberate confirmation that the item goes to the Recycle Bin"`
	}) (*mcp.CallToolResult, deleteResult, error) {
		if !a.Confirm {
			return nil, deleteResult{}, errors.New("refusing without confirm:true (items go to the Recycle Bin, recoverable)")
		}
		root, err := filepath.Abs(a.Path)
		if err != nil {
			return nil, deleteResult{}, errors.New("invalid path: " + a.Path)
		}
		if err := recycle.Guard(root); err != nil {
			return nil, deleteResult{}, err
		}
		if _, err := os.Stat(root); err != nil {
			return nil, deleteResult{}, errors.New("cannot read " + root + ": " + err.Error())
		}
		res := deleteResult{Path: root, Recoverable: true}
		if n, ok := c.find(root); ok {
			res.DiskBytes = n.Disk
		}
		if err := recycle.ToRecycleBin(root); err != nil {
			return nil, deleteResult{}, errors.New("move to Recycle Bin failed: " + err.Error())
		}
		res.Note = "item is in the Recycle Bin and can be restored from there"
		return nil, res, nil
	})

	mcp.AddTool(server, &mcp.Tool{
		Name:        "empty_recycle_bin",
		Description: "PERMANENTLY destroy the contents of the Windows Recycle Bin. Requires confirm:true. Irreversible — call only after the user explicitly asked to empty it.",
	}, func(ctx context.Context, req *mcp.CallToolRequest, a struct {
		Confirm bool `json:"confirm" jsonschema:"must be true"`
	}) (*mcp.CallToolResult, emptyResult, error) {
		if !a.Confirm {
			return nil, emptyResult{}, errors.New("refusing without confirm:true (emptying the Recycle Bin is permanent)")
		}
		if err := recycle.EmptyRecycleBin(); err != nil {
			return nil, emptyResult{}, errors.New("emptying the Recycle Bin failed: " + err.Error())
		}
		return nil, emptyResult{Emptied: true, Warning: "Recycle Bin contents are now permanently gone"}, nil
	})

	mcp.AddTool(server, &mcp.Tool{
		Name:        "recycle_bin",
		Description: "Size and item count currently sitting in the Windows Recycle Bin (default root drive). Pair with empty_recycle_bin (permanent) after asking the user.",
	}, func(ctx context.Context, req *mcp.CallToolRequest, a struct{}) (*mcp.CallToolResult, recycleResult, error) {
		q, err := recycle.Query()
		if err != nil {
			return nil, recycleResult{}, err
		}
		return nil, recycleResult{SizeBytes: q.SizeBytes, Items: q.Items, Note: "size is what emptying the bin would free"}, nil
	})

	mcp.AddTool(server, &mcp.Tool{
		Name: "duplicates",
		Description: "Probable duplicate files in a cached scan: groups of 2+ files with identical apparent size, " +
			"biggest waste first. CANDIDATES only (not hash-verified) — confirm before deleting anything. Requires a prior scan().",
	}, func(ctx context.Context, req *mcp.CallToolRequest, a struct {
		Path    string `json:"path" jsonschema:"directory previously covered by a scan"`
		MinSize int64  `json:"min_size_bytes,omitempty" jsonschema:"minimum file size, default 5242880 (5 MiB)"`
		Limit   int    `json:"limit,omitempty" jsonschema:"max groups returned, default 20"`
	}) (*mcp.CallToolResult, duplicatesResult, error) {
		root, err := filepath.Abs(a.Path)
		if err != nil {
			return nil, duplicatesResult{}, errors.New("invalid path: " + a.Path)
		}
		n, ok := c.find(root)
		if !ok {
			return nil, duplicatesResult{}, errors.New("path is not covered by a cached scan; call scan() first: " + root)
		}
		minBytes := a.MinSize
		if minBytes <= 0 {
			minBytes = 5 << 20
		}
		limit := a.Limit
		if limit <= 0 {
			limit = 20
		}
		groups := dupes.Find(n, minBytes, limit)
		var waste int64
		for _, g := range groups {
			waste += g.Waste
		}
		return nil, duplicatesResult{
			Path: n.Path, MinSizeBytes: minBytes, Groups: groups, TotalWasteBytes: waste,
			Note: "size-based candidates; verify by mtime/hash before delete_to_recycle",
		}, nil
	})

	return server
}

const defaultLimit = 20

type summary struct {
	Path         string  `json:"path"`
	Apparent     int64   `json:"size_apparent_bytes"`
	Disk         int64   `json:"size_disk_bytes"`
	Files        int64   `json:"files"`
	Dirs         int64   `json:"dirs"`
	Inaccessible int64   `json:"inaccessible_dirs"`
	Seconds      float64 `json:"scan_seconds"`
}

type childrenResult struct {
	Path    string  `json:"path"`
	Entries []entry `json:"entries"`
}

type classifyResult struct {
	Path            string              `json:"path"`
	Candidates      []cleanup.Candidate `json:"candidates"`
	SafeDiskBytes   int64               `json:"safe_disk_bytes"`
	ReviewDiskBytes int64               `json:"review_first_disk_bytes"`
	Note            string              `json:"note"`
}

type deleteResult struct {
	Path        string `json:"path"`
	DiskBytes   int64  `json:"disk_bytes_reported,omitempty"`
	Recoverable bool   `json:"recoverable"`
	Note        string `json:"note"`
}

type emptyResult struct {
	Emptied bool   `json:"emptied"`
	Warning string `json:"warning"`
}

type recycleResult struct {
	SizeBytes int64  `json:"size_bytes"`
	Items     int64  `json:"items"`
	Note      string `json:"note"`
}

type duplicatesResult struct {
	Path            string        `json:"path"`
	MinSizeBytes    int64         `json:"min_size_bytes"`
	Groups          []dupes.Group `json:"groups"`
	TotalWasteBytes int64         `json:"total_waste_bytes"`
	Note            string        `json:"note"`
}

type entry struct {
	Type       string `json:"type"`
	Name       string `json:"name"`
	Path       string `json:"path"`
	Apparent   int64  `json:"size_apparent_bytes"`
	Disk       int64  `json:"size_disk_bytes"`
	Files      int64  `json:"files,omitempty"`
	Dirs       int64  `json:"dirs,omitempty"`
	Unreadable bool   `json:"unreadable,omitempty"`
}

func summarize(n *scanner.Node, seconds float64) summary {
	return summary{
		Path: n.Path, Apparent: n.Size, Disk: n.Disk, Files: n.Files, Dirs: n.Dirs,
		Inaccessible: n.Errors, Seconds: seconds,
	}
}

func (c *cache) children(path string, limit, defLimit int, sortBy string) (childrenResult, string) {
	root, err := filepath.Abs(path)
	if err != nil {
		return childrenResult{}, "invalid path: " + path
	}
	n, ok := c.find(root)
	if !ok {
		return childrenResult{}, "path is not covered by a cached scan; call scan() first: " + root
	}
	if limit <= 0 {
		limit = defLimit
	}
	entries := make([]entry, 0, len(n.Children)+len(n.FileEntries))
	for _, k := range n.Children {
		entries = append(entries, entry{
			Type: "dir", Name: k.Name, Path: k.Path, Apparent: k.Size, Disk: k.Disk,
			Files: k.Files, Dirs: k.Dirs, Unreadable: k.HasError,
		})
	}
	for _, f := range n.FileEntries {
		entries = append(entries, entry{
			Type: "file", Name: f.Name, Path: filepath.Join(n.Path, f.Name),
			Apparent: f.Apparent, Disk: f.Disk, Files: 1,
		})
	}
	switch sortBy {
	case "name":
		sort.Slice(entries, func(i, j int) bool { return strings.ToLower(entries[i].Name) < strings.ToLower(entries[j].Name) })
	case "files":
		sort.Slice(entries, func(i, j int) bool { return entries[i].Files > entries[j].Files })
	default:
		sort.Slice(entries, func(i, j int) bool { return entries[i].Apparent > entries[j].Apparent })
	}
	if len(entries) > limit {
		entries = entries[:limit]
	}
	return childrenResult{Path: n.Path, Entries: entries}, ""
}
