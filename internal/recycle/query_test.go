package recycle

import (
	"runtime"
	"testing"
)

// Smoke test: querying the Recycle Bin must not error and must not change it.
func TestQuery(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("Recycle Bin query is Windows-specific")
	}
	q, err := Query()
	if err != nil {
		t.Fatalf("Query: %v", err)
	}
	if q.SizeBytes < 0 || q.Items < 0 {
		t.Errorf("nonsense bin stats: %+v", q)
	}
}
