//go:build !windows

package tui

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
)

// reveal opens the containing folder in the desktop file manager.
func reveal(path string) error {
	dir := path
	if info, err := os.Stat(path); err != nil || !info.IsDir() {
		dir = filepath.Dir(path)
	}
	opener := "xdg-open"
	if runtime.GOOS == "darwin" {
		opener = "open"
	}
	return exec.Command(opener, dir).Start()
}
