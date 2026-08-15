package rawcache

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestStoreUsesPrivateFilesAndPreservesStreams(t *testing.T) {
	cache := New(filepath.Join(t.TempDir(), "raw"), time.Hour, 1024)
	entry, err := cache.Store([]byte("out\x00\n"), []byte("err\n"))
	if err != nil {
		t.Fatal(err)
	}
	for name, want := range map[string]string{"stdout": "out\x00\n", "stderr": "err\n"} {
		path := filepath.Join(entry.Path, name)
		got, err := os.ReadFile(path)
		if err != nil || string(got) != want {
			t.Fatalf("%s = %q, %v", name, got, err)
		}
		info, err := os.Stat(path)
		if err != nil {
			t.Fatalf("stat %s: %v", name, err)
		}
		if info.Mode().Perm() != 0o600 {
			t.Fatalf("%s mode = %v", name, info.Mode().Perm())
		}
	}
	info, err := os.Stat(cache.Dir)
	if err != nil {
		t.Fatalf("stat cache: %v", err)
	}
	if info.Mode().Perm() != 0o700 {
		t.Fatalf("cache mode = %v", info.Mode().Perm())
	}
}

func TestCleanupExpiresAndBoundsEntries(t *testing.T) {
	root := filepath.Join(t.TempDir(), "raw")
	cache := New(root, time.Hour, 12)
	cache.now = func() time.Time { return time.Unix(10_000, 0) }
	old, err := cache.Store([]byte(strings.Repeat("a", 8)), nil)
	if err != nil {
		t.Fatal(err)
	}
	oldTime := cache.now().Add(-2 * time.Hour)
	if err := os.Chtimes(old.Path, oldTime, oldTime); err != nil {
		t.Fatal(err)
	}
	newer, err := cache.Store([]byte(strings.Repeat("b", 8)), nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(old.Path); !os.IsNotExist(err) {
		t.Fatalf("expired entry still exists: %v", err)
	}
	if _, err := os.Stat(newer.Path); err != nil {
		t.Fatalf("new entry missing: %v", err)
	}
	third, err := cache.Store([]byte(strings.Repeat("c", 8)), nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(newer.Path); !os.IsNotExist(err) {
		t.Fatalf("oldest entry was not evicted: %v", err)
	}
	if _, err := os.Stat(third.Path); err != nil {
		t.Fatalf("latest entry missing: %v", err)
	}
}

func TestOversizedAndSymlinkCachesDecline(t *testing.T) {
	cache := New(filepath.Join(t.TempDir(), "raw"), time.Hour, 4)
	if _, err := cache.Store([]byte("12345"), nil); err == nil {
		t.Fatal("oversized output accepted")
	}

	root := t.TempDir()
	target := filepath.Join(root, "target")
	if err := os.Mkdir(target, 0o700); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(root, "link")
	if err := os.Symlink(target, link); err != nil {
		t.Skipf("symlink unavailable: %v", err)
	}
	if _, err := New(link, time.Hour, 1024).Store([]byte("x"), nil); err == nil {
		t.Fatal("symlink cache root accepted")
	}
}
