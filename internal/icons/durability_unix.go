//go:build !windows

package icons

import (
	"errors"
	"os"
	"path/filepath"
)

// Package-private syscall seam; tests replace it only in serial subtests.
var syncDirectory = func(path string) error {
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	return errors.Join(f.Sync(), f.Close())
}

func syncDirectoryEntry(path string) error { return syncDirectory(filepath.Dir(path)) }

func createDurableDirectory(path string) error {
	if err := os.Mkdir(path, 0700); err != nil && !errors.Is(err, os.ErrExist) {
		return err
	}
	return syncDirectoryEntry(path)
}

func durableRename(from, to string) error {
	if err := os.Rename(from, to); err != nil {
		return err
	}
	return syncDirectoryEntry(to)
}

func syncExistingAsset(path string) error { return syncDirectoryEntry(path) }
