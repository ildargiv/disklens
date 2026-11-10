//go:build windows

package scanner

import (
	"os"
	"syscall"
)

// isReparsePoint reports whether info points at a Windows reparse point
// (junction, directory symlink, etc.).
func isReparsePoint(info os.FileInfo) bool {
	if data, ok := info.Sys().(*syscall.Win32FileAttributeData); ok && data != nil {
		return data.FileAttributes&syscall.FILE_ATTRIBUTE_REPARSE_POINT != 0
	}
	return false
}
