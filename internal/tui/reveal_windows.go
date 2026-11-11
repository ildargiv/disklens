//go:build windows

package tui

import (
	"os/exec"
	"syscall"
)

// reveal opens Explorer with path selected. explorer.exe does its own
// command-line parsing, so the raw command line is passed verbatim.
func reveal(path string) error {
	cmd := exec.Command("explorer.exe")
	cmd.SysProcAttr = &syscall.SysProcAttr{CmdLine: `explorer.exe /select,"` + path + `"`}
	// explorer.exe returns exit code 1 even on success: only a failure to
	// start is an error.
	return cmd.Start()
}
