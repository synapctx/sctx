// Package rawcache stores short-lived, owner-only copies of command output
// when a formatter explicitly reports that it omitted content.
package rawcache

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"time"
)

const idBytes = 6

// Cache is a local-only recovery store. It never participates in telemetry.
type Cache struct {
	Dir      string
	TTL      time.Duration
	MaxBytes int64
	now      func() time.Time
}

// Entry identifies one recovery directory containing stdout and, when
// present, stderr as separate byte-exact files.
type Entry struct {
	ID   string
	Path string
}

// New creates a cache policy. Store declines invalid or disabled policies.
func New(dir string, ttl time.Duration, maxBytes int64) *Cache {
	return &Cache{Dir: dir, TTL: ttl, MaxBytes: maxBytes, now: time.Now}
}

// Store writes a recoverable output pair and applies TTL/size cleanup. Cache
// failure is returned to the caller so it can silently omit the recovery hint;
// it must never affect the wrapped command.
func (c *Cache) Store(stdout, stderr []byte) (Entry, error) {
	if c == nil || c.Dir == "" || c.TTL <= 0 || c.MaxBytes <= 0 {
		return Entry{}, errors.New("raw cache disabled")
	}
	size := int64(len(stdout) + len(stderr))
	if size == 0 || size > c.MaxBytes {
		return Entry{}, errors.New("raw output does not fit cache")
	}
	if err := c.prepareRoot(); err != nil {
		return Entry{}, err
	}
	if err := c.cleanup(size); err != nil {
		return Entry{}, err
	}

	id, err := randomID()
	if err != nil {
		return Entry{}, err
	}
	path := filepath.Join(c.Dir, id)
	if err := os.Mkdir(path, 0o700); err != nil {
		return Entry{}, fmt.Errorf("creating raw cache entry: %w", err)
	}
	remove := true
	defer func() {
		if remove {
			_ = os.RemoveAll(path)
		}
	}()
	if err := os.WriteFile(filepath.Join(path, "stdout"), stdout, 0o600); err != nil {
		return Entry{}, fmt.Errorf("writing cached stdout: %w", err)
	}
	if len(stderr) > 0 {
		if err := os.WriteFile(filepath.Join(path, "stderr"), stderr, 0o600); err != nil {
			return Entry{}, fmt.Errorf("writing cached stderr: %w", err)
		}
	}
	if err := c.cleanup(0); err != nil {
		return Entry{}, err
	}
	remove = false
	return Entry{ID: id, Path: path}, nil
}

func (c *Cache) prepareRoot() error {
	if info, err := os.Lstat(c.Dir); err == nil {
		if info.Mode()&os.ModeSymlink != 0 || !info.IsDir() {
			return errors.New("raw cache path is not a private directory")
		}
	} else if !os.IsNotExist(err) {
		return fmt.Errorf("inspecting raw cache: %w", err)
	}
	if err := os.MkdirAll(c.Dir, 0o700); err != nil {
		return fmt.Errorf("creating raw cache: %w", err)
	}
	if err := os.Chmod(c.Dir, 0o700); err != nil {
		return fmt.Errorf("securing raw cache: %w", err)
	}
	return nil
}

type cachedEntry struct {
	path    string
	modTime time.Time
	size    int64
}

// cleanup removes expired entries, then oldest entries until current bytes
// plus reserve fit MaxBytes. Unknown files are ignored rather than deleted.
func (c *Cache) cleanup(reserve int64) error {
	dirs, err := os.ReadDir(c.Dir)
	if err != nil {
		return fmt.Errorf("reading raw cache: %w", err)
	}
	now := c.now()
	entries := make([]cachedEntry, 0, len(dirs))
	var total int64
	for _, dir := range dirs {
		if !dir.IsDir() || !validID(dir.Name()) {
			continue
		}
		info, err := dir.Info()
		if err != nil {
			continue
		}
		path := filepath.Join(c.Dir, dir.Name())
		if now.Sub(info.ModTime()) >= c.TTL {
			_ = os.RemoveAll(path)
			continue
		}
		size := entrySize(path)
		entries = append(entries, cachedEntry{path: path, modTime: info.ModTime(), size: size})
		total += size
	}
	sort.Slice(entries, func(i, j int) bool { return entries[i].modTime.Before(entries[j].modTime) })
	for _, entry := range entries {
		if total+reserve <= c.MaxBytes {
			break
		}
		if err := os.RemoveAll(entry.path); err == nil {
			total -= entry.size
		}
	}
	if total+reserve > c.MaxBytes {
		return errors.New("raw cache size limit cannot be satisfied")
	}
	return nil
}

func entrySize(path string) int64 {
	var total int64
	for _, name := range []string{"stdout", "stderr"} {
		if info, err := os.Stat(filepath.Join(path, name)); err == nil && info.Mode().IsRegular() {
			total += info.Size()
		}
	}
	return total
}

func randomID() (string, error) {
	var value [idBytes]byte
	if _, err := rand.Read(value[:]); err != nil {
		return "", fmt.Errorf("generating raw cache id: %w", err)
	}
	return hex.EncodeToString(value[:]), nil
}

func validID(value string) bool {
	if len(value) != idBytes*2 {
		return false
	}
	_, err := hex.DecodeString(value)
	return err == nil
}
