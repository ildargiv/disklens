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
	size  int64 // disk size (scan runs with DiskUsage)
	files int64
	dirs  int64
	node  *scanner.Node
	entry *scanner.FileEntry
}

// buildItems assembles and sorts the rows for one directory node.
func buildItems(n *scanner.Node, sortBy string) []item {
	items := make([]item, 0, len(n.Children)+len(n.FileEntries))
	for _, c := range n.Children {
		items = append(items, item{
			name: c.Name, isDir: true, size: c.Disk,
			files: c.Files, dirs: c.Dirs, node: c,
		})
	}
	for i := range n.FileEntries {
		f := &n.FileEntries[i]
		items = append(items, item{name: f.Name, size: f.Disk, files: 1, entry: f})
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
	default: // size: dirs and files mixed, biggest first
		sort.Slice(items, func(i, j int) bool { return items[i].size > items[j].size })
	}
	return items
}
