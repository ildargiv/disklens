//go:build windows

package recycle

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"unsafe"
)

const (
	foDelete = 3

	fofSilent         = 0x0004
	fofNoConfirm      = 0x0010
	fofAllowUndo      = 0x0040 // -> Recycle Bin
	fofNoConfirmMkDir = 0x0100
	fofNoErrorUI      = 0x0400
	// fofWantNukeWarning makes the shell ask before *permanently* deleting an
	// item that cannot go to the Recycle Bin (too big for the bin quota,
	// network share, removable drive without a bin). Without it, ALLOWUNDO
	// silently degrades to a hard delete in exactly those cases.
	fofWantNukeWarning = 0x4000

	sherbNoConfirmation = 0x1
	sherbNoProgressUI   = 0x2
	sherbNoSound        = 0x4
)

// Mirrors SHFILEOPSTRUCTW (shell32). Field order and types match the C layout
// on amd64; Go's alignment produces the same offsets as the C struct.
type shellFileOpStructW struct {
	Hwnd                 uintptr
	WFunc                uint32
	PFrom                *uint16
	PTo                  *uint16
	FFlags               uint16
	AnyOperationsAborted int32
	NameMappings         unsafe.Pointer
	LpszProgressTitle    *uint16
}

var (
	shell32                = syscall.NewLazyDLL("shell32.dll")
	procSHFileOperationW   = shell32.NewProc("SHFileOperationW")
	procSHEmptyRecycleBinW = shell32.NewProc("SHEmptyRecycleBinW")
)

// ToRecycleBin moves path (file or directory) to the Windows Recycle Bin.
// The item stays recoverable. pFrom requires a double-NUL-terminated list.
func ToRecycleBin(path string) error {
	// Defense in depth: never hand a drive root to the shell.
	if v := filepath.VolumeName(path); v != "" && strings.Trim(path, `\`) == strings.Trim(v, `\`) {
		return fmt.Errorf("refusing to recycle a drive root: %s", path)
	}
	if _, err := os.Stat(path); err != nil {
		return err
	}
	name, err := syscall.UTF16FromString(path) // includes one trailing NUL
	if err != nil {
		return err
	}
	name = append(name, 0) // double NUL termination

	ops := shellFileOpStructW{
		WFunc:  foDelete,
		PFrom:  &name[0],
		FFlags: fofAllowUndo | fofWantNukeWarning | fofSilent | fofNoConfirm | fofNoConfirmMkDir | fofNoErrorUI,
	}
	r, _, _ := procSHFileOperationW.Call(uintptr(unsafe.Pointer(&ops)))
	if ops.AnyOperationsAborted != 0 {
		return fmt.Errorf("operation aborted (item in use, protected, or permanent deletion was declined)")
	}
	if r != 0 {
		return fmt.Errorf("SHFileOperationW failed with code 0x%x (item may be in use or protected)", r)
	}
	if _, err := os.Lstat(path); err == nil {
		return fmt.Errorf("item is still present after the move to the Recycle Bin: %s", path)
	}
	return nil
}

// EmptyRecycleBin permanently destroys everything currently in the Recycle Bin.
func EmptyRecycleBin() error {
	// The return value is an HRESULT; the error from Call is GetLastError,
	// which is always non-nil and must not be used to detect failure.
	r, _, _ := procSHEmptyRecycleBinW.Call(
		0, 0, uintptr(sherbNoConfirmation|sherbNoProgressUI|sherbNoSound))
	// S_OK == 0; S_FALSE / ERROR_NO_MORE_FILES mean the bin was already empty.
	if r != 0 && r != 1 && uint32(r) != 0x80070012 {
		return fmt.Errorf("SHEmptyRecycleBinW failed: 0x%x", r)
	}
	return nil
}

// QueryInfo reports the current contents of the Recycle Bin.
type QueryInfo struct {
	SizeBytes int64 `json:"size_bytes"`
	Items     int64 `json:"items"`
}

type shqueryrbinfo struct {
	CbSize    uint32
	SizeBytes int64
	NumItems  int64
}

var procSHQueryRecycleBinW = shell32.NewProc("SHQueryRecycleBinW")

// Query returns the total size and item count of the Recycle Bins on all
// drives (pszRootPath = NULL).
func Query() (QueryInfo, error) {
	info := shqueryrbinfo{CbSize: uint32(unsafe.Sizeof(shqueryrbinfo{}))}
	r, _, _ := procSHQueryRecycleBinW.Call(0, uintptr(unsafe.Pointer(&info)))
	switch uint32(r) {
	case 0: // S_OK
		return QueryInfo{SizeBytes: info.SizeBytes, Items: info.NumItems}, nil
	case 1, 0x80070012, 0x80070018: // S_FALSE / no more files: the bin is simply empty
		return QueryInfo{}, nil
	}
	return QueryInfo{}, fmt.Errorf("SHQueryRecycleBinW failed: 0x%x", r)
}
