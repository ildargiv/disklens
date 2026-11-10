//go:build !windows

package scanner

import "os"

// isReparsePoint is always false on non-Windows systems: POSIX symlinks are
// handled through os.ModeSymlink instead.
func isReparsePoint(info os.FileInfo) bool { return false }

// diskSize on POSIX should use st_blocks*512; until that is implemented it
// approximates with the apparent size.
func diskSize(path string, apparent int64) int64 { return apparent }
