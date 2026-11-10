//go:build !windows

package recycle

import "errors"

var errUnsupported = errors.New("recycle bin operations are only supported on Windows")

// QueryInfo mirrors the Windows-side type so callers can use it cross-platform.
type QueryInfo struct {
	SizeBytes int64 `json:"size_bytes"`
	Items     int64 `json:"items"`
}

func ToRecycleBin(path string) error { return errUnsupported }

func EmptyRecycleBin() error { return errUnsupported }

func Query() (QueryInfo, error) { return QueryInfo{}, errUnsupported }
