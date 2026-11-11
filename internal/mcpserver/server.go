// Package mcpserver exposes disklens scans as MCP tools for AI agents.
//
// Safety model: measurement tools are read-only. The only mutation is
// delete_to_recycle, which moves items to the Windows Recycle Bin (never a
// hard delete) behind an explicit confirm argument; empty_recycle_bin is
// permanent and documented as "only when the user asked for it".
//
// Agents do not need to remember call order: every tool that reads a tree
// scans the path on demand when no cached scan covers it, and reports how
// old the data it answers from is (cache_age_seconds).
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

// cache holds scanned trees. mu guards the map AND the trees themselves:
// readers hold it while walking nodes, delete_to_recycle while editing them.
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
	key := normPath(e.root.Path)
	// A new scan of an ancestor supersedes cached scans below it.
	for k := range c.scans {
		if k != key && isWithin(k, key) {
			delete(c.scans, k)
		}
	}
	c.scans[key] = e
}

// findLocked returns the node for a normalized path, preferring the most
// recent scan that covers it. c.mu must be held.
func (c *cache) findLocked(key string) (*scanner.Node, *scanEntry) {
	var (
		best  *scanner.Node
		bestE *scanEntry
	)
	for _, e := range c.scans {
		if n, ok := e.index[key]; ok && (bestE == nil || e.scannedAt.After(bestE.scannedAt)) {
			best, bestE = n, e
		}
	}
	return best, bestE
}

// lookup is the tool entry point: it resolves path, scans it on demand when
// no cached scan covers it, and calls fn with the node while holding the
// cache lock. autoScanned tells fn whether this call did the scan.
func (c *cache) lookup(path string, fn func(n *scanner.Node, e *scanEntry, autoScanned bool) error) error {
	root, err := filepath.Abs(path)
	if err != nil || path == "" {
		return errors.New("invalid path: " + path)
	}
	key := normPath(root)
	c.mu.Lock()
	if n, e := c.findLocked(key); n != nil {
		defer c.mu.Unlock()
		return fn(n, e, false)
	}
	c.mu.Unlock()

	info, err := os.Stat(root)
	if err != nil {
		return errors.New("cannot read " + root + ": " + err.Error())
	}
	if !info.IsDir() {
		return errors.New(root + " is a file; pass its directory (or use du for a single file)")
	}
	e, err := scanPath(root, 0)
	if err != nil {
		return err
	}
	c.put(e)
	c.mu.Lock()
	defer c.mu.Unlock()
	n, e := c.findLocked(key)
	if n == nil {
		return errors.New("internal error: scanned tree not found for " + root)
	}
	return fn(n, e, true)
}

// forget updates every cached tree after path was moved to the Recycle Bin.
func (c *cache) forget(path string) {
	key := normPath(path)
	c.mu.Lock()
	defer c.mu.Unlock()
	for k, e := range c.scans {
		if k == key || isWithin(k, key) {
			delete(c.scans, k) // the scanned root itself (or something below it) is gone
			continue
		}
		parent, ok := e.index[normPath(filepath.Dir(path))]
		if !ok {
			continue
		}
		base := filepath.Base(path)
		name := base
		for _, ch := range parent.Children {
			if normPath(ch.Name) == normPath(base) {
				name = ch.Name
			}
		}
		for _, f := range parent.FileEntries {
			if normPath(f.Name) == normPath(base) {
				name = f.Name
			}
		}
		if scanner.RemoveChild(parent, name) {
			scanner.Reaggregate(e.root)
			e.index = buildIndex(e.root)
		}
	}
}

func scanPath(root string, workers int) (*scanEntry, error) {
	start := time.Now()
	rootNode, err := scanner.Scan(root, scanner.Options{Workers: workers, DiskUsage: true, KeepFiles: true})
	if err != nil {
		return nil, errors.New("scan failed: " + err.Error())
	}
	return &scanEntry{root: rootNode, index: buildIndex(rootNode), seconds: time.Since(start).Seconds(), scannedAt: time.Now()}, nil
}

func normPath(p string) string {
	p = filepath.Clean(p)
	if runtime.GOOS == "windows" {
		p = strings.ToLower(p)
	}
	return p
}

// isWithin reports whether normalized path p is strictly below dir.
func isWithin(p, dir string) bool {
	if !strings.HasSuffix(dir, string(filepath.Separator)) {
		dir += string(filepath.Separator)
	}
	return strings.HasPrefix(p, dir)
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

func age(e *scanEntry) float64 {
	return float64(time.Since(e.scannedAt).Milliseconds()) / 1000
}

// Run starts the MCP stdio server until ctx is done or stdin closes.
func Run(ctx context.Context, _ []string) error {
	return newServer().Run(ctx, &mcp.StdioTransport{})
}

func newServer() *mcp.Server {
	c := &cache{}
	server := mcp.NewServer(&mcp.Implementation{Name: "disklens", Title: buildinfo.FullName, Version: buildinfo.Version}, &mcp.ServerOptions{
		Instructions: buildinfo.FullName + " measures disk usage in parallel. Every tool scans on demand when needed " +
			"and caches the tree; results carry cache_age_seconds — call scan() to refresh. " +
			"All sizes are in bytes; size_disk_bytes is what deleting would actually free (hard links counted once). " +
			"A result with inaccessible_dirs > 0 is incomplete — say so. " +
			"delete_to_recycle moves items to the Recycle Bin only (recoverable) and requires confirm:true; " +
			"empty_recycle_bin is permanent and requires the user's explicit request.",
	})
	mcp.AddTool(server, &mcp.Tool{
		Name: "scan",
		Description: "Scan (or rescan) a directory tree in parallel and cache it. Returns totals only. " +
			"Other tools scan on demand, so call this mainly to refresh stale data.",
	}, func(ctx context.Context, req *mcp.CallToolRequest, a struct {
		Path    string `json:"path" jsonschema:"absolute directory path to scan"`
		Workers int    `json:"workers,omitempty" jsonschema:"optional number of scanning goroutines"`
	}) (*mcp.CallToolResult, summary, error) {
		root, err := filepath.Abs(a.Path)
		if err != nil || a.Path == "" {
			return nil, summary{}, errors.New("invalid path: " + a.Path)
		}
		if _, err := os.Stat(root); err != nil {
			return nil, summary{}, errors.New("cannot read " + root + ": " + err.Error())
		}
		e, err := scanPath(root, a.Workers)
		if err != nil {
			return nil, summary{}, err
		}
		c.put(e)
		return nil, summarize(e.root, e.seconds, 0), nil
	})
	mcp.AddTool(server, &mcp.Tool{
		Name:        "top",
		Description: "Largest children (directories and files) of a directory, biggest first. Scans on demand.",
	}, func(ctx context.Context, req *mcp.CallToolRequest, a struct {
		Path  string `json:"path" jsonschema:"absolute directory path"`
		Limit int    `json:"limit,omitempty" jsonschema:"max entries, default 20"`
	}) (*mcp.CallToolResult, childrenResult, error) {
		res, err := c.children(a.Path, a.Limit, defaultLimit, "size")
		return nil, res, err
	})
	mcp.AddTool(server, &mcp.Tool{
		Name:        "children",
		Description: "Direct children (directories and files) of a directory, sorted by size, name or files. Scans on demand.",
	}, func(ctx context.Context, req *mcp.CallToolRequest, a struct {
		Path  string `json:"path" jsonschema:"absolute directory path"`
		Limit int    `json:"limit,omitempty" jsonschema:"max entries, default 50"`
		Sort  string `json:"sort,omitempty" jsonschema:"size (default), name or files"`
	}) (*mcp.CallToolResult, childrenResult, error) {
		res, err := c.children(a.Path, a.Limit, 50, a.Sort)
		return nil, res, err
	})
	mcp.AddTool(server, &mcp.Tool{
		Name: "du",
		Description: "Totals for one path: answered instantly from cache when available, otherwise measured on the spot " +
			"(not cached). Works for files too.",
	}, func(ctx context.Context, req *mcp.CallToolRequest, a struct {
		Path string `json:"path" jsonschema:"absolute directory or file path"`
	}) (*mcp.CallToolResult, summary, error) {
		root, err := filepath.Abs(a.Path)
		if err != nil || a.Path == "" {
			return nil, summary{}, errors.New("invalid path: " + a.Path)
		}
		c.mu.Lock()
		if n, e := c.findLocked(normPath(root)); n != nil {
			s := summarize(n, 0, age(e))
			c.mu.Unlock()
			return nil, s, nil
		}
		c.mu.Unlock()
		info, err := os.Stat(root)
		if err != nil {
			return nil, summary{}, errors.New("cannot read " + root + ": " + err.Error())
		}
		if !info.IsDir() {
			return nil, summary{Path: root, Apparent: info.Size(), Disk: info.Size(), Files: 1}, nil
		}
		start := time.Now()
		n, err := scanner.Scan(root, scanner.Options{DiskUsage: true})
		if err != nil {
			return nil, summary{}, errors.New("scan failed: " + err.Error())
		}
		return nil, summarize(n, time.Since(start).Seconds(), 0), nil
	})
	mcp.AddTool(server, &mcp.Tool{
		Name: "classify",
		Description: "Cleanup candidates under a directory: 'safe' (caches and dependency dirs their tools recreate), " +
			"'review_first' (probably disposable, a human should look) and 'keep' (big system data that must be reclaimed " +
			"another way — the hint says how). Read-only heuristic; sizes are subtree totals. Scans on demand.",
	}, func(ctx context.Context, req *mcp.CallToolRequest, a struct {
		Path string `json:"path" jsonschema:"absolute directory path"`
	}) (*mcp.CallToolResult, classifyResult, error) {
		var res classifyResult
		err := c.lookup(a.Path, func(n *scanner.Node, e *scanEntry, auto bool) error {
			cands := cleanup.Classify(n)
			res = classifyResult{Path: n.Path, Candidates: cands, CacheAge: age(e), AutoScanned: auto,
				Note: "candidates are not descended into; sizes are complete subtree totals"}
			for _, cd := range cands {
				switch cd.Status {
				case "safe":
					res.SafeDiskBytes += cd.Disk
				case "keep":
					res.KeepDiskBytes += cd.Disk
				default:
					res.ReviewDiskBytes += cd.Disk
				}
			}
			return nil
		})
		return nil, res, err
	})
	mcp.AddTool(server, &mcp.Tool{
		Name: "largest_files",
		Description: "Biggest individual files anywhere under a directory (whole subtree, not just the top level), " +
			"optionally filtered by name glob, minimum size and age. Scans on demand.",
	}, func(ctx context.Context, req *mcp.CallToolRequest, a struct {
		Path          string `json:"path" jsonschema:"absolute directory path"`
		Limit         int    `json:"limit,omitempty" jsonschema:"max files, default 20"`
		MinSize       int64  `json:"min_size_bytes,omitempty" jsonschema:"only files at least this big"`
		Name          string `json:"name,omitempty" jsonschema:"glob on the file name, e.g. *.iso or *.mp4 (case-insensitive)"`
		OlderThanDays int    `json:"older_than_days,omitempty" jsonschema:"only files not modified for this many days"`
	}) (*mcp.CallToolResult, largestResult, error) {
		var res largestResult
		err := c.lookup(a.Path, func(n *scanner.Node, e *scanEntry, auto bool) error {
			limit := a.Limit
			if limit <= 0 {
				limit = defaultLimit
			}
			pattern := strings.ToLower(a.Name)
			if pattern != "" {
				if _, err := filepath.Match(pattern, ""); err != nil {
					return errors.New("invalid name pattern: " + a.Name)
				}
			}
			var cutoff int64
			if a.OlderThanDays > 0 {
				cutoff = time.Now().AddDate(0, 0, -a.OlderThanDays).UnixMilli()
			}
			var files []fileResult
			var walk func(*scanner.Node)
			walk = func(d *scanner.Node) {
				for _, f := range d.FileEntries {
					if f.Disk < a.MinSize && f.Apparent < a.MinSize {
						continue
					}
					if cutoff > 0 && (f.MTime == 0 || f.MTime > cutoff) {
						continue
					}
					if pattern != "" {
						if ok, _ := filepath.Match(pattern, strings.ToLower(f.Name)); !ok {
							continue
						}
					}
					files = append(files, fileResult{
						Path: filepath.Join(d.Path, f.Name), Apparent: f.Apparent, Disk: f.Disk,
						Modified: time.UnixMilli(f.MTime).UTC().Format(time.RFC3339), HardLink: f.HardLink,
					})
				}
				for _, ch := range d.Children {
					walk(ch)
				}
			}
			walk(n)
			sort.Slice(files, func(i, j int) bool {
				if files[i].Disk != files[j].Disk {
					return files[i].Disk > files[j].Disk
				}
				return files[i].Apparent > files[j].Apparent
			})
			res = largestResult{Path: n.Path, Matched: len(files), CacheAge: age(e), AutoScanned: auto}
			if len(files) > limit {
				files = files[:limit]
			}
			res.Files = files
			return nil
		})
		return nil, res, err
	})
	mcp.AddTool(server, &mcp.Tool{
		Name: "delete_to_recycle",
		Description: "Move a file or directory to the Windows Recycle Bin (recoverable; never a hard delete). " +
			"Requires confirm:true. Refuses drive roots, Windows system directories, $Recycle.Bin and .git paths. " +
			"Cached scans are updated so later answers reflect the freed space.",
	}, func(ctx context.Context, req *mcp.CallToolRequest, a struct {
		Path    string `json:"path" jsonschema:"absolute file or directory path"`
		Confirm bool   `json:"confirm" jsonschema:"must be true: deliberate confirmation that the item goes to the Recycle Bin"`
	}) (*mcp.CallToolResult, deleteResult, error) {
		if !a.Confirm {
			return nil, deleteResult{}, errors.New("refusing without confirm:true (items go to the Recycle Bin, recoverable)")
		}
		root, err := filepath.Abs(a.Path)
		if err != nil || a.Path == "" {
			return nil, deleteResult{}, errors.New("invalid path: " + a.Path)
		}
		if err := recycle.Guard(root); err != nil {
			return nil, deleteResult{}, err
		}
		info, err := os.Stat(root)
		if err != nil {
			return nil, deleteResult{}, errors.New("cannot read " + root + ": " + err.Error())
		}
		res := deleteResult{Path: root, Recoverable: true}
		c.mu.Lock()
		if n, _ := c.findLocked(normPath(root)); n != nil {
			res.DiskBytes = n.Disk
		} else if !info.IsDir() {
			res.DiskBytes = info.Size()
		}
		c.mu.Unlock()
		if err := recycle.ToRecycleBin(root); err != nil {
			return nil, deleteResult{}, errors.New("move to Recycle Bin failed: " + err.Error())
		}
		c.forget(root)
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
		Description: "Size and item count currently sitting in the Windows Recycle Bin (all drives). Pair with empty_recycle_bin (permanent) after asking the user.",
	}, func(ctx context.Context, req *mcp.CallToolRequest, a struct{}) (*mcp.CallToolResult, recycleResult, error) {
		q, err := recycle.Query()
		if err != nil {
			return nil, recycleResult{}, err
		}
		return nil, recycleResult{SizeBytes: q.SizeBytes, Items: q.Items, Note: "size is what emptying the bin would free"}, nil
	})
	mcp.AddTool(server, &mcp.Tool{
		Name: "duplicates",
		Description: "Probable duplicate files under a directory: groups of 2+ files with identical apparent size, " +
			"biggest waste first. CANDIDATES only (not hash-verified) — confirm before deleting anything. Scans on demand.",
	}, func(ctx context.Context, req *mcp.CallToolRequest, a struct {
		Path    string `json:"path" jsonschema:"absolute directory path"`
		MinSize int64  `json:"min_size_bytes,omitempty" jsonschema:"minimum file size, default 5242880 (5 MiB)"`
		Limit   int    `json:"limit,omitempty" jsonschema:"max groups returned, default 20"`
	}) (*mcp.CallToolResult, duplicatesResult, error) {
		var res duplicatesResult
		err := c.lookup(a.Path, func(n *scanner.Node, e *scanEntry, auto bool) error {
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
			res = duplicatesResult{
				Path: n.Path, MinSizeBytes: minBytes, Groups: groups, TotalWasteBytes: waste,
				CacheAge: age(e), AutoScanned: auto,
				Note: "size-based candidates; verify by mtime/hash before delete_to_recycle",
			}
			return nil
		})
		return nil, res, err
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
	SkippedLinks int64   `json:"skipped_links"`
	Seconds      float64 `json:"scan_seconds"`
	CacheAge     float64 `json:"cache_age_seconds,omitempty"`
}

type childrenResult struct {
	Path        string  `json:"path"`
	Entries     []entry `json:"entries"`
	Total       int     `json:"total_entries"`
	CacheAge    float64 `json:"cache_age_seconds"`
	AutoScanned bool    `json:"auto_scanned,omitempty"`
}

type classifyResult struct {
	Path            string              `json:"path"`
	Candidates      []cleanup.Candidate `json:"candidates"`
	SafeDiskBytes   int64               `json:"safe_disk_bytes"`
	ReviewDiskBytes int64               `json:"review_first_disk_bytes"`
	KeepDiskBytes   int64               `json:"keep_disk_bytes"`
	CacheAge        float64             `json:"cache_age_seconds"`
	AutoScanned     bool                `json:"auto_scanned,omitempty"`
	Note            string              `json:"note"`
}

type largestResult struct {
	Path        string       `json:"path"`
	Files       []fileResult `json:"files"`
	Matched     int          `json:"matched_files"`
	CacheAge    float64      `json:"cache_age_seconds"`
	AutoScanned bool         `json:"auto_scanned,omitempty"`
}

type fileResult struct {
	Path     string `json:"path"`
	Apparent int64  `json:"size_apparent_bytes"`
	Disk     int64  `json:"size_disk_bytes"`
	Modified string `json:"modified"`
	HardLink bool   `json:"hard_link,omitempty"`
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
	CacheAge        float64       `json:"cache_age_seconds"`
	AutoScanned     bool          `json:"auto_scanned,omitempty"`
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
	HardLink   bool   `json:"hard_link,omitempty"`
}

func summarize(n *scanner.Node, seconds, cacheAge float64) summary {
	return summary{
		Path: n.Path, Apparent: n.Size, Disk: n.Disk, Files: n.Files, Dirs: n.Dirs,
		Inaccessible: n.Errors, SkippedLinks: n.Links, Seconds: seconds, CacheAge: cacheAge,
	}
}

func (c *cache) children(path string, limit, defLimit int, sortBy string) (childrenResult, error) {
	var res childrenResult
	err := c.lookup(path, func(n *scanner.Node, e *scanEntry, auto bool) error {
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
				Apparent: f.Apparent, Disk: f.Disk, Files: 1, HardLink: f.HardLink,
			})
		}
		switch sortBy {
		case "name":
			sort.Slice(entries, func(i, j int) bool { return strings.ToLower(entries[i].Name) < strings.ToLower(entries[j].Name) })
		case "files":
			sort.SliceStable(entries, func(i, j int) bool { return entries[i].Files > entries[j].Files })
		default:
			sort.SliceStable(entries, func(i, j int) bool {
				if entries[i].Disk != entries[j].Disk {
					return entries[i].Disk > entries[j].Disk
				}
				return entries[i].Apparent > entries[j].Apparent
			})
		}
		res = childrenResult{Path: n.Path, Total: len(entries), CacheAge: age(e), AutoScanned: auto}
		if len(entries) > limit {
			entries = entries[:limit]
		}
		res.Entries = entries
		return nil
	})
	return res, err
}
