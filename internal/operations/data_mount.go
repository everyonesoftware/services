package operations

import (
	"errors"
	"path"
	"strings"
)

// StorageMount identifies the same durable directory in Docker's namespace.
// For a volume Source is its actual name; for a bind it is the daemon-reported
// absolute host path, never a Compose-relative path or the container's /data.
type StorageMount struct {
	Type   string
	Source string
}

// ResolveDataMount is shared by dashboard discovery and worker validation so
// both processes require the same writable, persistent /data mount contract.
func ResolveDataMount(inspected Inspect) (StorageMount, error) {
	var storage StorageMount
	found := false
	for _, mount := range inspected.Mounts {
		if strings.HasPrefix(mount.Destination, "/data/") {
			return StorageMount{}, errors.New("mounts beneath /data would give workers a different storage view")
		}
		if mount.Destination != "/data" {
			continue
		}
		if found {
			return StorageMount{}, errors.New("multiple /data mounts are ambiguous")
		}
		found = true
		if !mount.RW {
			return StorageMount{}, errors.New("/data mount must be writable")
		}
		switch mount.Type {
		case "volume":
			storage = StorageMount{Type: "volume", Source: mount.Name}
		case "bind":
			storage = StorageMount{Type: "bind", Source: mount.Source}
		default:
			return StorageMount{}, errors.New("/data must use a named volume or host-folder bind, not temporary storage")
		}
	}
	if !found {
		return StorageMount{}, errors.New("worker requires a writable named volume or host-folder bind mounted at /data")
	}
	if err := storage.validate(); err != nil {
		return StorageMount{}, err
	}
	return storage, nil
}

func (storage StorageMount) validate() error {
	switch storage.Type {
	case "volume":
		if storage.Source != "" {
			return nil
		}
	case "bind":
		// Validate both daemon path forms without rewriting the inspected source.
		source := strings.ReplaceAll(storage.Source, `\`, "/")
		drive := len(source) >= 3 && ((source[0] >= 'A' && source[0] <= 'Z') || (source[0] >= 'a' && source[0] <= 'z')) && source[1] == ':' && source[2] == '/'
		if drive {
			source = source[2:]
		}
		if path.IsAbs(source) && !strings.HasPrefix(source, "//") && path.Clean(source) != "/" {
			return nil
		}
	}
	return errors.New("/data requires a named volume or a non-root absolute host-folder bind source")
}
