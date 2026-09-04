package gitrepo

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func writeChildRepo(t *testing.T, root, name, url string, indexAge time.Duration) {
	t.Helper()
	dir := filepath.Join(root, name)
	writeFile(t, filepath.Join(dir, ".git", "config"), "[remote \"origin\"]\n\turl = "+url+"\n")
	idx := filepath.Join(dir, ".git", "index")
	if err := os.WriteFile(idx, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	mtime := time.Now().Add(-indexAge)
	if err := os.Chtimes(idx, mtime, mtime); err != nil {
		t.Fatal(err)
	}
}

func TestChildRepos(t *testing.T) {
	root := t.TempDir()
	writeChildRepo(t, root, "old-repo", "https://github.com/acme/old.git", 2*time.Hour)
	writeChildRepo(t, root, "new-repo", "https://github.com/acme/new.git", time.Minute)
	// A plain directory with no .git must be skipped, not error out.
	if err := os.MkdirAll(filepath.Join(root, "not-a-repo"), 0o755); err != nil {
		t.Fatal(err)
	}
	// A file (not a directory) must be skipped.
	writeFile(t, filepath.Join(root, "a-file"), "hi")

	got := ChildRepos(root, 200, time.Second)
	if len(got) != 2 {
		t.Fatalf("ChildRepos() returned %d entries, want 2: %+v", len(got), got)
	}
	if got[0].Name != "acme/new" {
		t.Errorf("busiest repo first: got %q, want %q", got[0].Name, "acme/new")
	}
	if got[1].Name != "acme/old" {
		t.Errorf("got[1] = %q, want %q", got[1].Name, "acme/old")
	}
}

func TestChildReposEntryCap(t *testing.T) {
	root := t.TempDir()
	for i := 0; i < 5; i++ {
		writeChildRepo(t, root, "repo"+string(rune('a'+i)), "https://github.com/acme/repo.git", time.Duration(i)*time.Minute)
	}
	got := ChildRepos(root, 2, time.Second)
	if len(got) > 2 {
		t.Fatalf("ChildRepos() with maxEntries=2 returned %d entries", len(got))
	}
}

func TestChildReposNonexistentDir(t *testing.T) {
	if got := ChildRepos(filepath.Join(t.TempDir(), "nope"), 200, time.Second); got != nil {
		t.Fatalf("ChildRepos() on a nonexistent dir = %v, want nil", got)
	}
}

func TestChildReposNoOrigin(t *testing.T) {
	root := t.TempDir()
	writeFile(t, filepath.Join(root, "solo", ".git", "config"), "[core]\n\trepositoryformatversion = 0\n")
	got := ChildRepos(root, 200, time.Second)
	if len(got) != 0 {
		t.Fatalf("ChildRepos() with no origin remote = %+v, want empty", got)
	}
}
