//go:build !windows

package scanner

import (
	"os"
	"syscall"
)

// isReparsePoint is always false on non-Windows systems: POSIX symlinks are
// handled through os.ModeSymlink instead.
func isReparsePoint(os.FileInfo) bool    { return false }
func isDirReparsePoint(os.FileInfo) bool { return false }

func listDir(path string, wantDisk bool) ([]dirEnt, error) {
	return listDirGeneric(path, wantDisk)
}

// diskSize returns allocated bytes (st_blocks*512) where available.
func diskSize(_ string, info os.FileInfo) int64 {
	if st, ok := info.Sys().(*syscall.Stat_t); ok {
		return int64(st.Blocks) * 512
	}
	return info.Size()
}

// hardLinkKey returns a dedup key only for files with more than one link,
// so the link set stays tiny on typical trees.
func hardLinkKey(info os.FileInfo) fileKey {
	if st, ok := info.Sys().(*syscall.Stat_t); ok && st.Nlink > 1 {
		return fileKey{dev: uint64(st.Dev), ino: uint64(st.Ino)}
	}
	return fileKey{}
}

// trustFileIDs: dev+ino are always meaningful on POSIX.
func trustFileIDs(string) bool { return true }
