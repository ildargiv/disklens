// Command disklens is a fast parallel disk usage analyzer.
package main

import (
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"disklens/internal/scanner"
)

func main() {
	workers := flag.Int("workers", 0, "parallel scanning goroutines (default: number of CPUs)")
	depth := flag.Int("depth", 1, "directory levels to display (0 = whole tree)")
	top := flag.Int("top", 0, "show only the N largest children per level (0 = all)")
	showItems := flag.Bool("items", true, "show file/dir counts")
	ascii := flag.Bool("ascii", false, "ASCII size bars instead of unicode blocks")
	flag.Parse()

	dir := "."
	if flag.NArg() > 0 {
		dir = flag.Arg(0)
	}
	abs, err := filepath.Abs(dir)
	if err != nil {
		fatal(err)
	}

	start := time.Now()
	root, err := scanner.Scan(abs, scanner.Options{Workers: *workers})
	if err != nil {
		fatal(err)
	}

	render(os.Stdout, root, *depth, *top, *showItems, *ascii, 0)
	fmt.Printf("\ntotal %s  |  %s files, %s dirs  |  %s  |  %.2fs",
		human(root.Size), humanInt(root.Files), humanInt(root.Dirs), abs, time.Since(start).Seconds())
	if root.Errors > 0 {
		fmt.Printf("  |  %s dirs inaccessible", humanInt(root.Errors))
	}
	fmt.Println()
}

func render(w io.Writer, n *scanner.Node, remaining, top int, showItems, ascii bool, indent int) {
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
				indent, "", human(sum), len(rest), human(sum), humanInt(files), humanInt(dirs))
		}()
	}
	for _, c := range kids {
		fmt.Fprintf(w, "%*s%s %s %s", indent, "", human(c.Size), sizeBar(c.Size, n.Size, ascii), c.Name)
		if showItems {
			fmt.Fprintf(w, "   (%s files, %s dirs)", humanInt(c.Files), humanInt(c.Dirs))
		}
		if c.HasError {
			fmt.Fprint(w, "  !unreadable")
		}
		fmt.Fprintln(w)
		if remaining != 0 {
			render(w, c, remaining-1, top, showItems, ascii, indent+2)
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

func human(n int64) string {
	const unit = 1024
	if n < unit {
		return fmt.Sprintf("%d B", n)
	}
	div, exp := int64(unit), 0
	for v := n / unit; v >= unit; v /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.1f %ciB", float64(n)/float64(div), "KMGTPE"[exp])
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
