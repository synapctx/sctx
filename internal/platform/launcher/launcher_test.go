package launcher

import (
	"slices"
	"testing"
)

func TestInner(t *testing.T) {
	tests := []struct {
		name string
		argv []string
		want []string
	}{
		{"npx", []string{"npx", "tsc", "--noEmit"}, []string{"tsc", "--noEmit"}},
		{"npx yes flag", []string{"npx", "-y", "eslint", "."}, []string{"eslint", "."}},
		{"npx version", []string{"npx", "tsc@5.4", "-p", "."}, []string{"tsc", "-p", "."}},
		{"npx unknown flag", []string{"npx", "-p", "typescript", "tsc"}, nil},
		{"npx scoped", []string{"npx", "@scope/tool"}, []string{"@scope/tool"}},
		{"bunx", []string{"bunx", "tsc"}, []string{"tsc"}},
		{"pnpm dlx", []string{"pnpm", "dlx", "tsc"}, []string{"tsc"}},
		{"pnpm exec", []string{"pnpm", "exec", "eslint", "."}, []string{"eslint", "."}},
		{"pnpm other", []string{"pnpm", "install"}, nil},
		{"npm exec dashdash", []string{"npm", "exec", "--", "tsc"}, []string{"tsc"}},
		{"npm exec flag", []string{"npm", "exec", "-c", "tsc"}, nil},
		{"uv run", []string{"uv", "run", "pytest", "-x"}, []string{"pytest", "-x"}},
		{"uv run flag", []string{"uv", "run", "--with", "x", "pytest"}, nil},
		{"uv sync", []string{"uv", "sync"}, nil},
		{"poetry run", []string{"poetry", "run", "mypy", "."}, []string{"mypy", "."}},
		{"bundle exec stays with bundler", []string{"bundle", "exec", "rspec", "spec/a_spec.rb"}, nil},
		{"bundle exec flag", []string{"bundle", "exec", "--keep-file-descriptors", "rspec"}, nil},
		{"bundle install", []string{"bundle", "install"}, nil},
		{"python -m pytest", []string{"python", "-m", "pytest", "-q"}, []string{"pytest", "-q"}},
		{"python3 -m ruff", []string{"python3", "-m", "ruff", "check"}, []string{"ruff", "check"}},
		{"python3.12 -u -m pip", []string{"python3.12", "-u", "-m", "pip", "list"}, []string{"pip", "list"}},
		{"python -m unknown", []string{"python", "-m", "http.server"}, nil},
		{"python script", []string{"python", "script.py"}, nil},
		{"python -c", []string{"python", "-c", "print(1)"}, nil},
		{"path launcher", []string{"/usr/bin/npx", "tsc"}, []string{"tsc"}},
		{"bare launcher", []string{"npx"}, nil},
		{"quoted tool", []string{"npx", `"tsc"`}, nil},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := Inner(tc.argv)
			if tc.want == nil {
				if !slices.Equal(got, tc.argv) {
					t.Fatalf("Inner(%q) = %q, want unchanged", tc.argv, got)
				}
				return
			}
			if !slices.Equal(got, tc.want) {
				t.Fatalf("Inner(%q) = %q, want %q", tc.argv, got, tc.want)
			}
		})
	}
}

func TestAttributed(t *testing.T) {
	tests := []struct {
		name string
		argv []string
		want []string
	}{
		{"known npx", []string{"npx", "tsc", "-p", "."}, []string{"tsc", "-p", "."}},
		{"known python module", []string{"python3", "-m", "pytest"}, []string{"pytest"}},
		{"private npx", []string{"npx", "private-pkg", "build"}, nil},
		{"scoped", []string{"npm", "exec", "@acme/internal-cli"}, nil},
		{"pnpm exec private", []string{"pnpm", "exec", "acme-deploy"}, nil},
		{"uv script", []string{"uv", "run", "scripts/x.py"}, nil},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := Attributed(tc.argv)
			want := tc.want
			if want == nil {
				want = tc.argv
			}
			if !slices.Equal(got, want) {
				t.Fatalf("Attributed(%q) = %q, want %q", tc.argv, got, want)
			}
		})
	}
}

func TestPeelBundleExec(t *testing.T) {
	idx, name, ok := Peel([]string{"bundle", "exec", "rspec@3", "spec"})
	if !ok || idx != 2 || name != "rspec" {
		t.Fatalf("Peel = %d, %q, %v; want 2, rspec, true", idx, name, ok)
	}
	if _, _, ok := Peel([]string{"bundle", "exec", "--keep-file-descriptors", "rspec"}); ok {
		t.Fatal("a flag before the inner program must not be peeled")
	}
	if _, _, ok := Peel([]string{"bundle", "install"}); ok {
		t.Fatal("bundle install is not a launcher form")
	}
}
