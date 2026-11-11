//go:build windows

package scanner

import (
	"encoding/binary"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"unicode/utf16"
	"unsafe"
)

const (
	fileAttrDirectory          = 0x10
	fileAttrSparse             = 0x200
	fileAttrReparsePoint       = 0x400
	fileAttrCompressed         = 0x800
	ioReparseTagSymlink        = 0xA000000C
	fileListDirectory          = 0x1
	classFileIDBothDirInfo     = 10 // FileIdBothDirectoryInfo
	classFileIDBothDirRestart  = 11 // FileIdBothDirectoryRestartInfo
	errorNoMoreFiles           = syscall.Errno(18)
	errorInvalidParameter      = syscall.Errno(87)
	errorInvalidFunction       = syscall.Errno(1)
	errorNotSupported          = syscall.Errno(50)
	windowsToUnixEpoch100ns    = 116444736000000000
	dirInfoBufBytes            = 64 << 10
	longPathThreshold          = 240
	fileShareReadWriteDelete   = syscall.FILE_SHARE_READ | syscall.FILE_SHARE_WRITE | syscall.FILE_SHARE_DELETE
	offNextEntry, offLastWrite = 0, 24
	offEndOfFile, offAllocSize = 40, 48
	offAttributes, offNameLen  = 56, 60
	offEaSize, offFileID       = 64, 96
	offFileName                = 104
)

var (
	kernel32                         = syscall.NewLazyDLL("kernel32.dll")
	procGetCompressedFileSizeW       = kernel32.NewProc("GetCompressedFileSizeW")
	procGetFileInformationByHandleEx = kernel32.NewProc("GetFileInformationByHandleEx")
)

// isReparsePoint reports whether info points at a Windows reparse point
// (junction, directory symlink, cloud placeholder, etc.).
func isReparsePoint(info os.FileInfo) bool {
	if data, ok := info.Sys().(*syscall.Win32FileAttributeData); ok && data != nil {
		return data.FileAttributes&syscall.FILE_ATTRIBUTE_REPARSE_POINT != 0
	}
	return false
}

// isDirReparsePoint reports a reparse point that is a directory: a junction,
// mount point or directory symlink. These are never descended into.
// (File reparse points such as OneDrive placeholders are real files.)
func isDirReparsePoint(info os.FileInfo) bool {
	if data, ok := info.Sys().(*syscall.Win32FileAttributeData); ok && data != nil {
		return data.FileAttributes&fileAttrReparsePoint != 0 && data.FileAttributes&fileAttrDirectory != 0
	}
	return false
}

// hardLinkKey: the generic (fallback) listing has no file ids on Windows.
func hardLinkKey(os.FileInfo) fileKey { return fileKey{} }

// diskSize returns the bytes actually allocated on disk for path (cluster-
// rounded, compression-aware), similar to st_blocks*512 on POSIX. Falls back
// to the apparent size on failure.
func diskSize(path string, info os.FileInfo) int64 {
	return compressedSize(path, info.Size())
}

func compressedSize(path string, apparent int64) int64 {
	p, err := syscall.UTF16PtrFromString(longPath(path))
	if err != nil {
		return apparent
	}
	var high uint32
	low, _, e := procGetCompressedFileSizeW.Call(uintptr(unsafe.Pointer(p)), uintptr(unsafe.Pointer(&high)))
	// INVALID_FILE_SIZE is a legitimate low DWORD; only GetLastError tells.
	if uint32(low) == 0xFFFFFFFF && e != syscall.Errno(0) {
		return apparent
	}
	return int64(high)<<32 | int64(uint32(low))
}

// longPath adds the \\?\ prefix to long absolute paths so Win32 calls made
// directly through syscall (which, unlike package os, do not do this) work
// beyond MAX_PATH.
func longPath(p string) string {
	if len(p) < longPathThreshold || strings.HasPrefix(p, `\\?\`) || !filepath.IsAbs(p) {
		return p
	}
	if strings.HasPrefix(p, `\\`) { // UNC \\server\share\...
		return `\\?\UNC\` + p[2:]
	}
	return `\\?\` + p
}

var errFallback = errors.New("fast directory enumeration not supported")

// listDir enumerates a directory with GetFileInformationByHandleEx
// (FileIdBothDirectoryInfo): one syscall returns names, sizes, allocation
// sizes, attributes, reparse tags and file ids for a whole batch of entries,
// instead of an lstat + GetCompressedFileSizeW per file. File ids give hard
// link deduplication for free. Filesystems that do not support this class
// (some network shares, FAT variants) fall back to the generic listing.
func listDir(path string, wantDisk bool) ([]dirEnt, error) {
	out, err := listDirFast(path, wantDisk)
	if errors.Is(err, errFallback) {
		return listDirGeneric(path, wantDisk)
	}
	return out, err
}

func listDirFast(path string, wantDisk bool) ([]dirEnt, error) {
	p, err := syscall.UTF16PtrFromString(longPath(path))
	if err != nil {
		return nil, err
	}
	h, err := syscall.CreateFile(p, fileListDirectory, fileShareReadWriteDelete, nil,
		syscall.OPEN_EXISTING, syscall.FILE_FLAG_BACKUP_SEMANTICS, 0)
	if err != nil {
		return nil, &os.PathError{Op: "open", Path: path, Err: err}
	}
	defer syscall.CloseHandle(h)

	// []uint64 guarantees the 8-byte alignment the kernel structures need.
	backing := make([]uint64, dirInfoBufBytes/8)
	buf := unsafe.Slice((*byte)(unsafe.Pointer(&backing[0])), dirInfoBufBytes)
	var out []dirEnt
	class := uintptr(classFileIDBothDirRestart)
	for first := true; ; first = false {
		r, _, e := procGetFileInformationByHandleEx.Call(uintptr(h), class,
			uintptr(unsafe.Pointer(&buf[0])), uintptr(len(buf)))
		class = classFileIDBothDirInfo
		if r == 0 {
			switch e {
			case errorNoMoreFiles:
				return out, nil
			case errorInvalidParameter, errorInvalidFunction, errorNotSupported:
				if first {
					return nil, errFallback
				}
			}
			return nil, &os.PathError{Op: "readdir", Path: path, Err: e}
		}
		for off := 0; ; {
			rec := buf[off:]
			next := binary.LittleEndian.Uint32(rec[offNextEntry:])
			nameLen := int(binary.LittleEndian.Uint32(rec[offNameLen:]))
			name := utf16Name(rec[offFileName : offFileName+nameLen])
			if name != "." && name != ".." {
				out = append(out, decodeEntry(path, name, rec, wantDisk))
			}
			if next == 0 {
				break
			}
			off += int(next)
		}
	}
}

func utf16Name(b []byte) string {
	u := make([]uint16, len(b)/2)
	for i := range u {
		u[i] = binary.LittleEndian.Uint16(b[2*i:])
	}
	return string(utf16.Decode(u))
}

func decodeEntry(dir, name string, rec []byte, wantDisk bool) dirEnt {
	attrs := binary.LittleEndian.Uint32(rec[offAttributes:])
	d := dirEnt{name: name}
	isDir := attrs&fileAttrDirectory != 0
	if attrs&fileAttrReparsePoint != 0 {
		// For reparse points EaSize carries the reparse tag.
		tag := binary.LittleEndian.Uint32(rec[offEaSize:])
		switch {
		case isDir:
			d.kind = entLink // junction, mount point, directory symlink
			return d
		case tag == ioReparseTagSymlink:
			d.kind = entSymlink
			d.size = int64(binary.LittleEndian.Uint64(rec[offEndOfFile:]))
			return d
		}
		// Other file reparse points (OneDrive/cloud placeholders, dedup,
		// app execution aliases) are real files: fall through.
	}
	if isDir {
		d.kind = entDir
		return d
	}
	d.kind = entFile
	d.size = int64(binary.LittleEndian.Uint64(rec[offEndOfFile:]))
	d.disk = d.size
	if wantDisk {
		d.disk = int64(binary.LittleEndian.Uint64(rec[offAllocSize:]))
		if attrs&(fileAttrCompressed|fileAttrSparse) != 0 {
			d.disk = compressedSize(filepath.Join(dir, name), d.size)
		}
	}
	if ft := int64(binary.LittleEndian.Uint64(rec[offLastWrite:])); ft > windowsToUnixEpoch100ns {
		d.mtime = (ft - windowsToUnixEpoch100ns) / 10000
	}
	if id := binary.LittleEndian.Uint64(rec[offFileID:]); id != 0 {
		d.key = fileKey{ino: id} // ids are per volume; a scan never leaves its volume
	}
	return d
}

var procGetDriveTypeW = kernel32.NewProc("GetDriveTypeW")

// trustFileIDs reports whether file ids under root are reliable enough for
// hard link deduplication: local fixed/removable volumes yes, network
// shares no (SMB servers may return zero or non-unique ids).
func trustFileIDs(root string) bool {
	if strings.HasPrefix(root, `\\`) {
		return false
	}
	vol := filepath.VolumeName(root)
	if vol == "" {
		return false
	}
	p, err := syscall.UTF16PtrFromString(vol + `\`)
	if err != nil {
		return false
	}
	t, _, _ := procGetDriveTypeW.Call(uintptr(unsafe.Pointer(p)))
	const driveRemovable, driveFixed = 2, 3
	return t == driveRemovable || t == driveFixed
}
