package icons

import (
	"errors"
	"os"
	"path/filepath"
)

// Create each missing ancestor durably before publishing children within it.
// Revisit existing ancestors too: an earlier initialization may have failed
// between creating a directory and persisting its parent entry.
func makeDurableDirectories(path string) error {
	parent := filepath.Dir(path)
	if parent == path {
		return nil
	}
	if err := makeDurableDirectories(parent); err != nil {
		return err
	}
	info, err := os.Stat(path)
	if err == nil {
		if !info.IsDir() {
			return &os.PathError{Op: "mkdir", Path: path, Err: errors.New("not a directory")}
		}
		return syncDirectoryEntry(path)
	}
	if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	return createDurableDirectory(path)
}
