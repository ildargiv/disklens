package recycle

import (
	"fmt"
	"path/filepath"
	"strings"
)

// Guard refuses obviously dangerous delete targets: drive roots, Windows
// system directories, .git paths. A nil return means "may be moved to the
// Recycle Bin". Used by both the MCP tool and the TUI.
func Guard(path string) error {
	low := strings.ToLower(filepath.Clean(path))
	// Drive roots: "C:", "C:\\" (one letter + colon, nothing after). Note that
	// Clean turns bare "c:" into "c:." (drive-relative), so strip trailing dots.
	if trimmed := strings.Trim(strings.TrimRight(low, `\`), "."); len(trimmed) == 2 && trimmed[1] == ':' {
		if c := trimmed[0]; c >= 'a' && c <= 'z' {
			return fmt.Errorf("refusing to delete a drive root: %s", path)
		}
	}
	segs := strings.Split(low, "\\")
	for _, s := range segs[1:] {
		switch s {
		case "windows", "system volume information", "$recycle.bin", ".git":
			return fmt.Errorf("refusing to touch a protected path: %s", path)
		}
	}
	return nil
}
