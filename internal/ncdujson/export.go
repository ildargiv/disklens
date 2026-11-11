// Package ncdujson reads and writes the JSON export format used by ncdu 1.9+
// (https://dev.yorhel.nl/ncdu/jsonfmt) so that disklens scans can be inspected
// with ncdu and ncdu dumps can be browsed with disklens.
//
// Format summary:
//
//	[<major=1>, <minor>, <metadata>, <directory>]
//	<directory> = [<infoblock>, <directory>..., <infoblock>...]
//
// Files are bare infoblock objects; subdirectories are arrays. The root
// infoblock name is the absolute path; names below it are plain names.
package ncdujson

import (
	"encoding/json"
	"fmt"
	"io"
	"time"

	"disklens/internal/scanner"
)

// minorEmitted matches the ncdu >= 1.16 minor version (we do not emit
// nlink/ino fields yet, which is valid: they are optional).
const minorEmitted = 2

// ExportOptions controls the export.
type ExportOptions struct {
	Generator string // defaults to "disklens"
	Version   string // defaults to "0.1"
	// DiskMeasured must be true when Node.Disk holds real on-disk sizes.
	// When false, dsize is written as a copy of asize (best known estimate).
	DiskMeasured bool
}

type infoBlock struct {
	Name      string `json:"name"`
	Asize     int64  `json:"asize,omitempty"`
	Dsize     int64  `json:"dsize,omitempty"`
	ReadError bool   `json:"read_error,omitempty"`
	NotReg    bool   `json:"notreg,omitempty"`
}

// Export writes root in ncdu JSON export format. The scanner must have been
// run with KeepFiles enabled, otherwise individual files would be lost.
func Export(w io.Writer, root *scanner.Node, opt ExportOptions) error {
	if opt.Generator == "" {
		opt.Generator = "disklens"
	}
	if opt.Version == "" {
		opt.Version = "0.1"
	}
	meta, err := json.Marshal(struct {
		Progname  string `json:"progname"`
		Progver   string `json:"progver"`
		Timestamp int64  `json:"timestamp"`
	}{
		Progname:  opt.Generator,
		Progver:   opt.Version,
		Timestamp: time.Now().Unix(),
	})
	if err != nil {
		return err
	}
	if _, err := fmt.Fprintf(w, "[1,%d,%s,", minorEmitted, meta); err != nil {
		return err
	}
	if err := exportDir(w, root, true, opt); err != nil {
		return err
	}
	_, err = w.Write([]byte("]"))
	return err
}

func exportDir(w io.Writer, n *scanner.Node, top bool, opt ExportOptions) error {
	if _, err := w.Write([]byte("[")); err != nil {
		return err
	}
	// Directory infoblock (first element, no leading comma): disklens does not
	// stat directories themselves, so asize/dsize are 0 and ncdu computes the
	// total from the children.
	name := n.Name
	if top {
		name = n.Path
	}
	if err := writeInfo(w, infoBlock{Name: name, ReadError: n.ReadError()}); err != nil {
		return err
	}
	for _, f := range n.FileEntries {
		dsize := f.Disk
		if !opt.DiskMeasured {
			dsize = f.Apparent
		}
		if _, err := w.Write([]byte{','}); err != nil {
			return err
		}
		if err := writeInfo(w, infoBlock{Name: f.Name, Asize: f.Apparent, Dsize: dsize}); err != nil {
			return err
		}
	}
	for _, c := range n.Children {
		if _, err := w.Write([]byte{','}); err != nil {
			return err
		}
		if err := exportDir(w, c, false, opt); err != nil {
			return err
		}
	}
	_, err := w.Write([]byte("]"))
	return err
}

// writeInfo writes one infoblock object, prefixed by a comma except for the
// first element right after the opening bracket of its directory.
func writeInfo(w io.Writer, ib infoBlock) error {
	b, err := json.Marshal(ib)
	if err != nil {
		return err
	}
	_, err = w.Write(b)
	return err
}
