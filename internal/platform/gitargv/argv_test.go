package gitargv

import "testing"

func TestParse(t *testing.T) {
	tests := []struct {
		name    string
		argv    []string
		command string
		ok      bool
	}{
		{"plain", []string{"git", "status"}, "status", true},
		{"globals", []string{"git", "--no-pager", "-C", "/tmp/repo", "-c", "color.ui=false", "status", "--short"}, "status", true},
		{"equals globals", []string{"git", "--git-dir=/tmp/repo.git", "--work-tree=/tmp/repo", "diff"}, "diff", true},
		{"exec path attached", []string{"git", "--exec-path=/opt/git", "help"}, "help", true},
		{"exec path does not steal command", []string{"git", "--exec-path", "status"}, "", false},
		{"malformed value", []string{"git", "-C"}, "", false},
		{"unknown global", []string{"git", "--future-option", "status"}, "", false},
		{"no command", []string{"git", "--version"}, "", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, ok := Parse(tt.argv)
			if ok != tt.ok || got.Command != tt.command {
				t.Fatalf("Parse(%v) = (%+v, %v), want command %q ok %v", tt.argv, got, ok, tt.command, tt.ok)
			}
		})
	}
}

func TestSafeToBuffer(t *testing.T) {
	tests := []struct {
		argv []string
		want bool
	}{
		{[]string{"git", "status"}, true},
		{[]string{"git", "check-ignore", "file"}, true},
		{[]string{"git", "commit", "-m", "message"}, true},
		{[]string{"git", "commit"}, false},
		{[]string{"git", "commit", "--amend"}, false},
		{[]string{"git", "commit", "--amend", "--no-edit"}, true},
		{[]string{"git", "add", "-p"}, false},
		{[]string{"git", "checkout", "-p"}, false},
		{[]string{"git", "config", "--edit"}, false},
		{[]string{"git", "notes", "--ref=review", "edit", "HEAD"}, false},
		{[]string{"git", "notes", "add", "-m", "reviewed", "HEAD"}, true},
		{[]string{"git", "notes", "copy", "--stdin"}, false},
		{[]string{"git", "tag", "-a", "v1"}, false},
		{[]string{"git", "tag", "-a", "v1", "-m", "release"}, true},
		{[]string{"git", "rebase", "-i", "HEAD~2"}, false},
		{[]string{"git", "cat-file", "--batch"}, false},
		{[]string{"git", "daemon"}, false},
		{[]string{"git", "difftool", "--no-prompt"}, true},
	}
	for _, tt := range tests {
		inv, ok := Parse(tt.argv)
		if !ok {
			t.Fatalf("Parse(%v) failed", tt.argv)
		}
		if got := SafeToBuffer(inv); got != tt.want {
			t.Errorf("SafeToBuffer(%v) = %v, want %v", tt.argv, got, tt.want)
		}
	}
}
