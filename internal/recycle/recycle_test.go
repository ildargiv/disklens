package recycle

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

// Sends a throwaway file to the real Recycle Bin and verifies it is gone.
// EmptyRecycleBin has no test on purpose: it is destructive by definition.
func TestToRecycleBin(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("recycle bin is a Windows concept")
	}
	root := t.TempDir()
	f := filepath.Join(root, "disklens-recycle-test.bin")
	if err := os.WriteFile(f, []byte("throwaway"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := ToRecycleBin(f); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(f); !os.IsNotExist(err) {
		t.Errorf("file still present after ToRecycleBin: %v", err)
	}
}

func TestToRecycleBinMissing(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("recycle bin is a Windows concept")
	}
	if err := ToRecycleBin(`C:\definitely\not\here.bin`); err == nil {
		t.Error("missing path should report an error")
	}
}
