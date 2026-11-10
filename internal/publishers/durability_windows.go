//go:build windows

package publishers

// Windows does not expose POSIX directory fsync through os.File.Sync; match
// the repository's icons store. File contents are synced before SQLite opens.
var syncDirectory = func(string) error { return nil }
