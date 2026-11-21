package awscli

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/synapctx/sctx/internal/domain/format"
)

var update = flag.Bool("update", false, "rewrite golden files")

func load(t *testing.T, name string) (string, int) {
	t.Helper()
	b, err := os.ReadFile("testdata/" + name + ".txt")
	if err != nil {
		t.Fatal(err)
	}
	exit := 0
	if e, err := os.ReadFile("testdata/" + name + ".exit"); err == nil && strings.TrimSpace(string(e)) != "0" {
		exit = 1
	}
	return string(b), exit
}

func in(argv string, stdout string, exit int) format.Input {
	return format.Input{Argv: strings.Fields(argv), Stdout: strings.NewReader(stdout), Stderr: strings.NewReader(""), ExitCode: exit}
}

func TestDescriptor(t *testing.T) {
	if New().Descriptor().Command != "aws" {
		t.Fatal("descriptor")
	}
}

// Fixtures are REAL aws-cli 2.41.2 output (macOS arm64) from a read-only
// session; account ids, ARNs, bucket and VPC ids are replaced with
// same-length placeholders so table widths stay faithful.
func TestRealFixtures(t *testing.T) {
	cases := []struct {
		name     string
		argv     string
		must     []string
		mustNot  []string
		declines bool
	}{
		{name: "regions-table", argv: "aws ec2 describe-regions --output table",
			must:    []string{"DescribeRegions", "Regions: Endpoint=ec2.ap-south-1.amazonaws.com; OptInStatus=opt-in-not-required; RegionName=ap-south-1", "  Geography: Name=India"},
			mustNot: []string{"+-----", "||"}},
		{name: "sts-table", argv: "aws sts get-caller-identity --output table",
			must:    []string{"GetCallerIdentity", "Account | 111122223333", "Arn | arn:aws:iam::111122223333:user/example.user.dev.abc0000000000", "UserId | AIDAEXAMPLEEXAMPLE123"},
			mustNot: []string{"+-----"}},
		{name: "help-vpcs", argv: "aws ec2 describe-vpcs help",
			must:    []string{"DESCRIBE-VPCS()", "NAME\n     describe-vpcs -", "DESCRIPTION", "describe-vpcs is a paginated operation."},
			mustNot: []string{"\x08"}},
		{name: "help-s3", argv: "aws s3 help", must: []string{"DESCRIPTION", "SYNOPSIS"}, mustNot: []string{"\x08"}},
		{name: "s3cp-dry", argv: "aws s3 cp dry s3://example-bucket-sctx/prefix --recursive --dryrun",
			must: []string{"(dryrun) upload dry/ -> s3://example-bucket-sctx/prefix/ ×7: f1.txt f2.txt f3.txt f4.txt f5.txt f6.txt sub/z.log"}},
		// Declines: JSON belongs to the lossless fallback; text, short output and
		// every failure stay byte for byte.
		{name: "regions", argv: "aws ec2 describe-regions", declines: true},
		{name: "vpcs", argv: "aws ec2 describe-vpcs", declines: true},
		{name: "regions-text", argv: "aws ec2 describe-regions --output text", declines: true},
		{name: "sts", argv: "aws sts get-caller-identity", declines: true},
		{name: "sts-text", argv: "aws sts get-caller-identity --output text", declines: true},
		{name: "s3ls", argv: "aws s3 ls", declines: true},
		{name: "s3ls-rec", argv: "aws s3 ls s3://b --recursive", declines: true},
		{name: "err-instances", argv: "aws ec2 describe-instances", declines: true},
		{name: "err-rolelist", argv: "aws iam list-roles", declines: true},
		{name: "err-badbucket", argv: "aws s3 ls s3://nope", declines: true},
		{name: "err-badcmd", argv: "aws ec2 describe-nothing", declines: true},
		{name: "err-badparam", argv: "aws ec2 describe-vpcs --vpc-ids vpc-123", declines: true},
		{name: "err-s3sync", argv: "aws s3 sync dry s3://example-bucket-sctx/prefix --dryrun", declines: true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			raw, exit := load(t, tc.name)
			r, err := New().Aggressive(context.Background(), in(tc.argv, raw, exit))
			if tc.declines {
				if !errors.Is(err, format.ErrTierInapplicable) {
					t.Fatalf("want decline, got err=%v body=%q", err, r.Body)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			body := string(r.Body)
			for _, w := range tc.must {
				if !strings.Contains(body, w) {
					t.Errorf("missing %q in:\n%.1500s", w, body)
				}
			}
			for _, w := range tc.mustNot {
				if strings.Contains(body, w) {
					t.Errorf("kept %q", w)
				}
			}
			if len(body) >= len(raw) {
				t.Errorf("not smaller: %d >= %d", len(body), len(raw))
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

func TestNonZeroExitDeclinesEvenForTables(t *testing.T) {
	raw, _ := load(t, "regions-table")
	if _, err := New().Aggressive(context.Background(), in("aws ec2 describe-regions --output table", raw, 1)); !errors.Is(err, format.ErrTierInapplicable) {
		t.Fatalf("got %v", err)
	}
}

// The transfer fold is exact: expanding SRC+rel -> DST+rel reproduces every
// operation, and a run with an odd line keeps it as written.
func TestTransferFoldIsExactAndBails(t *testing.T) {
	raw, _ := load(t, "s3cp-dry")
	got := renderTransfer(raw)
	for _, l := range strings.Split(strings.TrimSpace(raw), "\n") {
		src, dst, _ := strings.Cut(strings.TrimPrefix(l, "(dryrun) upload: "), " to ")
		rel := strings.TrimPrefix(src, "dry/")
		if !strings.Contains(got, rel) || dst != "s3://example-bucket-sctx/prefix/"+rel {
			t.Errorf("operation lost: %q", l)
		}
	}
	odd := "(dryrun) upload: a/x.txt to s3://b/p/x.txt\n(dryrun) upload: a/y.txt to s3://b/p/y.txt\n(dryrun) upload: a/z.txt to s3://b/other/z.txt\n(dryrun) upload: a/with space.txt to s3://b/p/with space.txt\n"
	out := renderTransfer(odd)
	if !strings.Contains(out, "with space.txt to s3://b/p/with space.txt") || !strings.Contains(out, "s3://b/other/z.txt") {
		t.Fatalf("odd lines not preserved:\n%s", out)
	}
}

func TestProgressTickersDropped(t *testing.T) {
	raw := "Completed 256.0 KiB/1.2 MiB (1.1 MiB/s) with 2 file(s) remaining\rupload: ./a to s3://b/a\rCompleted 1.2 MiB/1.2 MiB (3.1 MiB/s) with 1 file(s) remaining\rupload: ./b to s3://b/b\n"
	out := renderTransfer(raw)
	if strings.Contains(out, "Completed") || !strings.Contains(out, "upload: ./a to s3://b/a") || !strings.Contains(out, "upload: ./b to s3://b/b") {
		t.Fatalf("got %q", out)
	}
}

func TestLsCapMarksElision(t *testing.T) {
	var b strings.Builder
	for i := 0; i < 500; i++ {
		fmt.Fprintf(&b, "2025-05-21 20:48:06       1415 obj-%04d.json\n", i)
	}
	r, err := New().Aggressive(context.Background(), in("aws s3 ls s3://b/ --recursive", b.String(), 0))
	if err != nil {
		t.Fatal(err)
	}
	body := string(r.Body)
	if !r.Elided || !strings.Contains(body, "…+380 lines") || !strings.Contains(body, "obj-0000.json") || !strings.Contains(body, "obj-0499.json") || strings.Contains(body, "obj-0200.json") {
		t.Fatalf("cap wrong: elided=%v\n%s", r.Elided, body)
	}
	if _, err := New().Relaxed(context.Background(), in("aws s3 ls s3://b/ --recursive", b.String(), 0)); !errors.Is(err, format.ErrTierInapplicable) {
		t.Fatalf("relaxed must not cap: %v", err)
	}
}

func TestWordsSkipsFlagValues(t *testing.T) {
	got := words(strings.Fields("aws --profile dev --region eu-west-1 s3 ls --recursive"))
	if strings.Join(got, ",") != "s3,ls" {
		t.Fatalf("got %v", got)
	}
}
