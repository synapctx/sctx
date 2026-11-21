package gcloud

import (
	"context"
	"errors"
	"flag"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/synapctx/sctx/internal/domain/format"
)

var update = flag.Bool("update", false, "rewrite golden files")

func load(t *testing.T, name string) (stdout, stderr string, exit int) {
	t.Helper()
	rd := func(ext string) string {
		b, err := os.ReadFile("testdata/" + name + "." + ext)
		if err != nil {
			t.Fatal(err)
		}
		return string(b)
	}
	if strings.TrimSpace(rd("exit")) != "0" {
		exit = 1
	}
	return rd("out"), rd("err"), exit
}

func in(argv, stdout, stderr string, exit int) format.Input {
	return format.Input{Argv: strings.Fields(argv), Stdout: strings.NewReader(stdout), Stderr: strings.NewReader(stderr), ExitCode: exit}
}

func TestDescriptor(t *testing.T) {
	if New().Descriptor().Command != "gcloud" {
		t.Fatal("descriptor")
	}
}

// Fixtures are REAL gcloud 580.0.0 output (macOS arm64), stdout and stderr
// captured separately; account, project ids and numbers replaced.
func TestRealFixtures(t *testing.T) {
	cases := []struct {
		name     string
		argv     string
		must     []string
		mustNot  []string
		declines bool
	}{
		{name: "projects", argv: "gcloud projects list",
			must:    []string{"PROJECT_ID\tNAME\tPROJECT_NUMBER\tENVIRONMENT", "acm-development-0a1b2c\tAcme Corp. - Development\t123456789012", "gen-lang-client-1234567890\tDefault Gemini Project\t1234567890123"},
			mustNot: []string{"  "}},
		{name: "instances", argv: "gcloud compute instances list",
			must: []string{"ERROR: (gcloud.compute.instances.list) PERMISSION_DENIED: Compute Engine API has not been used in project acmecorp-production-0a1b2c",
				"This command is authenticated as dev.user@example.com", "…+2 lines (text repeated from the ERROR line)", "Google developers console API activation",
				"reason: SERVICE_DISABLED", "activationUrl:"}},
		{name: "badcmd", argv: "gcloud compute instances nope",
			must: []string{"ERROR: (gcloud.compute.instances) Invalid choice: 'nope'.", "Maybe you meant:", "  gcloud compute instance-groups", "…+", "more suggestions", "gcloud help -- SEARCH_TERMS"}},
		{name: "help", argv: "gcloud compute instances list --help",
			must: []string{"NAME\n    gcloud compute instances list - list Compute Engine instances", "SYNOPSIS", "DESCRIPTION"}, mustNot: []string{"\x1b"}},
		// Nothing to compress, or machine output: decline so the caller sees it verbatim.
		{name: "clusters", argv: "gcloud container clusters list", declines: true},
		{name: "badproj", argv: "gcloud compute instances list --project=nope", declines: true},
		{name: "services", argv: "gcloud services list --enabled", declines: true},
		{name: "config", argv: "gcloud config list", declines: true},
		{name: "projects-json", argv: "gcloud projects list --format=json", declines: true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			out, errOut, exit := load(t, tc.name)
			r, err := New().Aggressive(context.Background(), in(tc.argv, out, errOut, exit))
			if tc.declines {
				if !errors.Is(err, format.ErrTierInapplicable) {
					t.Fatalf("want decline, got %v", err)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			body := string(r.Body)
			for _, w := range tc.must {
				if !strings.Contains(body, w) {
					t.Errorf("missing %q in:\n%s", w, body)
				}
			}
			for _, w := range tc.mustNot {
				if strings.Contains(body, w) {
					t.Errorf("kept %q", w)
				}
			}
			if len(body) >= len(out)+len(errOut) {
				t.Errorf("not smaller: %d >= %d", len(body), len(out)+len(errOut))
			}
			if errOut != "" && !r.FoldStderr {
				t.Error("stderr consumed but not folded")
			}
			golden := filepath.Join("testdata", "golden", tc.name+".golden")
			if *update {
				_ = os.MkdirAll(filepath.Dir(golden), 0o755)
				if err := os.WriteFile(golden, []byte(body), 0o644); err != nil {
					t.Fatal(err)
				}
			}
			want, err := os.ReadFile(golden)
			if err != nil || string(want) != body {
				t.Errorf("golden mismatch (run -update): %v", err)
			}
		})
	}
}

// The tab table is exact: re-padding each cell by the header offsets gives the
// original rows back.
func TestTabulateRoundTrips(t *testing.T) {
	raw, _, _ := load(t, "projects")
	got, ok := tabulate(raw)
	if !ok {
		t.Fatal("declined")
	}
	rawLines := strings.Split(strings.TrimRight(raw, "\n"), "\n")
	for i, l := range strings.Split(strings.TrimRight(got, "\n"), "\n") {
		if strings.Join(strings.Fields(strings.ReplaceAll(l, "\t", "  ")), " ") != strings.Join(strings.Fields(rawLines[i]), " ") {
			t.Errorf("row %d changed: %q vs %q", i, l, rawLines[i])
		}
	}
}

func TestTabulateKeepsEmptyMiddleCell(t *testing.T) {
	raw := "NAME    ZONE        STATUS\nvm-1    us-a-1      RUNNING\nvm-2                STOPPED\n"
	got, ok := tabulate(raw)
	if !ok || !strings.Contains(got, "vm-2\t\tSTOPPED") {
		t.Fatalf("ok=%v got %q", ok, got)
	}
}

func TestMisalignedTableDeclines(t *testing.T) {
	raw := "NAME    ZONE\nvm-1abcdefZONE-x\nvm-2    us\n"
	if _, ok := tabulate(raw); ok {
		t.Fatal("misaligned table must decline")
	}
}

func TestRelaxedDoesNotDedupe(t *testing.T) {
	out, errOut, exit := load(t, "instances")
	if _, err := New().Relaxed(context.Background(), in("gcloud compute instances list", out, errOut, exit)); !errors.Is(err, format.ErrTierInapplicable) {
		t.Fatalf("relaxed must keep error text byte for byte: %v", err)
	}
}
