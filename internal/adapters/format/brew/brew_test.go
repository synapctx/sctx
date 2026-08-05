package brew

import (
	"context"
	"strings"
	"testing"

	"github.com/synapctx/sctx/internal/domain/format"
)

func TestDescriptor(t *testing.T) {
	f := New()
	if got := f.Descriptor().Command; got != "brew" {
		t.Errorf("Command = %q, want brew", got)
	}
	if subs := f.Descriptor().Subcommands; len(subs) != 0 {
		t.Errorf("Subcommands = %v, want empty (dispatch handled internally)", subs)
	}
}

func TestSubcommand(t *testing.T) {
	tests := []struct {
		name string
		argv []string
		want string
	}{
		{"install", []string{"brew", "install", "wget"}, "install"},
		{"upgrade", []string{"brew", "upgrade", "wget"}, "upgrade"},
		{"install with flag", []string{"brew", "install", "--verbose", "wget"}, "install"},
		{"list unsupported", []string{"brew", "list"}, ""},
		{"info unsupported", []string{"brew", "info", "wget"}, ""},
		{"bare", []string{"brew"}, ""},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := subcommand(tc.argv); got != tc.want {
				t.Errorf("subcommand(%v) = %q, want %q", tc.argv, got, tc.want)
			}
		})
	}
}

const installFixture = `==> Downloading https://ghcr.io/v2/homebrew/core/wget/manifests/1.21.4
Already downloaded: /Users/dev/Library/Caches/Homebrew/downloads/wget-1.21.4.bottle_manifest.json
==> Fetching wget
==> Downloading https://ghcr.io/v2/homebrew/core/wget/blobs/sha256:abcd1234
######################################################################## 100.0%
==> Pouring wget--1.21.4.arm64_sonoma.bottle.tar.gz
==> Caveats
Bash completion has been installed to:
  /opt/homebrew/etc/bash_completion.d
==> Summary
🍺  /opt/homebrew/Cellar/wget/1.21.4: 9 files, 4.5MB
==> Running brew cleanup wget...
Disable this behaviour by setting HOMEBREW_NO_INSTALL_CLEANUP.
Hide these hints with HOMEBREW_NO_ENV_HINTS (see man brew).
`

func TestAggressiveInstallCollapsesNoiseKeepsResultAndCaveats(t *testing.T) {
	f := New()
	in := format.Input{
		Argv:   []string{"brew", "install", "wget"},
		Stdout: strings.NewReader(installFixture),
	}
	out, err := f.Aggressive(context.Background(), in)
	if err != nil {
		t.Fatalf("Aggressive() error = %v", err)
	}
	body := string(out.Body)
	if !strings.Contains(body, "🍺  /opt/homebrew/Cellar/wget/1.21.4: 9 files, 4.5MB") {
		t.Errorf("missing result line: %q", body)
	}
	if !strings.Contains(body, "==> Caveats") || !strings.Contains(body, "Bash completion has been installed to:") {
		t.Errorf("missing caveats: %q", body)
	}
	if strings.Contains(body, "######") {
		t.Errorf("progress bar not collapsed: %q", body)
	}
	if strings.Contains(body, "==> Fetching") || strings.Contains(body, "==> Downloading") {
		t.Errorf("fetch/download noise not collapsed: %q", body)
	}
	if !strings.Contains(body, "…+") {
		t.Errorf("missing elision marker: %q", body)
	}
	if strings.Count(body, "\n") >= strings.Count(installFixture, "\n") {
		t.Errorf("output not smaller than input")
	}
}

const alreadyInstalledFixture = `Warning: wget 1.21.4 is already installed and up-to-date.
To reinstall 1.21.4, run:
  brew reinstall wget
`

func TestAggressiveNoOpCollapsesToOneLine(t *testing.T) {
	f := New()
	in := format.Input{
		Argv:   []string{"brew", "install", "wget"},
		Stdout: strings.NewReader(alreadyInstalledFixture),
	}
	out, err := f.Aggressive(context.Background(), in)
	if err != nil {
		t.Fatalf("Aggressive() error = %v", err)
	}
	body := string(out.Body)
	if strings.Contains(body, "\n") {
		t.Errorf("no-op body has more than one line: %q", body)
	}
	if !strings.HasPrefix(body, "Warning: wget 1.21.4 is already installed and up-to-date.") {
		t.Errorf("no-op body missing warning line: %q", body)
	}
	if !strings.Contains(body, "…+") {
		t.Errorf("missing elision marker for dropped hint lines: %q", body)
	}
}

const installErrorFixture = `==> Fetching wget
==> Downloading https://ghcr.io/v2/homebrew/core/wget/manifests/1.21.4
curl: (6) Could not resolve host: ghcr.io
Error: Failed to download resource "wget_bottle_manifest"
Download failed: https://ghcr.io/v2/homebrew/core/wget/manifests/1.21.4
`

func TestAggressiveKeepsErrorSignalVerbatim(t *testing.T) {
	f := New()
	in := format.Input{
		Argv:     []string{"brew", "install", "wget"},
		Stdout:   strings.NewReader(installErrorFixture),
		ExitCode: 1,
	}
	out, err := f.Aggressive(context.Background(), in)
	if err != nil {
		t.Fatalf("Aggressive() error = %v", err)
	}
	body := string(out.Body)
	if !strings.Contains(body, `curl: (6) Could not resolve host: ghcr.io`) {
		t.Errorf("curl error dropped: %q", body)
	}
	if !strings.Contains(body, `Error: Failed to download resource "wget_bottle_manifest"`) {
		t.Errorf("Error: line dropped: %q", body)
	}
	if !strings.Contains(body, "Download failed:") {
		t.Errorf("error continuation line dropped: %q", body)
	}
	if !strings.Contains(body, "…+") {
		t.Errorf("fetch/download noise before the error not collapsed: %q", body)
	}
}

func TestAggressiveUnsupportedSubcommandInapplicable(t *testing.T) {
	f := New()
	in := format.Input{
		Argv:   []string{"brew", "list"},
		Stdout: strings.NewReader("wget\ncurl\n"),
	}
	if _, err := f.Aggressive(context.Background(), in); err != format.ErrTierInapplicable {
		t.Errorf("err = %v, want ErrTierInapplicable", err)
	}
}

func TestAggressiveNonBrewBlobInapplicable(t *testing.T) {
	f := New()
	in := format.Input{
		Argv:   []string{"brew", "install", "wget"},
		Stdout: strings.NewReader("just some unrelated plain text\nwith no brew markers at all\n"),
	}
	if _, err := f.Aggressive(context.Background(), in); err != format.ErrTierInapplicable {
		t.Errorf("err = %v, want ErrTierInapplicable", err)
	}
}

func TestRelaxedDropsProgressAndFetchDownloadSpam(t *testing.T) {
	f := New()
	in := format.Input{
		Argv:   []string{"brew", "install", "wget"},
		Stdout: strings.NewReader(installFixture),
	}
	out, err := f.Relaxed(context.Background(), in)
	if err != nil {
		t.Fatalf("Relaxed() error = %v", err)
	}
	body := string(out.Body)
	if strings.Contains(body, "######") {
		t.Errorf("progress bar not dropped: %q", body)
	}
	if strings.Contains(body, "==> Fetching") || strings.Contains(body, "==> Downloading") {
		t.Errorf("fetch/download spam not dropped: %q", body)
	}
	if !strings.Contains(body, "🍺  /opt/homebrew/Cellar/wget/1.21.4: 9 files, 4.5MB") {
		t.Errorf("result line dropped: %q", body)
	}
	if !strings.Contains(body, "==> Caveats") {
		t.Errorf("caveats dropped: %q", body)
	}
}

func TestRelaxedKeepsErrorSignal(t *testing.T) {
	f := New()
	in := format.Input{
		Argv:     []string{"brew", "install", "wget"},
		Stdout:   strings.NewReader(installErrorFixture),
		ExitCode: 1,
	}
	out, err := f.Relaxed(context.Background(), in)
	if err != nil {
		t.Fatalf("Relaxed() error = %v", err)
	}
	body := string(out.Body)
	if !strings.Contains(body, `Error: Failed to download resource "wget_bottle_manifest"`) {
		t.Errorf("Error: line dropped: %q", body)
	}
	if !strings.Contains(body, "curl: (6) Could not resolve host: ghcr.io") {
		t.Errorf("curl error dropped: %q", body)
	}
}
