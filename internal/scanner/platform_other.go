//go:build !windows

package scanner

import "os"

// isReparsePoint is always false on non-Windows systems: POSIX symlinks are
// handled through os.ModeSymlink instead.
func isReparsePoint(info os.FileInfo) bool { return false }
