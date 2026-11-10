package ncdujson

import (
	"encoding/json"
	"fmt"
	"io"
	"path/filepath"

	"disklens/internal/scanner"
)

// Import reads an ncdu JSON export (or a disklens export) and rebuilds the
// directory tree. Directory infoblocks' own asize/dsize are ignored (ncdu
// counts directory self-sizes; disklens scans do not, and totals are
// computed from file entries).
func Import(r io.Reader) (*scanner.Node, error) {
	dec := json.NewDecoder(r)
	dec.UseNumber()

	t, err := dec.Token()
	if err != nil {
		return nil, fmt.Errorf("not an ncdu JSON export: %w", err)
	}
	if d, ok := t.(json.Delim); !ok || d != '[' {
		return nil, fmt.Errorf("not an ncdu JSON export: top-level value is not an array")
	}

	major, err := readVersion(dec)
	if err != nil {
		return nil, err
	}
	if major != 1 {
		return nil, fmt.Errorf("unsupported ncdu format major version %d", major)
	}
	if _, err := readVersion(dec); err != nil { // minor version
		return nil, err
	}
	if err := skipValue(dec); err != nil { // metadata
		return nil, err
	}

	root, err := decodeDir(dec, "")
	if err != nil {
		return nil, err
	}

	t, err = dec.Token() // top-level closing ']'
	if err != nil {
		return nil, err
	}
	if d, ok := t.(json.Delim); !ok || d != ']' {
		return nil, fmt.Errorf("malformed ncdu export: missing top-level closing bracket")
	}
	return root, nil
}

func readVersion(dec *json.Decoder) (int64, error) {
	t, err := dec.Token()
	if err != nil {
		return 0, err
	}
	n, ok := t.(json.Number)
	if !ok {
		return 0, fmt.Errorf("malformed ncdu export: version is not a number")
	}
	v, err := n.Int64()
	if err != nil {
		return 0, fmt.Errorf("malformed ncdu export: bad version %q", n)
	}
	return v, nil
}

// skipValue consumes one arbitrary JSON value (used for metadata).
func skipValue(dec *json.Decoder) error {
	t, err := dec.Token()
	if err != nil {
		return err
	}
	if d, ok := t.(json.Delim); ok && (d == '{' || d == '[') {
		for dec.More() {
			if err := skipValue(dec); err != nil {
				return err
			}
		}
		if t, err := dec.Token(); err != nil {
			return err
		} else if d, ok := t.(json.Delim); !ok || d != '}' && d != ']' {
			return fmt.Errorf("malformed ncdu export")
		}
	}
	return nil
}

// decodeDir reads one <directory> array; dec is positioned at its '['.
func decodeDir(dec *json.Decoder, parent string) (*scanner.Node, error) {
	t, err := dec.Token()
	if err != nil {
		return nil, err
	}
	if d, ok := t.(json.Delim); !ok || d != '[' {
		return nil, fmt.Errorf("malformed ncdu export: expected a directory array")
	}
	return parseDirBody(dec, parent)
}

// parseDirBody reads a directory while dec is positioned inside its array.
func parseDirBody(dec *json.Decoder, parent string) (*scanner.Node, error) {
	t, err := dec.Token()
	if err != nil {
		return nil, err
	}
	if d, ok := t.(json.Delim); !ok || d != '{' {
		return nil, fmt.Errorf("malformed ncdu export: directory does not start with an infoblock")
	}
	ib, err := readInfoblockBody(dec)
	if err != nil {
		return nil, err
	}
	if ib.Name == "" {
		return nil, fmt.Errorf("malformed ncdu export: infoblock without name")
	}

	n := &scanner.Node{Name: filepath.Base(ib.Name), Path: ib.Name}
	if parent != "" {
		n.Name = ib.Name
		n.Path = filepath.Join(parent, ib.Name)
	}
	if ib.ReadError {
		n.HasError = true
		n.Errors++
	}

	for dec.More() {
		t, err := dec.Token()
		if err != nil {
			return nil, err
		}
		d, ok := t.(json.Delim)
		if !ok {
			return nil, fmt.Errorf("malformed ncdu export: unexpected scalar in directory %q", n.Path)
		}
		switch d {
		case '{': // file infoblock
			fb, err := readInfoblockBody(dec)
			if err != nil {
				return nil, err
			}
			n.FileEntries = append(n.FileEntries, scanner.FileEntry{
				Name: fb.Name, Apparent: fb.Asize, Disk: estimateDisk(fb),
			})
			n.Size += fb.Asize
			n.Disk += estimateDisk(fb)
			n.Files++
		case '[': // subdirectory
			c, err := parseDirBody(dec, n.Path)
			if err != nil {
				return nil, err
			}
			n.Children = append(n.Children, c)
			n.Dirs += c.Dirs + 1
			n.Size += c.Size
			n.Disk += c.Disk
			n.Files += c.Files
			if c.HasError {
				n.HasError = true
			}
			n.Errors += c.Errors
		default:
			return nil, fmt.Errorf("malformed ncdu export: unexpected %q in directory", d)
		}
	}
	// closing ']' of this directory
	t, err = dec.Token()
	if err != nil {
		return nil, err
	}
	if d, ok := t.(json.Delim); !ok || d != ']' {
		return nil, fmt.Errorf("malformed ncdu export: directory %q is not closed", n.Path)
	}
	return n, nil
}

// readInfoblockBody reads an infoblock while dec is positioned inside it
// (after the opening '{'). ncdu infoblocks only contain scalar values.
func readInfoblockBody(dec *json.Decoder) (infoBlock, error) {
	ib := infoBlock{}
	for dec.More() {
		kTok, err := dec.Token()
		if err != nil {
			return ib, err
		}
		key, ok := kTok.(string)
		if !ok {
			return ib, fmt.Errorf("malformed ncdu export: non-string object key")
		}
		vTok, err := dec.Token()
		if err != nil {
			return ib, err
		}
		switch key {
		case "name":
			s, _ := vTok.(string)
			ib.Name = s
		case "asize":
			ib.Asize = number(vTok)
		case "dsize":
			ib.Dsize = number(vTok)
		case "read_error":
			ib.ReadError, _ = vTok.(bool)
		case "notreg":
			ib.NotReg, _ = vTok.(bool)
		}
		// unknown keys (dev/ino/mtime/...) are intentionally skipped
	}
	t, err := dec.Token()
	if err != nil {
		return ib, err
	}
	if d, ok := t.(json.Delim); !ok || d != '}' {
		return ib, fmt.Errorf("malformed ncdu export: infoblock is not closed")
	}
	return ib, nil
}

func number(t any) int64 {
	if n, ok := t.(json.Number); ok {
		v, _ := n.Int64()
		return v
	}
	return 0
}

// estimateDisk: ncdu allows omitting dsize (0 assumed). For viewing dumps
// where dsize is missing but asize is present, assume apparent == disk.
func estimateDisk(fb infoBlock) int64 {
	if fb.Dsize == 0 && fb.Asize > 0 {
		return fb.Asize
	}
	return fb.Dsize
}
