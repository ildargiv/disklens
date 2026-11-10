//go:build windows

package scanner

import (
	"os"
	"syscall"
	"unsafe"
)

// isReparsePoint reports whether info points at a Windows reparse point
// (junction, directory symlink, etc.).
func isReparsePoint(info os.FileInfo) bool {
	if data, ok := info.Sys().(*syscall.Win32FileAttributeData); ok && data != nil {
		return data.FileAttributes&syscall.FILE_ATTRIBUTE_REPARSE_POINT != 0
	}
	return false
}

var (
	kernel32                    = syscall.NewLazyDLL("kernel32.dll")
	procGetCompressedFileSizeW = kernel32.NewProc("GetCompressedFileSizeW")
)

// diskSize returns the bytes actually allocated on disk for path (cluster-
// rounded, compression-aware), similar to st_blocks*512 on POSIX. Falls back
// to apparent on any failure; the INVALID_FILE_SIZE edge case (a real file of
// exactly 4 GiB - 1) may also fall back, which is an accepted approximation.
func diskSize(path string, apparent int64) int64 {
	p, err := syscall.UTF16PtrFromString(path)
	if err != nil {
		return apparent
	}
	r, _, e := procGetCompressedFileSizeW.Call(uintptr(unsafe.Pointer(p)))
	if e != nil || r == 0xFFFFFFFF {
		return apparent
	}
	return int64(r)
}
