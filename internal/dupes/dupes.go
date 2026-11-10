// Package dupes finds probable duplicate files inside a scanned tree.
// v0 groups by apparent size only (cheap, no reading of file contents):
// results are CANDIDATES and must be confirmed (by eye, mtime or hash) before
// anything is deleted. .git subtrees are skipped — identical objects there
// are the design of git, not duplicates.
package dupes

import (
	"path/filepath"
	"sort"
	"strings"

	"disklens/internal/scanner"
)

// Group is one set of files with the same apparent size.
type Group struct {
	Size  int64    `json:"size_apparent_bytes"`
	Waste int64    `json:"wasted_bytes_if_all_but_one_removed"`
	Paths []string `json:"paths"`
}

// Find walks root (KeepFiles scans only) and returns groups of 2+ files with
// identical apparent size, largest potential waste first, capped at maxGroups.
func Find(root *scanner.Node, minBytes int64, maxGroups int) []Group {
	bySize := map[int64][]string{}
	var walk func(*scanner.Node)
	walk = func(n *scanner.Node) {
		for _, f := range n.FileEntries {
			if f.Apparent < minBytes {
				continue
			}
			bySize[f.Apparent] = append(bySize[f.Apparent], filepath.Join(n.Path, f.Name))
		}
		for _, c := range n.Children {
			if strings.EqualFold(c.Name, ".git") {
				continue
			}
			walk(c)
		}
	}
	walk(root)

	groups := make([]Group, 0)
	for size, paths := range bySize {
		if len(paths) < 2 {
			continue
		}
		sort.Strings(paths)
		groups = append(groups, Group{Size: size, Waste: size * int64(len(paths)-1), Paths: paths})
	}
	sort.Slice(groups, func(i, j int) bool { return groups[i].Waste > groups[j].Waste })
	if maxGroups > 0 && len(groups) > maxGroups {
		groups = groups[:maxGroups]
	}
	return groups
}
