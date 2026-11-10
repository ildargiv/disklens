// Package scanner implements a parallel (BFS) filesystem usage scanner.
//
// Design notes:
//   - A worker pool consumes jobs from a queue; each job scans one directory.
//   - During scanning, Node.Size holds only the sizes of the files directly
//   - inside that directory. After the scan finishes, aggregate() folds child
//   - totals into parents (post-order), so Node.Size ends up as the subtree total.
//   - Windows specifics: junctions and directory symlinks (reparse points) are
//   - never descended into, which prevents both loops and double counting.
//   - Directories that cannot be read (ACCESS_DENIED) are marked with HasError
//   - and the scan continues.
package scanner

import (
	"os"
	"path/filepath"
	"runtime"
	"sync"
	"sync/atomic"
)

// Options controls the scan.
type Options struct {
	// Workers is the number of concurrent scanning goroutines. <= 0 means NumCPU.
	Workers int
	// FollowSymlinks makes the scanner follow *file* symlinks (directory
	// symlinks and junctions are never followed on Windows).
	FollowSymlinks bool
	// DiskUsage measures real on-disk sizes (Windows: GetCompressedFileSizeW).
	// Without it, Node.Disk and FileEntry.Disk are not meaningful.
	DiskUsage bool
	// KeepFiles stores per-file entries in Node.FileEntries. Needed for
	// ncdu-compatible export and for browsing individual files in a TUI.
	KeepFiles bool
}

// FileEntry is a single regular file inside a directory node.
type FileEntry struct {
	Name     string
	Apparent int64 // lstat st_size
	Disk     int64 // on-disk size; valid only when Options.DiskUsage was set
}

// Node is one directory in the scanned tree.
type Node struct {
	Name  string
	Path  string
	Size  int64 // after aggregate(): total apparent bytes of the subtree
	Disk  int64 // after aggregate(): total on-disk bytes (requires DiskUsage)
	Files int64 // after aggregate(): total file count of the subtree
	Dirs  int64 // after aggregate(): total directory count of the subtree

	HasError bool  // this directory (or one below it) could not be read fully
	Errors   int64 // number of unreadable directories in the subtree

	FileEntries []FileEntry // direct files, only populated when KeepFiles is set
	Children    []*Node
}

type scanState struct {
	queue   chan *Node
	pending atomic.Int64
	denied  atomic.Int64
	opt     Options
}

// Scan walks root with a pool of worker goroutines and returns the tree.
func Scan(root string, opt Options) (*Node, error) {
	info, err := os.Stat(root)
	if err != nil {
		return nil, err
	}
	if !info.IsDir() {
		return nil, &NotDirError{Path: root}
	}
	if opt.Workers <= 0 {
		opt.Workers = runtime.NumCPU()
	}

	s := &scanState{queue: make(chan *Node, 1024), opt: opt}
	rootNode := &Node{Name: filepath.Base(root), Path: root}

	var wg sync.WaitGroup
	for i := 0; i < opt.Workers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for n := range s.queue {
				s.scanOne(n)
				// The last worker to finish closes the queue, releasing everyone.
				// pending is incremented on enqueue and decremented here, so it
				// reaches zero only after every enqueued node has been scanned.
				if s.pending.Add(-1) == 0 {
					close(s.queue)
				}
			}
		}()
	}

	s.enqueue(rootNode)
	wg.Wait()
	aggregate(rootNode)
	return rootNode, nil
}

// NotDirError is returned when the scan root is not a directory.
type NotDirError struct{ Path string }

func (e *NotDirError) Error() string { return e.Path + " is not a directory" }

func (s *scanState) enqueue(n *Node) {
	s.pending.Add(1)
	select {
	case s.queue <- n:
	default:
		// Queue is full and all workers may be waiting for a free slot:
		// park this one enqueue in its own goroutine.
		go func() { s.queue <- n }()
	}
}

func (s *scanState) scanOne(n *Node) {
	entries, err := os.ReadDir(n.Path)
	if err != nil {
		n.HasError = true
		n.Errors++
		s.denied.Add(1)
		return
	}
	for _, e := range entries {
		info, err := e.Info() // lstat semantics: does not follow links
		if err != nil {
			n.HasError = true
			n.Errors++
			continue
		}
		mode := info.Mode()
		switch {
		case mode&os.ModeSymlink != 0:
			// Directory symlinks are skipped; file symlinks are optionally
			// counted (their target size, without following on Windows links
			// to directories which show up as reparse points anyway).
			if s.opt.FollowSymlinks && !isReparsePoint(info) {
				n.Size += info.Size()
				n.Files++
			}
		case e.IsDir():
			if isReparsePoint(info) {
				// Junction or directory symlink: never descend (loops, double count).
				continue
			}
			child := &Node{Name: e.Name(), Path: filepath.Join(n.Path, e.Name())}
			n.Children = append(n.Children, child)
			s.enqueue(child)
		default:
			n.Size += info.Size()
			n.Files++
			if s.opt.KeepFiles || s.opt.DiskUsage {
				disk := info.Size()
				if s.opt.DiskUsage {
					disk = diskSize(filepath.Join(n.Path, e.Name()), info.Size())
				}
				n.Disk += disk
				if s.opt.KeepFiles {
					n.FileEntries = append(n.FileEntries, FileEntry{
						Name: e.Name(), Apparent: info.Size(), Disk: disk,
					})
				}
			}
		}
	}
}

// aggregate folds child totals into parents and marks error subtrees.
func aggregate(n *Node) {
	for _, c := range n.Children {
		aggregate(c)
		n.Size += c.Size
		n.Disk += c.Disk
		n.Files += c.Files
		n.Dirs += c.Dirs + 1
		if c.HasError {
			n.HasError = true
		}
		n.Errors += c.Errors
	}
}


