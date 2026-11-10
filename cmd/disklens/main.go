// Command disklens is a fast parallel disk usage analyzer.
package main

import (
	"bufio"
	"context"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"disklens/internal/buildinfo"
	"disklens/internal/mcpserver"
	"disklens/internal/ncdujson"
	"disklens/internal/scanner"
	"disklens/internal/tui"
)

func main() {
	// Subcommand dispatch must happen before flag parsing.
	if len(os.Args) > 1 && os.Args[1] == "version" {
		fmt.Println("disklens", buildinfo.Version)
		return
	}
	if len(os.Args) > 1 && os.Args[1] == "mcp" {
		if err := mcpserver.Run(context.Background(), os.Args[2:]); err != nil {
			fatal(err)
		}
		return
	}
	workers := flag.Int("workers", 0, "parallel scanning goroutines (default: number of CPUs)")
	depth := flag.Int("depth", 1, "directory levels to display (0 = whole tree)")
	top := flag.Int("top", 0, "show only the N largest children per level (0 = all)")
	showItems := flag.Bool("items", true, "show file/dir counts")
	ascii := flag.Bool("ascii", false, "ASCII size bars instead of unicode blocks")
	si := flag.Bool("si", false, "decimal units (kB/MB/GB) instead of binary (KiB/MiB/GiB)")
	apparent := flag.Bool("apparent", false, "report apparent size instead of size on disk")
	summarize := flag.Bool("summarize", false, "print only the totals")
	exportPath := flag.String("o", "", "export the scan as ncdu-compatible JSON ('-' for stdout)")
	importPath := flag.String("f", "", "import a scan from an ncdu/disklens JSON file instead of scanning")
	noTUI := flag.Bool("no-tui", false, "print a plain tree instead of opening the interactive TUI")
	showVersion := flag.Bool("version", false, "print version and exit")
	var (
		root         *scanner.Node
		err          error
		abs          string
		dirArg       *string
		diskMeasured bool
		start        = time.Now()
	)

	flag.Parse()
	// Accept flags both before and after the directory argument:
	// "disklens C:\\photos -o out.json" must work like "disklens -o out.json C:\\photos".
	if rest := flag.Args(); len(rest) > 0 {
		dirArg = &rest[0]
		flag.CommandLine.Parse(rest[1:])
	}
	if *showVersion {
		fmt.Println("disklens", buildinfo.Version)
		return
	}

	// Interactive mode: a TTY, no import, not --summarize -> the TUI owns the scan.
	if *importPath == "" && !*noTUI && !*summarize && stdoutIsTTY() {
		dir := "."
		if dirArg != nil {
			dir = *dirArg
		}
		tuiAbs, terr := filepath.Abs(dir)
		if terr != nil {
			fatal(terr)
		}
		if terr := tui.Run(tuiAbs); terr != nil {
			fatal(terr)
		}
		return
	}

	if *importPath != "" {
		f, ferr := os.Open(*importPath)
		if ferr != nil {
			fatal(ferr)
		}
		defer f.Close()
		root, err = ncdujson.Import(f)
		if err != nil {
			fatal(err)
		}
		abs = root.Path
		diskMeasured = true // ncdu dumps carry dsize; missing values were estimated
	} else {
		dir := "."
		if dirArg != nil {
			dir = *dirArg
		}
		abs, err = filepath.Abs(dir)
		if err != nil {
			fatal(err)
		}
		root, err = scanner.Scan(abs, scanner.Options{
			Workers:   *workers,
			DiskUsage: !*apparent,
			KeepFiles: *exportPath != "",
		})
		if err != nil {
			fatal(err)
		}
		diskMeasured = !*apparent
	}

	if *exportPath != "" {
		if err := export(root, *exportPath, diskMeasured); err != nil {
			fatal(err)
		}
	}

	// When piped, plain ASCII output is safer for other tools.
	if !stdoutIsTTY() {
		*ascii = true
	}

	if !*summarize {
		render(os.Stdout, root, *depth, *top, *showItems, *ascii, *si, 0)
	}

	fmt.Printf("\ntotal %s", human(root.Size, *si))
	if !*apparent && root.Disk > 0 {
		fmt.Printf(" on disk (%s apparent)", human(root.Size, *si))
	}
	fmt.Printf("  |  %s files, %s dirs  |  %s  |  %.2fs", humanInt(root.Files), humanInt(root.Dirs), abs, time.Since(start).Seconds())
	if root.Errors > 0 {
		fmt.Printf("  |  %s dirs inaccessible", humanInt(root.Errors))
	}
	fmt.Println()
}

func export(root *scanner.Node, path string, diskMeasured bool) error {
	if path == "-" {
		return ncdujson.Export(os.Stdout, root, ncdujson.ExportOptions{DiskMeasured: diskMeasured})
	}
	f, err := os.Create(path)
	if err != nil {
		return err
	}
	defer f.Close()
	bw := bufio.NewWriter(f)
	if err := ncdujson.Export(bw, root, ncdujson.ExportOptions{DiskMeasured: diskMeasured}); err != nil {
		return err
	}
	return bw.Flush()
}

func stdoutIsTTY() bool {
	info, err := os.Stdout.Stat()
	return err == nil && info.Mode()&os.ModeCharDevice != 0
}

func render(w io.Writer, n *scanner.Node, remaining, top int, showItems, ascii, si bool, indent int) {
	kids := append([]*scanner.Node(nil), n.Children...)
	sort.Slice(kids, func(i, j int) bool { return kids[i].Size > kids[j].Size })
	if top > 0 && len(kids) > top {
		rest := kids[top:]
		kids = kids[:top]
		defer func() {
			var sum, files, dirs int64
			for _, r := range rest {
				sum += r.Size
				files += r.Files
				dirs += r.Dirs
			}
			fmt.Fprintf(w, "%*s  %9s       + %d more (%s, %s files, %s dirs)\n",
				indent, "", human(sum, si), len(rest), human(sum, si), humanInt(files), humanInt(dirs))
		}()
	}
	for _, c := range kids {
		fmt.Fprintf(w, "%*s%s %s %s", indent, "", human(c.Size, si), sizeBar(c.Size, n.Size, ascii), c.Name)
		if showItems {
			fmt.Fprintf(w, "   (%s files, %s dirs)", humanInt(c.Files), humanInt(c.Dirs))
		}
		if c.HasError {
			fmt.Fprint(w, "  !unreadable")
		}
		fmt.Fprintln(w)
		if remaining != 0 {
			render(w, c, remaining-1, top, showItems, ascii, si, indent+2)
		}
	}
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
	return fmt.Sprintf("%.1f %c%s", float64(n)/float64(div), "kMGTPE"[exp], suffix)
}

func humanInt(n int64) string {
	s := fmt.Sprintf("%d", n)
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

func fatal(err error) {
	fmt.Fprintln(os.Stderr, "disklens:", err)
	os.Exit(1)
}
