package main

import (
	"bytes"
	"context"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/synapctx/sctx/internal/adapters/format/generic"
	"github.com/synapctx/sctx/internal/adapters/format/jsoncompact"
	"github.com/synapctx/sctx/internal/application/run"
	domexec "github.com/synapctx/sctx/internal/domain/exec"
	"github.com/synapctx/sctx/internal/domain/stats"
	"github.com/synapctx/sctx/internal/platform/launcher"
)

// coverageFixture replays a real captured run of one tool through the REAL
// pipeline (buildRegistry -> run.Service -> renderChain) with a fake runner that
// emits the fixture bytes and exit code. It catches what a per-package test
// cannot: a formatter that is registered but SHADOWED by another for the same
// program, one that is not registered at all, a launcher peel that names the
// wrong tool, or an argv the registry resolves differently from the formatter's
// own Descriptor.
type coverageFixture struct {
	argv string
	// dir is the format package whose testdata holds the fixture.
	dir string
	// out/err are testdata file names; empty means no bytes on that stream.
	out, err string
	// want is the Descriptor().Command of the formatter that must be selected.
	want string
	// exit is the exit code used when the fixture has no .exit file beside it.
	exit int
}

var coverageFixtures = []coverageFixture{
	// go extras and rust
	{argv: "gotestsum --format testname ./...", dir: "gotestsum", out: "testname.stdout.txt", err: "testname.stderr.txt", want: "gotestsum"},
	{argv: "govulncheck ./...", dir: "govulncheck", out: "vuln.stdout.txt", err: "vuln.stderr.txt", want: "govulncheck"},
	{argv: "staticcheck ./...", dir: "staticcheck", out: "sc.stdout.txt", err: "sc.stderr.txt", want: "staticcheck"},
	{argv: "buf lint", dir: "buf", out: "lint-bad.stdout.txt", err: "lint-bad.stderr.txt", want: "buf"},
	{argv: "cargo test", dir: "cargo", out: "test-fail.stdout.txt", err: "test-fail.stderr.txt", want: "cargo"},
	{argv: "cargo nextest run", dir: "cargo", out: "nx-fail.stdout.txt", err: "nx-fail.stderr.txt", want: "cargo"},
	// iac and cloud
	{argv: "ansible-playbook site.yml", dir: "ansible", out: "bad.txt", want: "ansible-playbook"},
	{argv: "aws ec2 describe-regions --output table", dir: "awscli", out: "regions-table.txt", want: "aws"},
	{argv: "gcloud compute instances list", dir: "gcloud", out: "instances.out", err: "instances.err", want: "gcloud"},
	{argv: "pulumi up", dir: "pulumi", out: "up-change.txt", want: "pulumi"},
	{argv: "helm template x ./chart", dir: "helm", out: "template.txt", want: "helm"},
	// javascript / typescript
	{argv: "biome check .", dir: "biome", out: "biome-check-fail.txt", want: "biome", exit: 1},
	{argv: "oxlint .", dir: "biome", out: "oxlint-default.txt", want: "oxlint", exit: 1},
	{argv: "bun test", dir: "bun", err: "test-fail.txt", want: "bun", exit: 1},
	{argv: "deno test", dir: "deno", out: "test-fail.txt", want: "deno", exit: 1},
	{argv: "deno lint", dir: "deno", out: "lint-fail.txt", want: "deno", exit: 1},
	{argv: "turbo run build", dir: "monorepo", out: "turbo-build.txt", want: "turbo"},
	{argv: "nx build app", dir: "monorepo", out: "nx-build.txt", want: "nx"},
	{argv: "next build", dir: "nextbuild", out: "build-typeerr.txt", want: "next", exit: 1},
	{argv: "playwright test", dir: "playwright", out: "pw-fail.txt", want: "playwright", exit: 1},
	{argv: "npx playwright test", dir: "playwright", out: "pw-fail.txt", want: "playwright", exit: 1},
	{argv: "prettier --check .", dir: "prettier", out: "check-fail.txt", want: "prettier", exit: 1},
	{argv: "prisma generate", dir: "prisma", out: "generate.txt", want: "prisma"},
	// linters and containers
	{argv: "shellcheck x.sh", dir: "shellcheck", out: "default.stdout", want: "shellcheck"},
	{argv: "hadolint Dockerfile", dir: "hadolint", out: "default.stdout", want: "hadolint", exit: 1},
	{argv: "yamllint .", dir: "yamllint", out: "default.stdout", want: "yamllint", exit: 1},
	{argv: "markdownlint .", dir: "markdownlint", err: "default.stderr", want: "markdownlint", exit: 1},
	{argv: "actionlint", dir: "actionlint", out: "default.stdout", want: "actionlint"},
	{argv: "pre-commit run --all-files", dir: "precommit", out: "fail.stdout", err: "fail.stderr", want: "pre-commit", exit: 1},
	{argv: "podman images", dir: "docker", out: "podman-images.stdout", want: "podman"},
	{argv: "docker buildx build .", dir: "docker", err: "build-failure-buildx.stderr", want: "docker", exit: 1},
	// native toolchains and task runners
	{argv: "clang -c x.c", dir: "cc", err: "clang-err.txt", want: "clang"},
	{argv: "clang++ -c x.cpp", dir: "cc", err: "clangpp-err.txt", want: "clang++"},
	{argv: "cmake --build build", dir: "cmake", out: "cmake-build-fail.txt", want: "cmake"},
	{argv: "ctest", dir: "ctest", out: "ctest-fail.txt", want: "ctest"},
	{argv: "ninja", dir: "ninja", out: "build-fail.txt", want: "ninja"},
	{argv: "ping -c 10 example.com", dir: "ping", out: "many.txt", want: "ping"},
	{argv: "swift build", dir: "swiftpm", out: "build-err.txt", want: "swift"},
	{argv: "swift test", dir: "swiftpm", out: "test-fail.txt", want: "swift"},
	{argv: "ast-grep scan", dir: "astgrep", out: "scan-mixed.txt", want: "ast-grep"},
	{argv: "sg scan", dir: "astgrep", out: "scan-mixed.txt", want: "sg"},
	{argv: "just test", dir: "taskrunner", out: "just-test.stdout", err: "just-test.stderr", want: "just", exit: 1},
	{argv: "task test", dir: "taskrunner", out: "task-test.stdout", err: "task-test.stderr", want: "task", exit: 1},
	{argv: "mise run test", dir: "taskrunner", out: "mise-test.stdout", err: "mise-test.stderr", want: "mise", exit: 1},
	// python
	{argv: "python -m unittest", dir: "pytest", out: "unittest.txt", want: "unittest"},
	{argv: "pyright", dir: "pyright", out: "pyright.txt", want: "pyright"},
	{argv: "basedpyright", dir: "pyright", out: "basedpyright.txt", want: "basedpyright"},
	{argv: "ty check", dir: "pyright", out: "ty.txt", want: "ty"},
	{argv: "black --check .", dir: "pyfmt", err: "black_check.txt", want: "black", exit: 1},
	{argv: "isort --check-only .", dir: "pyfmt", err: "isort_check.txt", want: "isort", exit: 1},
	{argv: "pylint pkg", dir: "pylint", out: "pylint.txt", want: "pylint"},
	{argv: "flake8", dir: "pylint", out: "flake8.txt", want: "flake8"},
	{argv: "sqlfluff lint q.sql", dir: "sqlfluff", out: "sqlfluff_bad.txt", want: "sqlfluff"},
	{argv: "poetry install", dir: "pyinstall", out: "poetry_install.txt", want: "poetry"},
	{argv: "pdm install", dir: "pyinstall", out: "pdm_install.txt", want: "pdm"},
	{argv: "uv sync", dir: "pyinstall", out: "uv_sync.txt", want: "uv"},
	{argv: "pip-compile requirements.in", dir: "pyinstall", out: "pipcompile.txt", want: "pip-compile"},
	// ruby and php
	{argv: "rspec", dir: "rspec", out: "fail.txt", want: "rspec"},
	{argv: "bundle exec rspec", dir: "rspec", out: "fail.txt", want: "bundle"},
	{argv: "rubocop", dir: "rubocop", out: "offenses.txt", want: "rubocop"},
	{argv: "rake test", dir: "minitest", out: "rake-fail.txt", want: "rake"},
	{argv: "ruby -Itest test/x_test.rb", dir: "minitest", out: "ruby-fail.txt", want: "ruby"},
	{argv: "bundle install", dir: "bundler", out: "fresh.txt", want: "bundle"},
	{argv: "composer install", dir: "composer", out: "install-fresh.txt", want: "composer"},
	{argv: "phpunit", dir: "phpunit", out: "fail.txt", want: "phpunit"},
	{argv: "phpstan analyse", dir: "phpstan", out: "multi.txt", want: "phpstan"},
	// jvm and .net
	{argv: "mvn test", dir: "maven", out: "mvn-test-fail.txt", want: "mvn"},
	{argv: "gradle test", dir: "gradle", out: "gr-test-fail-full.txt", want: "gradle"},
	{argv: "./gradlew build", dir: "gradle", out: "gr-wrapper-cold.txt", want: "gradlew"},
	{argv: "dotnet test", dir: "dotnet", out: "dn-test-fail.txt", want: "dotnet"},
	{argv: "dotnet build", dir: "dotnet", out: "dn-build-compile-fail.txt", want: "dotnet"},
	// wave 3: scanners
	{argv: "trivy image alpine:3.10", dir: "trivy", out: "image.stdout", err: "image.stderr", want: "trivy"},
	{argv: "trivy fs .", dir: "trivy", out: "fs.stdout", err: "fs.stderr", want: "trivy"},
	{argv: "gitleaks detect --no-banner -v", dir: "gitleaks", out: "detect.stdout", err: "detect.stderr", want: "gitleaks"},
	{argv: "semgrep scan --config x py", dir: "semgrep", out: "multi.stdout", err: "multi.stderr", want: "semgrep"},
	{argv: "checkov -d x", dir: "checkov", out: "k8s.stdout", want: "checkov"},
	{argv: "tflint", dir: "tflint", out: "default.stdout", want: "tflint"},
	{argv: "kube-linter lint x", dir: "kubelinter", out: "default.stdout", err: "default.stderr", want: "kube-linter"},
	{argv: "grype alpine:3.10", dir: "grype", out: "alpine.stdout", want: "grype"},
	{argv: "osv-scanner scan x", dir: "osvscanner", out: "scan.stdout", want: "osv-scanner"},
	{argv: "tfsec tf", dir: "tfsec", out: "default.stdout", err: "default.stderr", want: "tfsec"},
	{argv: "ansible-lint ans/play.yml", dir: "ansiblelint", out: "default.stdout", err: "default.stderr", want: "ansible-lint"},
	{argv: "helmfile template", dir: "helmfile", out: "template.stdout", err: "template.stderr", want: "helmfile"},
	// wave 3: transfer, archive, certificate and version-control tools
	{argv: "wget https://example.com/big.bin", dir: "wget", out: "big.txt", want: "wget"},
	{argv: "openssl x509 -in c.pem -text -noout", dir: "openssl", out: "x509.txt", want: "openssl"},
	{argv: "openssl s_client -connect h:443", dir: "openssl", out: "sclient13.txt", want: "openssl"},
	{argv: "svn checkout https://x/repo", dir: "svn", out: "checkout.txt", want: "svn"},
	{argv: "svn commit -m msg", dir: "svn", out: "commit.txt", want: "svn"},
	{argv: "tar tvf a.tar", dir: "archive", out: "tar-tv-gnu.txt", want: "tar"},
	{argv: "bsdtar -tvf a.tar", dir: "archive", out: "tar-tv-bsd.txt", want: "bsdtar"},
	{argv: "gtar cvf a.tar d", dir: "archive", out: "tar-c-gnu.txt", want: "gtar"},
	{argv: "unzip -l a.zip", dir: "archive", out: "unzip-l.txt", want: "unzip"},
	{argv: "zip -r a.zip d", dir: "archive", out: "zip-r.txt", want: "zip"},
	// wave 3: language toolchains
	{argv: "mix test", dir: "elixir", out: "ex-test-fail.txt", want: "mix"},
	{argv: "sbt test", dir: "scala", out: "sc-test-fail.txt", want: "sbt"},
	{argv: "dart test", dir: "dart", out: "dt-test-fail.txt", want: "dart"},
	{argv: "prove -r t", dir: "perl", out: "pl-prove-fail.txt", want: "prove"},
	{argv: "zig build", dir: "zig", out: "zg-build-err.txt", want: "zig"},
	{argv: "rebar3 eunit", dir: "erlang", out: "er-eunit-fail.txt", want: "rebar3"},
	{argv: "cabal build", dir: "haskell", out: "hs-build-err.txt", want: "cabal"},
	{argv: "ghc Main.hs", dir: "haskell", out: "hs-ghc-err.txt", want: "ghc"},
	{argv: "bazel build //...", dir: "bazel", out: "bz-build-err.txt", want: "bazel"},
	{argv: "bazelisk test //...", dir: "bazel", out: "bz-test-mixed.txt", want: "bazelisk"},
	{argv: "nix build .#x", dir: "nix", out: "nx-build-fail.txt", want: "nix"},
	{argv: "nix-build", dir: "nix", out: "nx-build-ok.txt", want: "nix-build"},
	// wave 3: database clients and migration tools
	{argv: "mysql -e x", dir: "mysqlcli", out: "mysql84_table.stdout", err: "mysql84_table.stderr", want: "mysql"},
	{argv: "sqlite3 -box db.sqlite x", dir: "sqlite3", out: "sqlite354_box.stdout", err: "sqlite354_box.stderr", want: "sqlite3"},
	{argv: "redis-cli lrange k 0 -1", dir: "rediscli", out: "redis7_lrange.stdout", err: "redis7_lrange.stderr", want: "redis-cli"},
	{argv: "redis-cli info", dir: "rediscli", out: "redis7_info.stdout", err: "redis7_info.stderr", want: "redis-cli"},
	{argv: "clickhouse-client -q x", dir: "clickhouse", out: "ch253_p230.stdout", want: "clickhouse-client"},
	{argv: "flyway migrate", dir: "dbmigrate", out: "flyway11_migrate.out", err: "flyway11_migrate.err", want: "flyway"},
	{argv: "liquibase status", dir: "dbmigrate", out: "liquibase431_status.out", err: "liquibase431_status.err", want: "liquibase"},
	{argv: "dbt run", dir: "dbt", out: "dbt112_run.stdout", want: "dbt"},
	// wave 3: Linux system administration
	{argv: "apt-get install -y x", dir: "pkgmgr", out: "apt-get-install.txt", want: "apt-get"},
	{argv: "apt install -y x", dir: "pkgmgr", out: "apt-install.txt", want: "apt"},
	{argv: "dnf install -y x", dir: "pkgmgr", out: "dnf-install.txt", want: "dnf"},
	{argv: "yum install -y x", dir: "pkgmgr", out: "yum-install.txt", want: "yum"},
	{argv: "apk add x", dir: "pkgmgr", out: "apk-add.txt", want: "apk"},
	{argv: "pacman -S --noconfirm x", dir: "pkgmgr", out: "pacman-S.txt", want: "pacman"},
	{argv: "systemctl list-units", dir: "systemd", out: "systemctl-list-units.txt", want: "systemctl"},
	{argv: "systemctl --failed", dir: "systemd", out: "systemctl-failed.txt", want: "systemctl"},
	{argv: "journalctl -u app --no-pager", dir: "systemd", out: "journalctl-app.txt", want: "journalctl"},
	{argv: "ip addr", dir: "netinfo", out: "ip-addr.txt", want: "ip"},
	{argv: "ss -tulpn", dir: "netinfo", out: "ss-tulpn.txt", want: "ss"},
	{argv: "netstat -tulpn", dir: "netinfo", out: "netstat-tulpn.txt", want: "netstat"},
	{argv: "traceroute example.com", dir: "netinfo", out: "traceroute-30.txt", want: "traceroute"},
	{argv: "top -bn1", dir: "sysinfo", out: "top.txt", want: "top"},
	{argv: "dmesg", dir: "sysinfo", out: "dmesg.txt", want: "dmesg"},
	{argv: "mount", dir: "sysinfo", out: "mount.txt", want: "mount"},
	{argv: "lsblk", dir: "sysinfo", out: "lsblk-a.txt", want: "lsblk"},
	{argv: "iptables -L -n", dir: "fw", out: "iptables-L-n.txt", want: "iptables"},
	{argv: "ufw status", dir: "fw", out: "ufw-status.txt", want: "ufw"},
	{argv: "ip6tables -L -n", dir: "fw", out: "iptables-L-n.txt", want: "ip6tables"},
	{argv: "nix-shell -p hello --run hello", dir: "nix", out: "nx-shell.txt", want: "nix-shell"},
}

// coverageSelectionOnly are argvs with no captured fixture whose formatter must
// still be the one selected (registration/shadowing check only).
var coverageSelectionOnly = map[string]string{
	"env":                  "env",
	"printenv HOME":        "printenv",
	"lsof -i :80":          "lsof",
	"wc -l go.mod":         "wc",
	"mvnw test":            "mvnw",
	"./mvnw verify":        "mvnw",
	"mvnd test":            "mvnd",
	"gcc -c x.c":           "gcc",
	"g++ -c x.cpp":         "g++",
	"cc -c x.c":            "cc",
	"c++ -c x.cpp":         "c++",
	"rails test":           "rails",
	"python -m black .":    "black",
	"uv run pyright":       "pyright",
	"npx prettier -c .":    "prettier",
	"npx biome check":      "biome",
	"npx turbo run build":  "turbo",
	"podman ps":            "podman",
	"docker builder build": "docker",
	// wave 3 programs whose fixtures are exercised under another driver name
	"tar xzf a.tgz":       "tar",
	"unzip -o a.zip":      "unzip",
	"sbt compile":         "sbt",
	"trivy k8s cluster":   "trivy",
	"helmfile diff":       "helmfile",
	"apt update":          "apt",
	"apt-get update":      "apt-get",
	"journalctl -b":       "journalctl",
	"systemctl status x":  "systemctl",
	"ss -s":               "ss",
	"mongosh --eval x":    "mongosh",
	"ssh h ls":            "ssh",
	"docker run --rm x y": "docker",
}

type fixtureSpill struct{ b []byte }

func (m *fixtureSpill) Bytes() (io.Reader, error) { return bytes.NewReader(m.b), nil }
func (m *fixtureSpill) Len() int64                { return int64(len(m.b)) }
func (m *fixtureSpill) Spilled() bool             { return false }
func (m *fixtureSpill) Close() error              { return nil }

type fixtureRunner struct {
	out, err []byte
	exit     int
}

func (r fixtureRunner) Run(context.Context, domexec.Command) (domexec.Outcome, error) {
	return domexec.Outcome{ExitCode: r.exit, Stdout: &fixtureSpill{r.out}, Stderr: &fixtureSpill{r.err}}, nil
}

type fixtureStatsStore struct {
	stats.Store
	runs []stats.Run
}

func (c *fixtureStatsStore) Record(_ context.Context, r stats.Run) error {
	c.runs = append(c.runs, r)
	return nil
}

func readFixture(t *testing.T, dir, name string) []byte {
	t.Helper()
	if name == "" {
		return nil
	}
	b, err := os.ReadFile(filepath.Join("..", "..", "internal", "adapters", "format", dir, "testdata", name))
	if err != nil {
		t.Fatalf("reading fixture: %v", err)
	}
	return b
}

// fixtureExit finds the captured exit code beside a fixture: `<stem>.exit` or
// `<stem>.exit.txt`, where stem drops the stream suffix.
func fixtureExit(t *testing.T, dir, name string, fallback int) int {
	t.Helper()
	stem := name
	for _, suf := range []string{".stdout.txt", ".stderr.txt", ".stdout", ".stderr", ".out", ".err", ".txt"} {
		if s, ok := strings.CutSuffix(stem, suf); ok {
			stem = s
			break
		}
	}
	for _, cand := range []string{stem + ".exit", stem + ".exit.txt"} {
		b, err := os.ReadFile(filepath.Join("..", "..", "internal", "adapters", "format", dir, "testdata", cand))
		if err != nil {
			continue
		}
		n, err := strconv.Atoi(strings.TrimSpace(string(b)))
		if err != nil {
			t.Fatalf("%s: %v", cand, err)
		}
		return n
	}
	return fallback
}

func execFixtureArgv(t *testing.T, argv string, r fixtureRunner) (stats.Run, int, int) {
	t.Helper()
	store := &fixtureStatsStore{}
	var stdout, stderr bytes.Buffer
	svc := run.NewService(buildRegistry(), r, store, nil, generic.New(), &stdout, &stderr,
		run.Options{LosslessFallback: jsoncompact.New()})
	code, err := svc.Execute(context.Background(), strings.Fields(argv))
	if err != nil {
		t.Fatalf("Execute(%q): %v", argv, err)
	}
	if code != r.exit {
		t.Errorf("exit code = %d, want %d: the wrapped command's exit code is sacred", code, r.exit)
	}
	if len(store.runs) != 1 {
		t.Fatalf("recorded %d runs, want 1", len(store.runs))
	}
	return store.runs[0], stdout.Len() + stderr.Len(), len(r.out) + len(r.err)
}

func TestNewFormattersAreSelectedAndShrink(t *testing.T) {
	for _, fx := range coverageFixtures {
		t.Run(fx.argv, func(t *testing.T) {
			stem := fx.out
			if stem == "" {
				stem = fx.err
			}
			r := fixtureRunner{out: readFixture(t, fx.dir, fx.out), err: readFixture(t, fx.dir, fx.err), exit: fixtureExit(t, fx.dir, stem, fx.exit)}
			got, outBytes, rawBytes := execFixtureArgv(t, fx.argv, r)
			t.Logf("%-38s -> %-16s %-10s %6d -> %6d bytes (exit %d)", fx.argv, got.Formatter, got.Tier, rawBytes, outBytes, r.exit)
			if got.Formatter != fx.want {
				t.Fatalf("selected formatter %q, want %q (shadowed or unregistered)", got.Formatter, fx.want)
			}
			if got.Tier == "verbatim" {
				t.Errorf("rendered verbatim: the formatter declined its own real fixture")
			}
			if outBytes >= rawBytes {
				t.Errorf("render is %d bytes, raw %d: no reduction", outBytes, rawBytes)
			}
		})
	}
}

// TestNewFormattersResolve covers argvs with no captured fixture: the registry
// must select the intended formatter (an empty run still reports who claimed it).
func TestNewFormattersResolve(t *testing.T) {
	registry := buildRegistry()
	for argv, want := range coverageSelectionOnly {
		t.Run(argv, func(t *testing.T) {
			f, ok := registry.ResolveByArgv(launcher.Inner(strings.Fields(argv)))
			if !ok {
				t.Fatalf("no formatter resolves %q", argv)
			}
			if got := f.Descriptor().Command; got != want {
				t.Errorf("%q resolves to %q, want %q", argv, got, want)
			}
		})
	}
}
