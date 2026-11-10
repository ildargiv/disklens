//go:build !windows

package recycle

import "errors"

var errUnsupported = errors.New("recycle bin operations are only supported on Windows")

func ToRecycleBin(path string) error { return errUnsupported }

func EmptyRecycleBin() error { return errUnsupported }
