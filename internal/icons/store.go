// Package icons persists explicit per-project and per-container image overrides.
// Source downloads are limited to 2 MiB. Decoded images are limited to 4,000,000
// pixels and 4096 pixels per side, and are always re-encoded as PNG. No automatic
// refresh or container mutation occurs. Configure one Store per data directory.
// Source URLs are limited to 4096 bytes, with at most five redirects and a
// 15-second total HTTP timeout (five seconds for DNS plus connection attempts).
package icons

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"image"
	_ "image/gif"
	_ "image/jpeg"
	"image/png"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"
)

const MaxDownloadBytes = 2 << 20
const MaxPixels = 4_000_000
const MaxDimension = 4096

// ErrInvalidInput marks invalid targets, source URLs or encoded images.
var ErrInvalidInput = errors.New("invalid icon input")

// ErrDownload marks an upstream fetch failure; persistence has not changed.
var ErrDownload = errors.New("icon download failed")

// ErrUnavailable marks a store that must be reopened before further mutations.
var ErrUnavailable = errors.New("icon storage unavailable; reopen required")

// Icon.URL is always a local immutable asset URL; SourceURL is provenance only.
// An empty SourceURL identifies an uploaded file, not a remote download.
type Icon struct {
	Kind      string `json:"kind"`
	Key       string `json:"key"`
	URL       string `json:"url"`
	SourceURL string `json:"sourceUrl"`
}

// Downloader supplies untrusted encoded image bytes. Injection is intended for
// trusted application integrations/tests; Open always uses the secure downloader.
// Store independently applies byte, format, dimension and PNG normalization limits.
type Downloader interface {
	Download(context.Context, string) ([]byte, error)
}
type Store struct {
	mu         sync.RWMutex
	directory  string
	downloader Downloader
	records    map[string]Icon
	failed     error // guarded by mu; metadata publication may have reached disk
	// publish is a per-store seam for testing durable publication failures.
	publish func(string, string) error
}
type metadata struct {
	Version int    `json:"version"`
	Icons   []Icon `json:"icons"`
}

var assetName = regexp.MustCompile(`^[a-f0-9]{64}\.png$`)

func Open(directory string) (*Store, error) { return OpenWithDownloader(directory, newDownloader()) }

func OpenWithDownloader(directory string, downloader Downloader) (*Store, error) {
	if strings.TrimSpace(directory) == "" || downloader == nil {
		return nil, errors.New("icon directory and downloader required")
	}
	directory, err := filepath.Abs(directory)
	if err != nil {
		return nil, err
	}
	if err = makeDurableDirectories(filepath.Join(directory, "images")); err != nil {
		return nil, err
	}
	for _, path := range []string{directory, filepath.Join(directory, "images")} {
		if err = probeWritable(path); err != nil {
			return nil, err
		}
	}
	s := &Store{directory: directory, downloader: downloader, records: make(map[string]Icon), publish: durableRename}
	b, err := os.ReadFile(filepath.Join(directory, "metadata.json"))
	if errors.Is(err, os.ErrNotExist) {
		return s, nil
	}
	if err != nil {
		return nil, err
	}
	var m metadata
	if err = json.Unmarshal(b, &m); err != nil {
		return nil, err
	}
	if m.Version != 1 {
		return nil, errors.New("unsupported icon metadata version")
	}
	for _, icon := range m.Icons {
		if err = validateTarget(icon.Kind, icon.Key); err != nil {
			return nil, err
		}
		if icon.SourceURL != "" {
			if err = validateURL(icon.SourceURL); err != nil {
				return nil, err
			}
		}
		name := strings.TrimPrefix(icon.URL, "/api/icons/images/")
		if !assetName.MatchString(name) || icon.URL != "/api/icons/images/"+name {
			return nil, errors.New("invalid icon asset")
		}
		id := recordKey(icon.Kind, icon.Key)
		if _, exists := s.records[id]; exists {
			return nil, errors.New("duplicate icon target")
		}
		s.records[id] = icon
	}
	return s, nil
}

// Probe the actual filesystem rather than permission bits (which do not model
// ACLs, read-only mounts, or the current process's privileges).
func probeWritable(directory string) error {
	f, err := os.CreateTemp(directory, ".icons-probe-*")
	if err != nil {
		return err
	}
	defer os.Remove(f.Name())
	_, writeErr := f.Write([]byte("icon store write probe"))
	syncErr := f.Sync()
	closeErr := f.Close()
	removeErr := os.Remove(f.Name())
	return errors.Join(writeErr, syncErr, closeErr, removeErr)
}

func recordKey(kind, key string) string { return kind + "\x00" + key }
func validateTarget(kind, key string) error {
	if (kind != "project" && kind != "container") || strings.TrimSpace(key) == "" || len(key) > 512 || strings.ContainsAny(key, "\x00\r\n") {
		return errors.New("invalid icon target")
	}
	return nil
}
func sortedRecords(records map[string]Icon) []Icon {
	result := make([]Icon, 0, len(records))
	for _, icon := range records {
		result = append(result, icon)
	}
	sort.Slice(result, func(i, j int) bool {
		if result[i].Kind != result[j].Kind {
			return result[i].Kind < result[j].Kind
		}
		return result[i].Key < result[j].Key
	})
	return result
}
func (s *Store) List() []Icon { s.mu.RLock(); defer s.mu.RUnlock(); return sortedRecords(s.records) }

// Available reports whether mutations are safe. Last-known-good reads remain
// usable after a metadata error, but only reopening can recover writable state.
func (s *Store) Available() bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.failed == nil
}
func (s *Store) copyRecords() map[string]Icon {
	next := make(map[string]Icon, len(s.records))
	for k, v := range s.records {
		next[k] = v
	}
	return next
}
func (s *Store) Put(ctx context.Context, kind, key, sourceURL string) (Icon, error) {
	if !s.Available() {
		return Icon{}, ErrUnavailable
	}
	if err := validateTarget(kind, key); err != nil {
		return Icon{}, fmt.Errorf("%w: %v", ErrInvalidInput, err)
	}
	if err := validateURL(sourceURL); err != nil {
		return Icon{}, fmt.Errorf("%w: %v", ErrInvalidInput, err)
	}
	data, err := s.downloader.Download(ctx, sourceURL)
	if err != nil {
		return Icon{}, fmt.Errorf("%w: %v", ErrDownload, err)
	}
	return s.save(ctx, kind, key, sourceURL, data)
}

// Upload validates image bytes directly, without contacting an external host or
// trusting a client filename or MIME type. Persistence is shared with URL imports.
func (s *Store) Upload(ctx context.Context, kind, key string, data []byte) (Icon, error) {
	return s.save(ctx, kind, key, "", data)
}

func (s *Store) save(ctx context.Context, kind, key, sourceURL string, data []byte) (Icon, error) {
	if !s.Available() {
		return Icon{}, ErrUnavailable
	}
	if err := validateTarget(kind, key); err != nil {
		return Icon{}, fmt.Errorf("%w: %v", ErrInvalidInput, err)
	}
	normalized, err := normalize(data)
	if err != nil {
		return Icon{}, fmt.Errorf("%w: %v", ErrInvalidInput, err)
	}
	if err = ctx.Err(); err != nil {
		return Icon{}, err
	}
	hash := sha256.Sum256(normalized)
	name := hex.EncodeToString(hash[:]) + ".png"
	icon := Icon{Kind: kind, Key: key, URL: "/api/icons/images/" + name, SourceURL: sourceURL}
	s.mu.Lock()
	defer s.mu.Unlock()
	// Downloads happen outside the lock; another mutation may have failed meanwhile.
	if s.failed != nil {
		return Icon{}, s.failed
	}
	assetPath := filepath.Join(s.directory, "images", name)
	// Content-addressed files are immutable and shared by independent targets.
	// Do not replace an existing asset: on Windows it may be open for serving.
	info, statErr := os.Lstat(assetPath)
	switch {
	case statErr == nil:
		if !info.Mode().IsRegular() {
			return Icon{}, errors.New("icon asset is not a regular file")
		}
		// A previous attempt may have renamed the image but failed its
		// durability barrier. Reusing it must not bypass that barrier.
		if err = syncExistingAsset(assetPath); err != nil {
			return Icon{}, err
		}
	case errors.Is(statErr, os.ErrNotExist):
		if err = s.atomicWrite(filepath.Dir(assetPath), name, normalized); err != nil {
			return Icon{}, err
		}
	default:
		return Icon{}, statErr
	}
	next := s.copyRecords()
	next[recordKey(kind, key)] = icon
	if err = s.persist(next); err != nil {
		return Icon{}, err
	}
	s.records = next
	return icon, nil
}
func (s *Store) Delete(kind, key string) error {
	if !s.Available() {
		return ErrUnavailable
	}
	if err := validateTarget(kind, key); err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.failed != nil {
		return s.failed
	}
	next := s.copyRecords()
	delete(next, recordKey(kind, key))
	if err := s.persist(next); err != nil {
		return err
	}
	s.records = next
	return nil
}
func (s *Store) persist(records map[string]Icon) error {
	data, err := json.Marshal(metadata{Version: 1, Icons: sortedRecords(records)})
	if err != nil {
		return err
	}
	if err = s.atomicWrite(s.directory, "metadata.json", data); err != nil {
		// Rename may already have succeeded before its durability barrier failed.
		// Never roll back or write again from potentially stale in-memory records.
		s.failed = errors.Join(ErrUnavailable, err)
		return s.failed
	}
	return nil
}
func (s *Store) atomicWrite(directory, name string, data []byte) error {
	f, err := os.CreateTemp(directory, ".icons-*")
	if err != nil {
		return err
	}
	defer os.Remove(f.Name())
	if _, err = f.Write(data); err != nil {
		f.Close()
		return err
	}
	if err = f.Sync(); err != nil {
		f.Close()
		return err
	}
	if err = f.Close(); err != nil {
		return err
	}
	return s.publish(f.Name(), filepath.Join(directory, name))
}

// Image only accepts canonical SHA-256 PNG basenames, never a caller path.
func (s *Store) Image(name string) (*os.File, error) {
	if !assetName.MatchString(name) {
		return nil, os.ErrNotExist
	}
	root, err := os.OpenRoot(filepath.Join(s.directory, "images"))
	if err != nil {
		return nil, err
	}
	defer root.Close()
	return root.Open(name)
}
func normalize(data []byte) ([]byte, error) {
	if len(data) > MaxDownloadBytes {
		return nil, errors.New("image exceeds 2 MiB")
	}
	config, format, err := image.DecodeConfig(bytes.NewReader(data))
	if err != nil {
		return nil, errors.New("invalid image")
	}
	if format != "png" && format != "jpeg" && format != "gif" {
		return nil, errors.New("unsupported image format")
	}
	if config.Width < 1 || config.Height < 1 || config.Width > MaxDimension || config.Height > MaxDimension || int64(config.Width)*int64(config.Height) > MaxPixels {
		return nil, fmt.Errorf("image exceeds %d pixels or %d per side", MaxPixels, MaxDimension)
	}
	im, _, err := image.Decode(bytes.NewReader(data))
	if err != nil {
		return nil, err
	}
	var out bytes.Buffer
	if err = png.Encode(&out, im); err != nil {
		return nil, err
	}
	return out.Bytes(), nil
}
