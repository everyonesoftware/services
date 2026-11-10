//go:build windows

package icons

import (
	"errors"
	"os"
	"path/filepath"

	"golang.org/x/sys/windows"
)

// Windows directory handles cannot use os.File.Sync. Publish both files and
// newly created directories with MoveFileEx's synchronous write-through move.
// Never fall back to a rename lacking WRITE_THROUGH when this operation fails.
var moveFileEx = windows.MoveFileEx

func durableRename(from, to string) error {
	return moveWriteThrough(from, to, windows.MOVEFILE_REPLACE_EXISTING)
}

func moveWriteThrough(from, to string, flags uint32) error {
	source, err := windows.UTF16PtrFromString(from)
	if err != nil {
		return err
	}
	target, err := windows.UTF16PtrFromString(to)
	if err != nil {
		return err
	}
	return moveFileEx(source, target, flags|windows.MOVEFILE_WRITE_THROUGH)
}

func createDurableDirectory(path string) error {
	tmp, err := os.MkdirTemp(filepath.Dir(path), ".icons-dir-*")
	if err != nil {
		return err
	}
	defer os.Remove(tmp)
	// Directory destinations must not exist; REPLACE_EXISTING is for files.
	return moveWriteThrough(tmp, path, 0)
}

// Existing directory entries have no pending creation by this Store. Every new
// directory is created by the write-through move above, before its children.
func syncDirectoryEntry(string) error { return nil }

func syncExistingAsset(path string) error {
	f, err := os.OpenFile(path, os.O_RDWR, 0)
	if err != nil {
		return err
	}
	return errors.Join(f.Sync(), f.Close())
}
