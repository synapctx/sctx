package hook

import "testing"

func TestClassify(t *testing.T) {
	tests := []struct {
		name string
		cmd  string
		want Verdict
		key  string
	}{
		{"covered", "go test ./...", VerdictWrap, "go test"},
		{"insertion point inside a shared prefix", "sed -n '1,5p' file.txt", VerdictWrap, "sed"},
		{"second segment wrapped", "cd sub && go vet ./...", VerdictWrap, "go vet"},
		{"env assignment", "FOO=1 go test ./...", VerdictWrap, "go test"},
		{"already wrapped", "sctx go test ./...", VerdictSkip, ""},
		{"uncovered program never carries arguments", "someunknowntool --host db.internal /etc/passwd", VerdictGap, "someunknowntool"},
		{"builtin", "echo hi", VerdictSkip, ""},
		{"gh level 2", "gh pr list", VerdictWrap, "gh pr list"},
		{"docker compose", "docker compose ps", VerdictWrap, "docker compose ps"},
		{"npx tool", "npx jest", VerdictWrap, "jest"},
		{"python -m", "python -m pytest", VerdictWrap, "pytest"},
		{"uv run", "uv run pytest", VerdictWrap, "pytest"},
		{"newline boundary", "cd app\nnpm run test-e2e", VerdictWrap, "npm run"},
		{"chain does not leak into argv", "git status | head", VerdictWrap, "git status"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			v, key := Classify(tc.cmd)
			if v != tc.want || key != tc.key {
				t.Fatalf("Classify(%q) = %v, %q; want %v, %q", tc.cmd, v, key, tc.want, tc.key)
			}
		})
	}
}
