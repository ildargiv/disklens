package recycle

import (
	"runtime"
	"testing"
)

func TestGuard(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("drive-root detection is Windows-specific")
	}
	refuse := []string{`C:\`, `c:`, `C:\Windows`, `C:\Windows\System32\drivers`, `D:\Tool\.git`, `C:\System Volume Information\Foo`}
	allow := []string{`C:\Users\Pod\Downloads`, `C:\Users\Pod\.gitignore`, `C:\WindowsApps\thing`}
	for _, p := range refuse {
		if err := Guard(p); err == nil {
			t.Errorf("Guard(%q) should refuse", p)
		}
	}
	for _, p := range allow {
		if err := Guard(p); err != nil {
			t.Errorf("Guard(%q) should allow: %v", p, err)
		}
	}
}
