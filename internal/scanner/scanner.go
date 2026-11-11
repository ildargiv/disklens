// Package scanner implements a parallel (BFS) filesystem usage scanner.
//
// Design notes:
//   - A worker pool consumes jobs from a queue; each job lists one directory.
//   - During scanning a Node only records its own files (direct* fields).
//     After the scan, aggregate() folds child totals into parents
//     (post-order), so Node.Size ends up as the subtree total.
//   - Windows specifics: junctions, mount points and directory symlinks
//     (reparse points) are never descended into and never counted as files,
//     which prevents loops, double counting and inflated file counts.
//   - Hard links are counted once per scan (like du): the first link seen
//     carries the size, later links are flagged FileEntry.HardLink and add
//     no bytes. Disable with Options.CountLinks.
//   - Directories that cannot be read (ACCESS_DENIED) are marked with
//     HasError and the scan continues.
package scanner

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
)

// Options controls the scan.
type Options struct {
	// Workers is the number of concurrent scanning goroutines. <= 0 means NumCPU.
	Workers int
	// FollowSymlinks counts *file* symlinks by their own (link) size.
	// Directory symlinks and junctions are never followed.
	FollowSymlinks bool
	// DiskUsage measures real on-disk sizes (Windows: allocation size, with
	// GetCompressedFileSizeW for compressed/sparse files; POSIX: st_blocks).
	// Without it, Node.Disk and FileEntry.Disk equal the apparent size.
	DiskUsage bool
	// KeepFiles stores per-file entries in Node.FileEntries. Needed for
	// ncdu-compatible export and for browsing individual files in a TUI.
	KeepFiles bool
	// CountLinks counts every hard link of a file separately (du -l).
	// By default a hard-linked file contributes its size only once.
	CountLinks bool
	// Exclude skips entries whose name matches one of these glob patterns
	// (filepath.Match syntax). A pattern containing a path separator is
	// matched against the full path instead. Matching is case-insensitive
	// on Windows.
	Exclude []string
	// Progress, when set, is called with a live snapshot of running totals
	// during the scan (every few dozen completed directories, from worker
	// goroutines — implementations must be concurrency-safe).
	Progress func(ProgressState)
	// Cancel, when closed, makes the scan stop scheduling new directories and
	// return the partial tree it has built so far (no error).
	Cancel <-chan struct{}
}

// ProgressState is a non-authoritative live snapshot taken mid-scan; final
// totals are on the Node after Scan returns.
type ProgressState struct {
	DirsDone int64 // directories fully listed so far
	Files    int64 // files counted so far
	Bytes    int64 // apparent bytes summed so far
}

// FileEntry is a single regular file inside a directory node.
type FileEntry struct {
	Name     string
	Apparent int64 // logical file size
	Disk     int64 // on-disk size; equals Apparent unless Options.DiskUsage was set
	MTime    int64 // last modification, Unix milliseconds
	// HardLink marks an additional hard link to a file already counted
	// elsewhere in this scan: its bytes are not included in any total.
	HardLink bool
}

// Node is one directory in the scanned tree.
type Node struct {
	Name  string
	Path  string
	Size  int64 // after aggregate(): total apparent bytes of the subtree
	Disk  int64 // after aggregate(): total on-disk bytes
	Files int64 // after aggregate(): total file count of the subtree
	Dirs  int64 // after aggregate(): total directory count of the subtree

	HasError bool  // this directory (or one below it) could not be read fully
	Errors   int64 // number of unreadable directories in the subtree
	Links    int64 // junctions/symlinks skipped in the subtree (not descended)

	// direct* hold only this directory's own files (set during the scan);
	// Size/Files/Disk/Errors are recomputed from them by aggregate/Reaggregate.
	directSize     int64
	directDisk     int64
	directFiles    int64
	directHasError bool
	directErrors   int64
	directLinks    int64

	FileEntries []FileEntry // direct files, only populated when KeepFiles is set
	Children    []*Node
}

// AddFile appends a file to n's own entries and counters (used by importers
// that build trees without scanning). Call Reaggregate on the root afterwards.
func (n *Node) AddFile(f FileEntry) {
	n.FileEntries = append(n.FileEntries, f)
	n.directFiles++
	if !f.HardLink {
		n.directSize += f.Apparent
		n.directDisk += f.Disk
	}
}

// MarkReadError records that this directory itself could not be read fully.
func (n *Node) MarkReadError() {
	n.directHasError = true
	n.directErrors++
}

// ReadError reports whether this directory itself (not a descendant) had a
// read error.
func (n *Node) ReadError() bool { return n.directHasError }

type entKind uint8

const (
	entFile entKind = iota
	entDir
	entLink    // junction / mount point / directory symlink: never descended
	entSymlink // file symlink
	entErr     // entry could not be stat'ed
)

// fileKey identifies a file across hard links (volume + file id / dev + ino).
type fileKey struct{ dev, ino uint64 }

// dirEnt is one listed directory entry, produced by the platform listDir.
type dirEnt struct {
	name  string
	kind  entKind
	size  int64
	disk  int64
	mtime int64
	key   fileKey // zero when the file cannot have (or has no) extra hard links
}

const linkShards = 64

type linkSet struct {
	mu   [linkShards]sync.Mutex
	seen [linkShards]map[fileKey]struct{}
}

// firstSeen reports whether k is seen for the first time in this scan.
func (l *linkSet) firstSeen(k fileKey) bool {
	i := (k.ino ^ k.dev*0x9E3779B97F4A7C15) % linkShards
	l.mu[i].Lock()
	defer l.mu[i].Unlock()
	if l.seen[i] == nil {
		l.seen[i] = make(map[fileKey]struct{})
	}
	if _, ok := l.seen[i][k]; ok {
		return false
	}
	l.seen[i][k] = struct{}{}
	return true
}

type scanState struct {
	queue    chan *Node
	pending  atomic.Int64
	done     atomic.Int64
	files    atomic.Int64
	bytes    atomic.Int64
	canceled atomic.Bool
	links    linkSet
	trustIDs bool
	exclude  []string
	opt      Options
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
	s.trustIDs = !opt.CountLinks && trustFileIDs(root)
	for _, p := range opt.Exclude {
		if p = strings.TrimSpace(p); p != "" {
			s.exclude = append(s.exclude, foldCase(filepath.FromSlash(p)))
		}
	}
	rootNode := &Node{Name: filepath.Base(root), Path: root}

	var wg sync.WaitGroup
	for i := 0; i < opt.Workers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for n := range s.queue {
				if !s.canceled.Load() && opt.Cancel != nil {
					select {
					case <-opt.Cancel:
						s.canceled.Store(true)
					default:
					}
				}
				if !s.canceled.Load() {
					s.scanOne(n)
				}
				// The last worker to finish closes the queue, releasing everyone.
				// pending is incremented on enqueue and decremented here, so it
				// reaches zero only after every enqueued node has been drained.
				if s.pending.Add(-1) == 0 {
					close(s.queue)
				}
			}
		}()
	}

	s.enqueue(rootNode)
	wg.Wait()
	if opt.Progress != nil && !s.canceled.Load() {
		opt.Progress(s.snapshot()) // final live snapshot matches the authoritative totals
	}
	aggregate(rootNode)
	return rootNode, nil
}

// NotDirError is returned when the scan root is not a directory.
type NotDirError struct{ Path string }

func (e *NotDirError) Error() string { return e.Path + " is not a directory" }

func (s *scanState) snapshot() ProgressState {
	return ProgressState{DirsDone: s.done.Load(), Files: s.files.Load(), Bytes: s.bytes.Load()}
}

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

func foldCase(s string) string {
	if runtime.GOOS == "windows" {
		return strings.ToLower(s)
	}
	return s
}

func (s *scanState) excluded(name, path string) bool {
	if len(s.exclude) == 0 {
		return false
	}
	name, path = foldCase(name), foldCase(path)
	for _, p := range s.exclude {
		target := name
		if strings.ContainsRune(p, filepath.Separator) {
			target = path
		}
		if ok, _ := filepath.Match(p, target); ok {
			return true
		}
	}
	return false
}

func (s *scanState) scanOne(n *Node) {
	if s.opt.Progress != nil {
		defer func() {
			if d := s.done.Add(1); d == 1 || d%32 == 0 {
				s.opt.Progress(s.snapshot())
			}
		}()
	}
	entries, err := listDir(n.Path, s.opt.DiskUsage)
	if err != nil {
		n.MarkReadError()
		return
	}
	for i := range entries {
		e := &entries[i]
		if len(s.exclude) > 0 && s.excluded(e.name, filepath.Join(n.Path, e.name)) {
			continue
		}
		switch e.kind {
		case entErr:
			n.MarkReadError()
		case entLink:
			// Junction, mount point or directory symlink: never descend
			// (loops, double counting) and never count it as a file.
			n.directLinks++
		case entSymlink:
			if s.opt.FollowSymlinks {
				n.directSize += e.size
				n.directDisk += e.size
				n.directFiles++
			} else {
				n.directLinks++
			}
		case entDir:
			child := &Node{Name: e.name, Path: filepath.Join(n.Path, e.name)}
			n.Children = append(n.Children, child)
			s.enqueue(child)
		default:
			disk := e.size
			if s.opt.DiskUsage {
				disk = e.disk
			}
			dup := s.trustIDs && e.key != (fileKey{}) && !s.links.firstSeen(e.key)
			n.directFiles++
			s.files.Add(1)
			if !dup {
				n.directSize += e.size
				n.directDisk += disk
				s.bytes.Add(e.size)
			}
			if s.opt.KeepFiles {
				n.FileEntries = append(n.FileEntries, FileEntry{
					Name: e.name, Apparent: e.size, Disk: disk, MTime: e.mtime, HardLink: dup,
				})
			}
		}
	}
}

// aggregate folds child totals into parents and marks error subtrees.
// It recomputes totals from the direct* counters, so it is safe to call
// repeatedly after tree edits (see Reaggregate).
func aggregate(n *Node) {
	n.Size = n.directSize
	n.Files = n.directFiles
	n.Disk = n.directDisk
	n.Dirs = int64(len(n.Children))
	n.HasError = n.directHasError
	n.Errors = n.directErrors
	n.Links = n.directLinks
	for _, c := range n.Children {
		aggregate(c)
		n.Size += c.Size
		n.Disk += c.Disk
		n.Files += c.Files
		n.Dirs += c.Dirs
		if c.HasError {
			n.HasError = true
		}
		n.Errors += c.Errors
		n.Links += c.Links
	}
}

// Reaggregate recomputes subtree totals after the tree was edited in place
// (e.g. an item was removed by the TUI after a Recycle Bin move).
func Reaggregate(root *Node) { aggregate(root) }

// Replace swaps the contents of old (a node inside some tree) for a freshly
// scanned subtree, keeping old's identity so parents stay linked.
// Call Reaggregate on the root afterwards.
func Replace(old, fresh *Node) {
	name := old.Name
	*old = *fresh
	old.Name = name
}

// RemoveChild removes a direct child directory or file entry by name and
// reports whether anything was removed. Call Reaggregate afterwards.
func RemoveChild(parent *Node, name string) bool {
	for i, c := range parent.Children {
		if c.Name == name {
			parent.Children = append(parent.Children[:i], parent.Children[i+1:]...)
			return true
		}
	}
	for i, f := range parent.FileEntries {
		if f.Name == name {
			parent.FileEntries = append(parent.FileEntries[:i], parent.FileEntries[i+1:]...)
			parent.directFiles--
			if !f.HardLink {
				parent.directSize -= f.Apparent
				parent.directDisk -= f.Disk
			}
			return true
		}
	}
	return false
}

// listDirGeneric lists a directory through os.ReadDir + lstat. It is the
// only implementation on POSIX and the fallback on Windows filesystems that
// do not support the fast FileIdBothDirectoryInfo enumeration.
func listDirGeneric(path string, wantDisk bool) ([]dirEnt, error) {
	entries, err := os.ReadDir(path)
	if err != nil {
		return nil, err
	}
	out := make([]dirEnt, 0, len(entries))
	for _, e := range entries {
		d := dirEnt{name: e.Name()}
		info, err := e.Info() // lstat semantics: does not follow links
		if err != nil {
			if os.IsNotExist(err) {
				continue // vanished between readdir and lstat
			}
			d.kind = entErr
			out = append(out, d)
			continue
		}
		mode := info.Mode()
		switch {
		case isDirReparsePoint(info):
			d.kind = entLink
		case mode&os.ModeSymlink != 0:
			d.kind = entSymlink
			d.size = info.Size()
		case mode.IsDir():
			d.kind = entDir
		case mode&os.ModeIrregular != 0 && isReparsePoint(info):
			d.kind = entLink
		default:
			d.kind = entFile
			d.size = info.Size()
			d.disk = d.size
			if wantDisk {
				d.disk = diskSize(filepath.Join(path, d.name), info)
			}
			d.mtime = info.ModTime().UnixMilli()
			d.key = hardLinkKey(info)
		}
		out = append(out, d)
	}
	return out, nil
}
