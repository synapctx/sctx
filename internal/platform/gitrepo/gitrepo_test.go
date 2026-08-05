package gitrepo

import (
	"os"
	"path/filepath"
	"testing"
)

func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestDetect(t *testing.T) {
	tests := []struct {
		name  string
		setup func(t *testing.T, root string) string // returns dir to Detect
		want  string
	}{
		{
			name: "https url",
			setup: func(t *testing.T, root string) string {
				writeFile(t, filepath.Join(root, ".git", "config"), `
[core]
	repositoryformatversion = 0
[remote "origin"]
	url = https://github.com/synapctx/sctx.git
	fetch = +refs/heads/*:refs/remotes/origin/*
`)
				return root
			},
			want: "synapctx/sctx",
		},
		{
			name: "ssh scp-like url",
			setup: func(t *testing.T, root string) string {
				writeFile(t, filepath.Join(root, ".git", "config"), `
[remote "origin"]
	url = git@github.com:synapctx/sctx.git
`)
				return root
			},
			want: "synapctx/sctx",
		},
		{
			name: "ssh:// url",
			setup: func(t *testing.T, root string) string {
				writeFile(t, filepath.Join(root, ".git", "config"), `
[remote "origin"]
	url = ssh://git@github.com/synapctx/sctx.git
`)
				return root
			},
			want: "synapctx/sctx",
		},
		{
			name: "no origin",
			setup: func(t *testing.T, root string) string {
				writeFile(t, filepath.Join(root, ".git", "config"), `
[remote "upstream"]
	url = https://github.com/synapctx/sctx.git
`)
				return root
			},
			want: "",
		},
		{
			name: "no .git",
			setup: func(t *testing.T, root string) string {
				return root
			},
			want: "",
		},
		{
			name: "git-file worktree indirection",
			setup: func(t *testing.T, root string) string {
				actualGitDir := filepath.Join(root, "main-repo", ".git-actual")
				writeFile(t, filepath.Join(actualGitDir, "config"), `
[remote "origin"]
	url = https://github.com/synapctx/sctx.git
`)
				worktree := filepath.Join(root, "worktree")
				writeFile(t, filepath.Join(worktree, ".git"), "gitdir: "+actualGitDir+"\n")
				return worktree
			},
			want: "synapctx/sctx",
		},
		{
			name: "nested subdirectory walks up",
			setup: func(t *testing.T, root string) string {
				writeFile(t, filepath.Join(root, ".git", "config"), `
[remote "origin"]
	url = https://github.com/synapctx/sctx.git
`)
				sub := filepath.Join(root, "a", "b", "c")
				if err := os.MkdirAll(sub, 0o755); err != nil {
					t.Fatal(err)
				}
				return sub
			},
			want: "synapctx/sctx",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			root := t.TempDir()
			dir := tt.setup(t, root)
			if got := Detect(dir); got != tt.want {
				t.Errorf("Detect() = %q, want %q", got, tt.want)
			}
		})
	}
}
