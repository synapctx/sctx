package rawcache

import (
	"os"
	"path/filepath"
	"runtime"
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
		// Windows has no POSIX mode bits: os.FileMode there is synthesized
		// from the read-only attribute, not real per-owner permissions.
		if runtime.GOOS != "windows" && info.Mode().Perm() != 0o600 {
			t.Fatalf("%s mode = %v", name, info.Mode().Perm())
		}
	}
	info, err := os.Stat(cache.Dir)
	if err != nil {
		t.Fatalf("stat cache: %v", err)
	}
	if runtime.GOOS != "windows" && info.Mode().Perm() != 0o700 {
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

func TestLookupListAndExpiry(t *testing.T) {
	cache := New(filepath.Join(t.TempDir(), "raw"), time.Hour, 1024)
	now := time.Now()
	cache.now = func() time.Time { return now }
	a, err := cache.StoreFor("/usr/local/bin/go", []byte("one"), nil)
	if err != nil {
		t.Fatal(err)
	}
	if !validID(a.ID) {
		t.Fatalf("id %q is not a short hex id", a.ID)
	}
	later := now.Add(time.Minute)
	if err := os.Chtimes(a.Path, later, later); err != nil {
		t.Fatal(err)
	}
	b, err := cache.Store([]byte("two!"), []byte("e"))
	if err != nil {
		t.Fatal(err)
	}

	info, err := cache.Lookup(a.ID)
	if err != nil || info.Program != "go" || info.Bytes != 3 {
		t.Fatalf("lookup = %+v, %v", info, err)
	}
	if info, err = cache.Lookup(b.ID); err != nil || info.Program != "" || info.Bytes != 5 {
		t.Fatalf("lookup b = %+v, %v", info, err)
	}
	for _, bad := range []string{"", "nothex", "../" + a.ID, strings.Repeat("0", 12)} {
		if _, err := cache.Lookup(bad); err != ErrNotFound {
			t.Fatalf("Lookup(%q) = %v, want ErrNotFound", bad, err)
		}
	}
	if list, err := cache.List(); err != nil || len(list) != 2 {
		t.Fatalf("list = %v, %v", list, err)
	}

	cache.now = func() time.Time { return now.Add(2 * time.Hour) }
	if _, err := cache.Lookup(a.ID); err != ErrNotFound {
		t.Fatalf("expired lookup = %v, want ErrNotFound", err)
	}
	if list, _ := cache.List(); len(list) != 0 {
		t.Fatalf("expired entries listed: %v", list)
	}
}
