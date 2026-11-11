package tui

import (
	"sort"
	"strings"

	"disklens/internal/scanner"
)

// item is one browser row: a directory or a file of the current node.
type item struct {
	name  string
	isDir bool
	size  int64 // size on disk, or apparent size when the browser shows apparent sizes
	files int64
	dirs  int64
	node  *scanner.Node
	entry *scanner.FileEntry
}

// buildItems assembles, filters and sorts the rows for one directory node.
func buildItems(n *scanner.Node, sortBy, filter string, apparent bool) []item {
	items := make([]item, 0, len(n.Children)+len(n.FileEntries))
	lowFilter := strings.ToLower(filter)
	for _, c := range n.Children {
		if lowFilter != "" && !strings.Contains(strings.ToLower(c.Name), lowFilter) {
			continue
		}
		size := c.Disk
		if apparent {
			size = c.Size
		}
		items = append(items, item{
			name: c.Name, isDir: true, size: size,
			files: c.Files, dirs: c.Dirs, node: c,
		})
	}
	for i := range n.FileEntries {
		f := &n.FileEntries[i]
		if lowFilter != "" && !strings.Contains(strings.ToLower(f.Name), lowFilter) {
			continue
		}
		size := f.Disk
		if apparent {
			size = f.Apparent
		}
		items = append(items, item{name: f.Name, size: size, files: 1, entry: f})
	}

	switch sortBy {
	case "name":
		sort.Slice(items, func(i, j int) bool {
			if items[i].isDir != items[j].isDir {
				return items[i].isDir // directories first when browsing by name
			}
			return strings.ToLower(items[i].name) < strings.ToLower(items[j].name)
		})
	case "files":
		sort.Slice(items, func(i, j int) bool {
			if items[i].files != items[j].files {
				return items[i].files > items[j].files
			}
			return items[i].size > items[j].size
		})
	default: // size: dirs and files mixed, biggest first (stable, ties by name)
		sort.SliceStable(items, func(i, j int) bool {
			if items[i].size != items[j].size {
				return items[i].size > items[j].size
			}
			return strings.ToLower(items[i].name) < strings.ToLower(items[j].name)
		})
	}
	return items
}
