package pkgmgr

import (
	"context"
	"errors"
	"os"
	"strconv"
	"strings"
	"testing"

	"github.com/synapctx/sctx/internal/domain/format"
)

func fixture(t *testing.T, name string) (string, int) {
	t.Helper()
	b, err := os.ReadFile("testdata/" + name + ".txt")
	if err != nil {
		t.Fatal(err)
	}
	e, err := os.ReadFile("testdata/" + name + ".exit")
	if err != nil {
		t.Fatal(err)
	}
	code, _ := strconv.Atoi(strings.TrimSpace(string(e)))
	return string(b), code
}

func formatterFor(t *testing.T, prog string) format.Formatter {
	t.Helper()
	for _, f := range All() {
		if f.Descriptor().Command == prog {
			return f
		}
	}
	t.Fatalf("no formatter for %s", prog)
	return nil
}

func run(t *testing.T, tier string, argv []string, raw string, exit int) (format.Rendered, error) {
	t.Helper()
	f := formatterFor(t, argv[0])
	in := format.Input{Argv: argv, Stdout: strings.NewReader(raw), ExitCode: exit}
	if tier == "relaxed" {
		return f.Relaxed(context.Background(), in)
	}
	return f.Aggressive(context.Background(), in)
}

func TestRenders(t *testing.T) {
	cases := []struct {
		file string
		argv string
		keep []string // must survive
		drop []string // must be gone
	}{
		{"apt-get-install", "apt-get install -y curl jq",
			[]string{"NEW (24): ca-certificates curl jq", "UPGRADE (1): libssl3t64", "1 upgraded, 24 newly installed", "Fetched 5757 kB", "Updating certificates", "…+139 lines elided", "Get ×25", "Setting up ×25"},
			[]string{"Unpacking curl", "Get:1 ", "Suggested packages", "krb5-doc"}},
		{"apt-get-update", "apt-get update", []string{"Fetched 36.3 MB in 6s", "Get ×19"}, []string{"Get:6 "}},
		{"apt-install", "apt install -y htop", []string{"NEW (3): htop libnl-3-200 libnl-genl-3-200", "0 upgraded, 3 newly installed"}, []string{"WARNING: apt does not have"}},
		{"apt-remove", "apt remove -y htop", []string{"REMOVE (1): htop", "1 to remove", "AUTOREMOVABLE (2)"}, []string{"Removing htop"}},
		{"apt-list-upgradable", "apt list --upgradable",
			[]string{"12 packages (arch arm64", "libc6 2.39-0ubuntu8.9→2.39-0ubuntu8.12 [sec]", "libaudit-common 1:3.1.2-2.1build1.1→1:3.1.2-2.1ubuntu0.1 (all)", "libsystemd0 255.4-1ubuntu8.17→255.4-1ubuntu8.19"},
			[]string{"noble-updates", "[upgradable from"}},
		{"apt-list-installed-head", "apt list --installed", []string{"all installed", "jq 1.7.1-3ubuntu0.24.04.2", "krb5-locales 1.20.1-6ubuntu2.10 (all) [automatic]"}, []string{"[installed]"}},
		{"dnf-install", "dnf install -y htop jq", []string{"Package Arch Version Repository Size", "htop aarch64 3.3.0-4.fc41 fedora 556.4 KiB", "Installing: 5 packages", "Complete!"}, []string{"[1/5] jq-0", "Repositories loaded"}},
		{"dnf-upgrade", "dnf upgrade -y", []string{"xz-libs aarch64 1:5.8.1-4.fc41 updates 201.7 KiB", "Upgrading: 2 packages", "Complete!"}, []string{"Verify package files"}},
		{"yum-install", "yum install -y git-core jq", []string{"Dependencies resolved.", "openssh-clients aarch64 9.9p1-16.el9_9.rocky.0.1 baseos 741 k", "Install 8 Packages", "Complete!", "steps ×20"}, []string{"Verifying", "Running scriptlet"}},
		{"yum-remove", "yum remove -y jq", []string{"Remove 2 Packages", "Freed space: 1.2 M"}, []string{"Erasing"}},
		{"dnf-check-update", "dnf check-update", []string{"xz.aarch64 1:5.8.1-4.fc41 updates", "xz-libs.aarch64 1:5.8.1-4.fc41 updates"}, []string{"Repositories loaded"}},
		{"apk-add", "apk add curl jq", []string{"Installing (12): ca-certificates 20261020-r0", "jq 1.7.1-r0", "OK: 17 MiB in 29 packages", "trigger ×2"}, []string{"(1/12)"}},
		{"apk-upgrade", "apk upgrade", []string{"Upgrading (4): apk-tools 2.14.4-r1→2.14.13-r0", "OK: 16 MiB in 26 packages", "Continuing the upgrade transaction"}, nil},
		{"pacman-S", "pacman -S --noconfirm htop jq", []string{"Package (3) New Version", "extra/htop 3.5.3-1 0.41 MiB 0.18 MiB", "Total Download Size: 0.60 MiB", "downloading ×3"}, []string{"installing htop...", "lm_sensors"}},
		{"pacman-Syu", "pacman -Syu --noconfirm", []string{":: Starting full system upgrade...", "core/glibc 2.44+r50+g1848099f063e-1 2.44+r78+gfcfb867c58ab-1", "Failed to resolve specifier", "downloading ×29"}, []string{"( 1/15) Generating"}},
	}
	for _, tc := range cases {
		t.Run(tc.file, func(t *testing.T) {
			raw, exit := fixture(t, tc.file)
			out, err := run(t, "aggressive", strings.Fields(tc.argv), raw, exit)
			if err != nil {
				t.Fatalf("Aggressive: %v", err)
			}
			body := string(out.Body)
			if len(body) >= len(raw) {
				t.Errorf("not smaller: %d >= %d", len(body), len(raw))
			}
			for _, k := range tc.keep {
				if !strings.Contains(body, k) {
					t.Errorf("lost %q in:\n%s", k, body)
				}
			}
			for _, d := range tc.drop {
				if strings.Contains(body, d) {
					t.Errorf("kept %q in:\n%s", d, body)
				}
			}
			if strings.Contains(body, "…+") != out.Elided && !strings.Contains(tc.file, "list") {
				t.Errorf("Elided=%v disagrees with marker presence", out.Elided)
			}
		})
	}
}

// TestNonZeroExitKeepsErrorSignal: for every failing fixture, each raw line that
// carries an error signal must be present in the render.
func TestNonZeroExitKeepsErrorSignal(t *testing.T) {
	cases := []struct{ file, argv string }{
		{"apt-get-install-notfound", "apt-get install -y nosuchpkg-xyz"},
		{"apt-get-install-brokendep", "apt-get install -y ./brokendep.deb"},
		{"apt-get-install-badpost", "apt-get install -y ./badpost.deb"},
		{"apt-get-install-badversion2", "apt-get install -y curl=1.0"},
		{"dnf-install-notfound", "dnf install -y nosuchpkg-xyz"},
		{"dnf-install-nonet", "dnf install -y htop"},
		{"yum-install-notfound", "yum install -y nosuchpkg-xyz"},
		{"apk-add-notfound", "apk add nosuchpkg-xyz"},
		{"apk-update-nonet", "apk update"},
		{"apt-get-update-nonet", "apt-get update"},
		{"pacman-S-notfound", "pacman -S --noconfirm nosuchpkg-xyz"},
	}
	signal := []string{"E:", "W:", "Err:", "ERROR", "error", "No match", "Failed", "Unable", "WARNING", "unmet", "Cannot", "not available", "not installable", "Errors were", "Curl error"}
	for _, tc := range cases {
		t.Run(tc.file, func(t *testing.T) {
			raw, exit := fixture(t, tc.file)
			if exit == 0 && !strings.Contains(tc.file, "nonet") {
				t.Fatalf("fixture %s should be a failure", tc.file)
			}
			for _, tier := range []string{"aggressive", "relaxed"} {
				out, err := run(t, tier, strings.Fields(tc.argv), raw, exit)
				if errors.Is(err, format.ErrTierInapplicable) {
					continue // declining keeps every byte
				}
				if err != nil {
					t.Fatal(err)
				}
				body := string(out.Body)
				seen := map[string]bool{}
				for _, l := range strings.Split(raw, "\n") {
					l = strings.TrimRight(l, "\r")
					for _, s := range signal {
						if !strings.Contains(l, s) {
							continue
						}
						// consecutive identical lines are folded with ×N, which is a marked elision.
						if !strings.Contains(body, l) && !strings.Contains(body, l+" ×") {
							t.Errorf("%s: lost error line %q\n%s", tier, l, body)
						}
						seen[l] = true
					}
				}
				if len(seen) == 0 {
					t.Fatalf("fixture has no error line; test is vacuous")
				}
			}
		})
	}
}

func TestFailedRunKeepsLastStep(t *testing.T) {
	raw, exit := fixture(t, "apt-get-install-badpost")
	out, err := run(t, "aggressive", strings.Fields("apt-get install -y ./badpost.deb"), raw, exit)
	if err != nil {
		t.Fatal(err)
	}
	body := string(out.Body)
	for _, want := range []string{"dpkg: error processing package badpost", "E: Sub-process /usr/bin/dpkg returned an error code (1)", "postinst: cannot connect to database", "last step: Setting up badpost (1.0) ..."} {
		if !strings.Contains(body, want) {
			t.Errorf("missing %q in:\n%s", want, body)
		}
	}
}

func TestCheckUpdateExit100IsNotFailure(t *testing.T) {
	raw, _ := fixture(t, "dnf-check-update")
	out, err := run(t, "aggressive", []string{"dnf", "check-update"}, raw, 100)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(out.Body), "last step:") {
		t.Errorf("exit 100 from check-update must not be treated as a failed transaction:\n%s", out.Body)
	}
}

func TestRelaxedKeepsPackageBlocks(t *testing.T) {
	raw, exit := fixture(t, "apt-get-install")
	out, err := run(t, "relaxed", strings.Fields("apt-get install -y curl jq"), raw, exit)
	if err != nil {
		t.Fatal(err)
	}
	body := string(out.Body)
	for _, want := range []string{"The following NEW packages will be installed:", "  ca-certificates curl jq krb5-locales", "Suggested packages:", "…+"} {
		if !strings.Contains(body, want) {
			t.Errorf("relaxed lost %q", want)
		}
	}
	if strings.Contains(body, "Unpacking jq") {
		t.Errorf("relaxed kept progress")
	}
}

func TestDeclines(t *testing.T) {
	raw, _ := fixture(t, "apt-get-install")
	for _, argv := range []string{
		"apt search jq", "apt show jq", "apt-get install --print-uris -y jq", "apt-get source jq",
		"dnf list installed", "dnf search htop", "dnf info jq", "yum repolist", "yum --json install jq",
		"apk info", "apk search jq", "pacman -Ss htop", "pacman -Qi htop", "pacman -Si htop", "pacman --version", "apt", "apt-get",
	} {
		t.Run(argv, func(t *testing.T) {
			if _, err := run(t, "aggressive", strings.Fields(argv), raw, 0); !errors.Is(err, format.ErrTierInapplicable) {
				t.Errorf("want ErrTierInapplicable, got %v", err)
			}
		})
	}
	t.Run("empty output", func(t *testing.T) {
		if _, err := run(t, "aggressive", []string{"apt-get", "update"}, "", 0); !errors.Is(err, format.ErrTierInapplicable) {
			t.Errorf("got %v", err)
		}
	})
	t.Run("already minimal", func(t *testing.T) {
		if _, err := run(t, "aggressive", []string{"apk", "add", "x"}, "OK: 1 MiB in 3 packages\n", 0); !errors.Is(err, format.ErrTierInapplicable) {
			t.Errorf("got %v", err)
		}
	})
}

func TestOptionValueIsNotTheVerb(t *testing.T) {
	raw, exit := fixture(t, "apt-get-update")
	if _, err := run(t, "aggressive", strings.Fields("apt-get -o Acquire::Retries=3 update"), raw, exit); err != nil {
		t.Errorf("flags before the verb must still resolve it: %v", err)
	}
	if _, err := run(t, "aggressive", strings.Fields("apt-get -o install update"), raw, exit); err != nil {
		t.Errorf("an -o value must not be read as the verb: %v", err)
	}
}

func TestStderrIsFoldedIn(t *testing.T) {
	f := formatterFor(t, "apt-get")
	raw, _ := fixture(t, "apt-get-install-notfound")
	in := format.Input{Argv: []string{"apt-get", "install", "-y", "x"}, Stdout: strings.NewReader(""),
		Stderr: strings.NewReader(raw), ExitCode: 100}
	out, err := f.Aggressive(context.Background(), in)
	if err != nil {
		t.Fatal(err)
	}
	if !out.FoldStderr || !strings.Contains(string(out.Body), "E: Unable to locate package") {
		t.Errorf("stderr error not folded: fold=%v body=%q", out.FoldStderr, out.Body)
	}
}

func TestCarriageReturnProgressKeepsLastFrame(t *testing.T) {
	raw := "Reading package lists...\nGet:1 http://x a [1 kB]\n(Reading database ... 5%\r(Reading database ... 100%\r\n" +
		"E: Sub-process /usr/bin/dpkg returned an error code (1)\n"
	out, err := run(t, "aggressive", []string{"apt-get", "install", "-y", "a"}, strings.Repeat("Get:1 http://x a [1 kB]\n", 5)+raw, 100)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(out.Body), "E: Sub-process") {
		t.Errorf("lost error: %s", out.Body)
	}
}
