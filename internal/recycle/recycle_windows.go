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
		FFlags: fofAllowUndo | fofSilent | fofNoConfirm | fofNoConfirmMkDir | fofNoErrorUI,
	}
	// SHFileOperationW only reports failures via the struct: its return value
	// is meaningless.
	procSHFileOperationW.Call(uintptr(unsafe.Pointer(&ops)))
	if ops.AnyOperationsAborted != 0 {
		return fmt.Errorf("operation aborted by the shell (item may be in use or protected)")
	}
	return nil
}

// EmptyRecycleBin permanently destroys everything currently in the Recycle Bin.
func EmptyRecycleBin() error {
	r, _, e := procSHEmptyRecycleBinW.Call(
		0, 0, uintptr(sherbNoConfirmation|sherbNoProgressUI|sherbNoSound))
	if e != nil {
		return fmt.Errorf("SHEmptyRecycleBinW failed: %w", e)
	}
	// S_OK == 0; S_FALSE means the bin was already empty, which is fine.
	if r != 0 && r != 1 {
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

// Query returns the size and item count of the Recycle Bin for the default
// root (pszRootPath = NULL: the drive OpenCode/disklens runs on).
func Query() (QueryInfo, error) {
	info := shqueryrbinfo{CbSize: uint32(unsafe.Sizeof(shqueryrbinfo{}))}
	r, _, e := procSHQueryRecycleBinW.Call(0, uintptr(unsafe.Pointer(&info)))
	switch r {
	case 0: // S_OK
		return QueryInfo{SizeBytes: info.SizeBytes, Items: info.NumItems}, nil
	case 1, 0x80070018: // S_FALSE / ERROR_NO_MORE_FILES: the bin is simply empty
		return QueryInfo{}, nil
	}
	if e != nil {
		return QueryInfo{}, fmt.Errorf("SHQueryRecycleBinW failed: %w", e)
	}
	return QueryInfo{}, fmt.Errorf("SHQueryRecycleBinW failed: 0x%x", r)
}
