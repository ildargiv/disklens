// Command disklens is a fast parallel disk usage analyzer for Windows
// (DiskLens by KopiTehnik).
package main

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"disklens/internal/buildinfo"
	"disklens/internal/mcpserver"
	"disklens/internal/ncdujson"
	"disklens/internal/scanner"
	"disklens/internal/tui"
)

// Exit codes.
const (
	exitOK         = 0
	exitError      = 1
	exitIncomplete = 2 // --strict and some directories could not be read
)

type stringList []string

func (s *stringList) String() string     { return strings.Join(*s, ",") }
func (s *stringList) Set(v string) error { *s = append(*s, v); return nil }

type config struct {
	workers     int
	depth       int
	top         int
	showItems   bool
	ascii       bool
	si          bool
	apparent    bool
	summarize   bool
	exportPath  string
	importPath  string
	noTUI       bool
	showVersion bool
	jsonOut     bool
	showFiles   bool
	sortBy      string
	minSizeStr  string
	minSize     int64
	excludes    stringList
	excludeFrom string
	quiet       bool
	strict      bool
	countLinks  bool
}

func main() {
	// Subcommand dispatch must happen before flag parsing.
	if len(os.Args) > 1 {
		switch os.Args[1] {
		case "version":
			fmt.Println(buildinfo.FullName, buildinfo.Version)
			return
		case "mcp":
			if err := mcpserver.Run(context.Background(), os.Args[2:]); err != nil {
				fatal(err)
			}
			return
		}
	}
	os.Exit(run())
}

func run() int {
	var c config
	flag.Usage = usage
	flag.IntVar(&c.workers, "workers", 0, "parallel scanning goroutines (default: number of CPUs)")
	flag.IntVar(&c.depth, "depth", 1, "directory levels to display (0 = whole tree)")
	flag.IntVar(&c.top, "top", 0, "show only the N largest entries per level (0 = all)")
	flag.BoolVar(&c.showItems, "items", true, "show file/dir counts")
	flag.BoolVar(&c.ascii, "ascii", false, "ASCII size bars instead of unicode blocks")
	flag.BoolVar(&c.si, "si", false, "decimal units (kB/MB/GB) instead of binary (KiB/MiB/GiB)")
	flag.BoolVar(&c.apparent, "apparent", false, "report apparent (logical) size instead of size on disk")
	flag.BoolVar(&c.summarize, "summarize", false, "print only the totals")
	flag.StringVar(&c.exportPath, "o", "", "export the scan as ncdu-compatible JSON ('-' for stdout)")
	flag.StringVar(&c.importPath, "f", "", "import a scan from an ncdu/disklens JSON file instead of scanning")
	flag.BoolVar(&c.noTUI, "no-tui", false, "print a plain tree instead of opening the interactive TUI")
	flag.BoolVar(&c.showVersion, "version", false, "print version and exit")
	flag.BoolVar(&c.jsonOut, "json", false, "print the result as JSON (stable schema, same field names as the MCP tools)")
	flag.BoolVar(&c.showFiles, "files", false, "list files too, not only directories")
	flag.StringVar(&c.sortBy, "sort", "size", "order of entries: size, name or files")
	flag.StringVar(&c.minSizeStr, "min-size", "", "hide entries smaller than this (e.g. 100M, 1.5G)")
	flag.Var(&c.excludes, "exclude", "skip entries matching a glob (repeatable; a pattern with a path separator matches the full path)")
	flag.StringVar(&c.excludeFrom, "exclude-from", "", "read --exclude patterns from a file, one per line")
	flag.BoolVar(&c.quiet, "quiet", false, "no live progress on stderr")
	flag.BoolVar(&c.strict, "strict", false, "exit with code 2 when some directories could not be read")
	flag.BoolVar(&c.countLinks, "count-links", false, "count every hard link of a file separately (default: once)")

	var dirArg string
	flag.Parse()
	// Accept flags both before and after the directory argument:
	// "disklens C:\\photos -o out.json" must work like "disklens -o out.json C:\\photos".
	if rest := flag.Args(); len(rest) > 0 {
		dirArg = rest[0]
		if err := flag.CommandLine.Parse(rest[1:]); err != nil {
			return exitError
		}
		if extra := flag.Args(); len(extra) > 0 {
			return fail(fmt.Errorf("unexpected arguments: %s (only one directory can be scanned at a time)", strings.Join(extra, " ")))
		}
	}
	if c.showVersion {
		fmt.Println(buildinfo.FullName, buildinfo.Version)
		return exitOK
	}
	switch c.sortBy {
	case "size", "name", "files":
	default:
		return fail(fmt.Errorf("--sort must be size, name or files, got %q", c.sortBy))
	}
	if c.minSizeStr != "" {
		v, err := parseSize(c.minSizeStr)
		if err != nil {
			return fail(err)
		}
		c.minSize = v
	}
	if c.excludeFrom != "" {
		pats, err := readLines(c.excludeFrom)
		if err != nil {
			return fail(err)
		}
		c.excludes = append(c.excludes, pats...)
	}
	dirArg = normalizeDirArg(dirArg)

	// Interactive mode: a TTY, no import, no machine output -> the TUI owns the scan.
	if c.importPath == "" && !c.noTUI && !c.summarize && !c.jsonOut && c.exportPath == "" && stdoutIsTTY() {
		abs, err := filepath.Abs(dirArg)
		if err != nil {
			return fail(err)
		}
		if err := tui.Run(abs, tui.Options{Exclude: c.excludes, CountLinks: c.countLinks}); err != nil {
			return fail(err)
		}
		return exitOK
	}

	start := time.Now()
	var (
		root         *scanner.Node
		diskMeasured bool
	)
	if c.importPath != "" {
		f, err := os.Open(c.importPath)
		if err != nil {
			return fail(err)
		}
		root, err = ncdujson.Import(bufio.NewReader(f))
		f.Close()
		if err != nil {
			return fail(err)
		}
		diskMeasured = true // ncdu dumps carry dsize; missing values were estimated
	} else {
		abs, err := filepath.Abs(dirArg)
		if err != nil {
			return fail(err)
		}
		opts := scanner.Options{
			Workers:    c.workers,
			DiskUsage:  !c.apparent,
			KeepFiles:  c.exportPath != "" || c.showFiles,
			CountLinks: c.countLinks,
			Exclude:    c.excludes,
		}
		var stopProgress func()
		if !c.quiet && stderrIsTTY() {
			opts.Progress, stopProgress = progressPrinter(os.Stderr, c.si)
		}
		root, err = scanner.Scan(abs, opts)
		if stopProgress != nil {
			stopProgress()
		}
		if err != nil {
			return fail(err)
		}
		diskMeasured = !c.apparent
	}
	elapsed := time.Since(start)

	if c.exportPath != "" {
		if err := export(root, c.exportPath, diskMeasured); err != nil {
			return fail(err)
		}
		if c.exportPath == "-" {
			return exitCode(c, root)
		}
	}

	if c.jsonOut {
		if err := writeJSON(os.Stdout, root, c, elapsed); err != nil {
			return fail(err)
		}
		return exitCode(c, root)
	}

	// When piped, plain ASCII output is safer for other tools.
	if !stdoutIsTTY() {
		c.ascii = true
	}
	w := bufio.NewWriter(os.Stdout)
	defer w.Flush()
	if !c.summarize {
		render(w, root, c.depth, 0, c)
	}
	fmt.Fprintf(w, "\ntotal %s", human(metric(root, c), c.si))
	if !c.apparent {
		fmt.Fprintf(w, " on disk (%s apparent)", human(root.Size, c.si))
	}
	fmt.Fprintf(w, "  |  %s files, %s dirs  |  %s  |  %.2fs",
		humanInt(root.Files), humanInt(root.Dirs), root.Path, elapsed.Seconds())
	if root.Errors > 0 {
		fmt.Fprintf(w, "  |  %s dirs inaccessible (totals are incomplete)", humanInt(root.Errors))
	}
	if root.Links > 0 {
		fmt.Fprintf(w, "  |  %s links/junctions not followed", humanInt(root.Links))
	}
	fmt.Fprintln(w)
	return exitCode(c, root)
}

// normalizeDirArg repairs what Windows shells hand us for drive roots.
// Explorer passes "%V" for a drive as `"C:\"`, and the C runtime argument
// rules turn the trailing `\"` into a literal quote: the program sees `C:"`.
// A bare `C:` would mean "current directory on drive C". Both become `C:\`.
func normalizeDirArg(arg string) string {
	arg = strings.TrimRight(strings.TrimSpace(arg), `"`)
	if arg == "" {
		return "."
	}
	if len(arg) == 2 && arg[1] == ':' && isLetter(arg[0]) {
		return arg + `\`
	}
	return arg
}

func isLetter(b byte) bool { return (b|0x20) >= 'a' && (b|0x20) <= 'z' }

func exitCode(c config, root *scanner.Node) int {
	if c.strict && root.Errors > 0 {
		return exitIncomplete
	}
	return exitOK
}

func usage() {
	out := flag.CommandLine.Output()
	fmt.Fprintf(out, `%s %s — fast disk usage analyzer for Windows

Usage:
  disklens [flags] [directory]     interactive browser (TUI) or plain tree when piped
  disklens mcp                     MCP server for AI agents (stdio)
  disklens version

Examples:
  disklens C:\Users\me                     browse interactively
  disklens --no-tui --depth 2 --top 10 .   two levels, 10 largest per level
  disklens --json --files --min-size 1G D:\  machine-readable, big items only
  disklens --exclude node_modules --exclude .git C:\src

Flags:
`, buildinfo.FullName, buildinfo.Version)
	flag.PrintDefaults()
}

// progressPrinter returns a throttled scanner progress callback that rewrites
// one stderr line, and a stop function that clears it.
func progressPrinter(w io.Writer, si bool) (func(scanner.ProgressState), func()) {
	var (
		mu      sync.Mutex
		last    time.Time
		printed bool
		width   int
	)
	cb := func(p scanner.ProgressState) {
		mu.Lock()
		defer mu.Unlock()
		if time.Since(last) < 150*time.Millisecond {
			return
		}
		last = time.Now()
		line := fmt.Sprintf("scanning… %s dirs, %s files, %s", humanInt(p.DirsDone), humanInt(p.Files), human(p.Bytes, si))
		if len(line) < width {
			line += strings.Repeat(" ", width-len(line))
		}
		width = len(line)
		fmt.Fprint(w, "\r"+line)
		printed = true
	}
	stop := func() {
		mu.Lock()
		defer mu.Unlock()
		if printed {
			fmt.Fprint(w, "\r"+strings.Repeat(" ", width)+"\r")
		}
		last = time.Now().Add(time.Hour) // swallow late callbacks
	}
	return cb, stop
}

func export(root *scanner.Node, path string, diskMeasured bool) error {
	opt := ncdujson.ExportOptions{DiskMeasured: diskMeasured, Version: buildinfo.Version}
	if path == "-" {
		bw := bufio.NewWriter(os.Stdout)
		if err := ncdujson.Export(bw, root, opt); err != nil {
			return err
		}
		return bw.Flush()
	}
	f, err := os.Create(path)
	if err != nil {
		return err
	}
	bw := bufio.NewWriter(f)
	if err := ncdujson.Export(bw, root, opt); err != nil {
		f.Close()
		return err
	}
	if err := bw.Flush(); err != nil {
		f.Close()
		return err
	}
	return f.Close()
}

func stdoutIsTTY() bool { return isTTY(os.Stdout) }
func stderrIsTTY() bool { return isTTY(os.Stderr) }

func isTTY(f *os.File) bool {
	info, err := f.Stat()
	return err == nil && info.Mode()&os.ModeCharDevice != 0
}

// row is one displayable entry: a directory node or a file.
type row struct {
	name     string
	path     string
	isDir    bool
	apparent int64
	disk     int64
	files    int64
	dirs     int64
	errors   int64
	hardLink bool
	node     *scanner.Node
}

func metric(n *scanner.Node, c config) int64 {
	if c.apparent {
		return n.Size
	}
	return n.Disk
}

func (r row) size(c config) int64 {
	if c.apparent {
		return r.apparent
	}
	return r.disk
}

// rows returns n's entries after --files, --min-size and --sort.
func rows(n *scanner.Node, c config) []row {
	out := make([]row, 0, len(n.Children))
	for _, k := range n.Children {
		out = append(out, row{name: k.Name, path: k.Path, isDir: true, apparent: k.Size, disk: k.Disk,
			files: k.Files, dirs: k.Dirs, errors: k.Errors, node: k})
	}
	if c.showFiles {
		for _, f := range n.FileEntries {
			out = append(out, row{name: f.Name, path: filepath.Join(n.Path, f.Name), apparent: f.Apparent,
				disk: f.Disk, files: 1, hardLink: f.HardLink})
		}
	}
	if c.minSize > 0 {
		kept := out[:0]
		for _, r := range out {
			if r.size(c) >= c.minSize {
				kept = append(kept, r)
			}
		}
		out = kept
	}
	switch c.sortBy {
	case "name":
		sort.Slice(out, func(i, j int) bool { return strings.ToLower(out[i].name) < strings.ToLower(out[j].name) })
	case "files":
		sort.SliceStable(out, func(i, j int) bool { return out[i].files > out[j].files })
	default:
		sort.SliceStable(out, func(i, j int) bool { return out[i].size(c) > out[j].size(c) })
	}
	return out
}

// render prints n's entries; remaining is the number of levels still to
// print (1 = only this level, 0 = unlimited).
func render(w io.Writer, n *scanner.Node, remaining, indent int, c config) {
	kids := rows(n, c)
	var rest []row
	if c.top > 0 && len(kids) > c.top {
		kids, rest = kids[:c.top], kids[c.top:]
	}
	parent := metric(n, c)
	for _, k := range kids {
		fmt.Fprintf(w, "%*s%9s %s %s", indent, "", human(k.size(c), c.si), sizeBar(k.size(c), parent, c.ascii), k.name)
		if k.isDir {
			fmt.Fprint(w, string(filepath.Separator))
			if c.showItems {
				fmt.Fprintf(w, "   (%s files, %s dirs)", humanInt(k.files), humanInt(k.dirs))
			}
			if k.errors > 0 {
				fmt.Fprint(w, "  !unreadable")
			}
		} else if k.hardLink {
			fmt.Fprint(w, "  (hard link, counted once)")
		}
		fmt.Fprintln(w)
		if k.isDir && remaining != 1 {
			render(w, k.node, remaining-1, indent+2, c)
		}
	}
	if len(rest) > 0 {
		var sum, files, dirs int64
		for _, r := range rest {
			sum += r.size(c)
			files += r.files
			dirs += r.dirs
		}
		fmt.Fprintf(w, "%*s%9s %s + %d more (%s files, %s dirs)\n",
			indent, "", human(sum, c.si), sizeBar(sum, parent, c.ascii), len(rest), humanInt(files), humanInt(dirs))
	}
}

type jsonEntry struct {
	Type         string      `json:"type"`
	Name         string      `json:"name"`
	Path         string      `json:"path"`
	Apparent     int64       `json:"size_apparent_bytes"`
	Disk         int64       `json:"size_disk_bytes"`
	Files        int64       `json:"files"`
	Dirs         int64       `json:"dirs,omitempty"`
	Inaccessible int64       `json:"inaccessible_dirs,omitempty"`
	HardLink     bool        `json:"hard_link,omitempty"`
	Children     []jsonEntry `json:"children,omitempty"`
	More         *jsonMore   `json:"more,omitempty"`
}

type jsonMore struct {
	Count    int   `json:"count"`
	Apparent int64 `json:"size_apparent_bytes"`
	Disk     int64 `json:"size_disk_bytes"`
}

type jsonResult struct {
	Tool         string      `json:"tool"`
	Version      string      `json:"version"`
	Path         string      `json:"path"`
	Complete     bool        `json:"complete"`
	Apparent     int64       `json:"size_apparent_bytes"`
	Disk         int64       `json:"size_disk_bytes"`
	Files        int64       `json:"files"`
	Dirs         int64       `json:"dirs"`
	Inaccessible int64       `json:"inaccessible_dirs"`
	SkippedLinks int64       `json:"skipped_links"`
	Seconds      float64     `json:"scan_seconds"`
	Children     []jsonEntry `json:"children"`
	More         *jsonMore   `json:"more,omitempty"`
}

func writeJSON(w io.Writer, root *scanner.Node, c config, elapsed time.Duration) error {
	res := jsonResult{
		Tool: "disklens", Version: buildinfo.Version, Path: root.Path, Complete: root.Errors == 0,
		Apparent: root.Size, Disk: root.Disk, Files: root.Files, Dirs: root.Dirs,
		Inaccessible: root.Errors, SkippedLinks: root.Links, Seconds: elapsed.Seconds(),
		Children: []jsonEntry{},
	}
	if !c.summarize {
		res.Children, res.More = jsonChildren(root, c.depth, c)
	}
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	return enc.Encode(res)
}

func jsonChildren(n *scanner.Node, remaining int, c config) ([]jsonEntry, *jsonMore) {
	kids := rows(n, c)
	var more *jsonMore
	if c.top > 0 && len(kids) > c.top {
		more = &jsonMore{Count: len(kids) - c.top}
		for _, r := range kids[c.top:] {
			more.Apparent += r.apparent
			more.Disk += r.disk
		}
		kids = kids[:c.top]
	}
	out := make([]jsonEntry, 0, len(kids))
	for _, k := range kids {
		e := jsonEntry{Type: "file", Name: k.name, Path: k.path, Apparent: k.apparent, Disk: k.disk,
			Files: k.files, HardLink: k.hardLink}
		if k.isDir {
			e.Type, e.Dirs, e.Inaccessible = "dir", k.dirs, k.errors
			if remaining != 1 {
				e.Children, e.More = jsonChildren(k.node, remaining-1, c)
			}
		}
		out = append(out, e)
	}
	return out, more
}

func sizeBar(size, parent int64, ascii bool) string {
	const width = 12
	filled := 0
	if parent > 0 {
		filled = int(float64(size) / float64(parent) * width)
	}
	if filled > width {
		filled = width
	}
	if filled == 0 && size > 0 {
		filled = 1
	}
	if ascii {
		return "[" + strings.Repeat("#", filled) + strings.Repeat(".", width-filled) + "]"
	}
	return "[" + strings.Repeat("█", filled) + strings.Repeat("░", width-filled) + "]"
}

func human(n int64, si bool) string {
	unit := int64(1024)
	suffix := "iB"
	if si {
		unit = 1000
		suffix = "B"
	}
	if n < unit {
		return fmt.Sprintf("%d B", n)
	}
	div, exp := unit, 0
	for v := n / unit; v >= unit; v /= unit {
		div *= unit
		exp++
	}
	prefix := "KMGTPE"
	if si {
		prefix = "kMGTPE"
	}
	return fmt.Sprintf("%.1f %c%s", float64(n)/float64(div), prefix[exp], suffix)
}

// parseSize parses "1500", "100K", "1.5G", "2TiB", "300MB" (binary units).
func parseSize(s string) (int64, error) {
	t := strings.ToUpper(strings.TrimSpace(s))
	t = strings.TrimSuffix(strings.TrimSuffix(t, "B"), "I")
	mult := int64(1)
	if t != "" {
		if i := strings.IndexByte("KMGTPE", t[len(t)-1]); i >= 0 {
			mult = int64(1) << (10 * (i + 1))
			t = t[:len(t)-1]
		}
	}
	v, err := strconv.ParseFloat(strings.TrimSpace(t), 64)
	if err != nil || v < 0 {
		return 0, fmt.Errorf("invalid size %q (examples: 500K, 100M, 1.5G)", s)
	}
	return int64(v * float64(mult)), nil
}

func readLines(path string) ([]string, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	var out []string
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line != "" && !strings.HasPrefix(line, "#") {
			out = append(out, line)
		}
	}
	return out, sc.Err()
}

func humanInt(n int64) string {
	s := strconv.FormatInt(n, 10)
	if n < 1000 {
		return s
	}
	var parts []string
	for len(s) > 3 {
		parts = append([]string{s[len(s)-3:]}, parts...)
		s = s[:len(s)-3]
	}
	parts = append([]string{s}, parts...)
	return strings.Join(parts, " ")
}

func fail(err error) int {
	var pe *os.PathError
	if errors.As(err, &pe) {
		err = fmt.Errorf("%s: %v", pe.Path, pe.Err)
	}
	fmt.Fprintln(os.Stderr, "disklens:", err)
	return exitError
}

func fatal(err error) { os.Exit(fail(err)) }
