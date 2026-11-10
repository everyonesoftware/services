//go:build !windows

package publishers

import (
	"errors"
	"os"
)

// Package-private seam so tests can exercise real directory-sync failures.
var syncDirectory = func(path string) error {
	file, err := os.Open(path)
	if err != nil {
		return err
	}
	return errors.Join(file.Sync(), file.Close())
}
