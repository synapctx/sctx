package rsync

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/synapctx/sctx/internal/domain/format"
)

// input builds a format.Input the way the run pipeline does. Stderr is left empty and the
// fixtures fold rsync's diagnostics into stdout, matching what the pipeline hands a
// formatter when stderr has already been merged.
func input(stdout string, exitCode int) format.Input {
	return format.Input{
		Command:  "rsync",
		Argv:     []string{"rsync", "-av", "src/", "dst/"},
		Stdout:   strings.NewReader(stdout),
		Stderr:   strings.NewReader(""),
		ExitCode: exitCode,
	}
}

// Every fixture below is REAL output captured from GNU rsync 3.4.1 on the deployment VM
// (and openrsync on macOS where noted), not written from memory. That distinction already
// caught one bug: the two dialects use different file-list banners, and a formatter built
// on the macOS wording declines on every Linux host — which is where the workload runs.

const gnuFirstSync = `sending incremental file list
file1.bin
file10.bin
file11.bin
file12.bin
file2.bin
file3.bin
file4.bin
file5.bin
file6.bin
file7.bin
file8.bin
file9.bin
sub/
sub/a.txt
sub/deep/
sub/deep/b.txt

sent 32,195 bytes  received 298 bytes  64,986.00 bytes/sec
total size is 31,212  speedup is 0.96
`

const gnuNoChanges = `sending incremental file list

sent 363 bytes  received 14 bytes  754.00 bytes/sec
total size is 31,212  speedup is 82.79
`

const gnuWithDelete = `sending incremental file list
deleting file3.bin
newfile.txt

sent 419 bytes  received 50 bytes  938.00 bytes/sec
total size is 30,016  speedup is 64.00
`

const gnuProgress = `sending incremental file list
file1.bin
            400 100%  390.62kB/s    0:00:00              400 100%  390.62kB/s    0:00:00 (xfr#1, to-chk=15/17)
file10.bin
          4,000 100%    3.81MB/s    0:00:00            4,000 100%    3.81MB/s    0:00:00 (xfr#2, to-chk=14/17)
file11.bin
          4,400 100%    4.20MB/s    0:00:00            4,400 100%    4.20MB/s    0:00:00 (xfr#3, to-chk=13/17)
file12.bin
          4,800 100%    4.58MB/s    0:00:00            4,800 100%    4.58MB/s    0:00:00 (xfr#4, to-chk=12/17)

sent 32,195 bytes  received 298 bytes  64,986.00 bytes/sec
total size is 31,212  speedup is 0.96
`

const gnuItemized = `sending incremental file list
.d..t...... ./
>f+++++++++ file1.bin
>f+++++++++ file10.bin
>f.st...... file2.bin
.f...p..... file4.bin
cd+++++++++ sub/

sent 419 bytes  received 50 bytes  938.00 bytes/sec
total size is 30,016  speedup is 64.00
`

const gnuStats = `sending incremental file list

Number of files: 17 (reg: 14, dir: 3)
Number of created files: 0
Number of deleted files: 0
Number of regular files transferred: 0
Total file size: 30,016 bytes
Total transferred file size: 0 bytes
Literal data: 0 bytes
Matched data: 0 bytes
File list size: 0
File list generation time: 0.001 seconds
File list transfer time: 0.000 seconds
Total bytes sent: 368
Total bytes received: 14

sent 368 bytes  received 14 bytes  764.00 bytes/sec
total size is 30,016  speedup is 78.58
`

const gnuDryRun = `sending incremental file list
./
another.txt

sent 404 bytes  received 24 bytes  856.00 bytes/sec
total size is 30,018  speedup is 70.14 (DRY RUN)
`

// Exit code 23. Both diagnostic lines must survive verbatim.
const gnuMissingSource = `sending incremental file list
rsync: [sender] change_dir "/tmp/does-not-exist" failed: No such file or directory (2)

sent 19 bytes  received 12 bytes  62.00 bytes/sec
total size is 0  speedup is 0.00
rsync error: some files/attrs were not transferred (see previous errors) (code 23) at main.c(1347) [sender=3.4.1]
`

// macOS openrsync, protocol 29 — a different banner entirely.
const openrsyncFirstSync = `Transfer starting: 17 files
file1.bin
file2.bin
sub/
sub/a.txt

sent 32489 bytes  received 340 bytes  8007073 bytes/sec
total size is 31212  speedup is 0.95
`

// openrsync (macOS) prefixes diagnostics with its PID — captured from the real binary.
const openrsyncError = `Transfer starting: 0 files

sent 29 bytes  received 20 bytes  490000 bytes/sec
total size is 0  speedup is 0.00
rsync(18346): error: /tmp/definitely-not-here/: (l)stat: No such file or directory
`

// A LONG stretch of output that is not rsync's. It must exceed minLines, or the tier
// declines on length alone and looksLikeRsync is never exercised — which is exactly how the
// first version of TestNonRsyncOutputDeclines passed without testing anything.
const notRsyncButLong = `Usage: rsync [OPTION]... SRC [SRC]... DEST
  -v, --verbose               increase verbosity
  -a, --archive               archive mode
  -z, --compress              compress file data during the transfer
      --delete                delete extraneous files from dest dirs
  -n, --dry-run               perform a trial run with no changes made
  -P                          same as --partial --progress
  -h, --help                  show this help
See http://rsync.samba.org/ for updates and bug reports.
`

func render(t *testing.T, raw string, exit int) string {
	t.Helper()
	f := New()
	out, err := f.Aggressive(context.Background(), input(raw, exit))
	if err != nil {
		t.Fatalf("Aggressive() error = %v", err)
	}
	return string(out.Body)
}

func TestAggressiveSummarisesRealOutput(t *testing.T) {
	got := render(t, gnuFirstSync, 0)
	for _, want := range []string{"14 transferred", "2 dirs", "sent 31.4KB", "total 30.5KB", "speedup 0.96"} {
		if !strings.Contains(got, want) {
			t.Errorf("render missing %q\ngot:\n%s", want, got)
		}
	}
	// A representative sample of paths must survive; the point is not to hide what moved.
	if !strings.Contains(got, "file1.bin") {
		t.Errorf("render dropped every path\ngot:\n%s", got)
	}
}

func TestAggressiveIsActuallySmaller(t *testing.T) {
	// The tier chain rejects a render that is not smaller than its input as an anomaly,
	// so a formatter that "works" but does not compress is a formatter that never runs.
	for name, raw := range map[string]string{
		"first sync": gnuFirstSync,
		"delete":     gnuWithDelete,
		"progress":   gnuProgress,
		"itemized":   gnuItemized,
		"stats":      gnuStats,
		"openrsync":  openrsyncFirstSync,
	} {
		got := render(t, raw, 0)
		if len(got) >= len(raw) {
			t.Errorf("%s: render is %d bytes for %d bytes of input — not smaller, the chain would reject it\ngot:\n%s",
				name, len(got), len(raw), got)
		}
	}
}

func TestProgressLinesAreElidedAndCounted(t *testing.T) {
	got := render(t, gnuProgress, 0)
	if strings.Contains(got, "100%") || strings.Contains(got, "to-chk=") {
		t.Errorf("progress noise survived\ngot:\n%s", got)
	}
	// Every elision must be visible. An invisible one is indistinguishable from a bug.
	if !strings.Contains(got, "4 progress lines elided") {
		t.Errorf("elision not disclosed\ngot:\n%s", got)
	}
	if !strings.Contains(got, "4 transferred") {
		t.Errorf("progress lines were counted as transferred files\ngot:\n%s", got)
	}
}

func TestDeletionsAreNeverHidden(t *testing.T) {
	got := render(t, gnuWithDelete, 0)
	if !strings.Contains(got, "1 deleted") || !strings.Contains(got, "file3.bin") {
		t.Errorf("a deletion was not reported — the most consequential thing rsync does\ngot:\n%s", got)
	}
}

func TestErrorsSurviveVerbatimOnFailure(t *testing.T) {
	got := render(t, gnuMissingSource, 23)
	for _, want := range []string{
		`rsync: [sender] change_dir "/tmp/does-not-exist" failed: No such file or directory (2)`,
		"code 23",
		"EXIT 23",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("error signal lost: missing %q\ngot:\n%s", want, got)
		}
	}

	// Presence is not enough, and this is how the first version of this test passed while
	// the formatter was wrong. `rsync error: ... (code 23)` begins "rsync " with a SPACE,
	// which the diagnostic pattern did not match, so the line fell through and was
	// classified as a TRANSFERRED FILE — satisfying Contains("code 23") from under the
	// "+" marker while the headline claimed "1 transferred" for a run that transferred
	// nothing and failed. Assert the CLASSIFICATION, not merely the bytes.
	if !strings.Contains(got, "0 transferred") {
		t.Errorf("a failed run reported files as transferred; a diagnostic was probably parsed as a path\ngot:\n%s", got)
	}
	for line := range strings.SplitSeq(got, "\n") {
		if strings.HasPrefix(strings.TrimSpace(line), "+ ") && strings.Contains(line, "rsync error") {
			t.Errorf("a diagnostic was listed as a transferred path: %q\ngot:\n%s", line, got)
		}
	}
	// Both diagnostics must appear ABOVE the headline, so a reader sees the failure first.
	iErr := strings.Index(got, "change_dir")
	iHead := strings.Index(got, "rsync: 0 transferred")
	if iErr < 0 || iHead < 0 || iErr > iHead {
		t.Errorf("diagnostics must precede the summary line\ngot:\n%s", got)
	}
}

func TestItemizedChangesSeparateDataFromAttributes(t *testing.T) {
	got := render(t, gnuItemized, 0)
	// `.f...p.....` changed permissions only; counting it as transferred would overstate
	// what actually moved.
	if !strings.Contains(got, "attr-only") {
		t.Errorf("attribute-only change not distinguished\ngot:\n%s", got)
	}
	if strings.Contains(got, ">f+++++++++") {
		t.Errorf("raw itemize codes survived\ngot:\n%s", got)
	}
}

func TestDryRunIsAnnounced(t *testing.T) {
	got := render(t, gnuDryRun, 0)
	if !strings.Contains(got, "DRY RUN") {
		t.Errorf("a dry run rendered as a real transfer — the reader would believe files moved\ngot:\n%s", got)
	}
}

func TestStatsBlockIsTrimmedNotDropped(t *testing.T) {
	got := render(t, gnuStats, 0)
	if !strings.Contains(got, "Number of files: 17") {
		t.Errorf("useful --stats lines were dropped\ngot:\n%s", got)
	}
	if strings.Contains(got, "File list generation time") {
		t.Errorf("redundant --stats lines survived\ngot:\n%s", got)
	}
	if !strings.Contains(got, "more --stats lines") {
		t.Errorf("trimmed --stats lines were not disclosed\ngot:\n%s", got)
	}
}

func TestBothDialectsParse(t *testing.T) {
	// openrsync (macOS) and GNU rsync (Linux) announce the file list differently. Handling
	// only one means declining on the platform that matters.
	for name, raw := range map[string]string{"GNU": gnuFirstSync, "openrsync": openrsyncFirstSync} {
		p, err := parse(strings.NewReader(raw))
		if err != nil {
			t.Fatalf("%s: parse error %v", name, err)
		}
		if !p.looksLikeRsync() {
			t.Errorf("%s: output not recognised as rsync", name)
		}
		if len(p.transferred) == 0 {
			t.Errorf("%s: no transferred paths parsed", name)
		}
	}
}

func TestNonRsyncOutputDeclines(t *testing.T) {
	for name, raw := range map[string]string{
		"short help text": "Usage: rsync [OPTION]... SRC [SRC]... DEST\n  -v, --verbose\n  -a, --archive\n",
		"empty":           "",
		"other tool":      "warning: something unrelated happened\nand another line\n",
		// The load-bearing case. The three above are all shorter than minLines, so the
		// tier declines on LENGTH and looksLikeRsync is never reached — mutation-testing
		// showed that deleting the guard entirely kept this test green. Only a fixture
		// long enough to clear minLines can exercise it.
		"long help text": notRsyncButLong,
	} {
		f := New()
		if _, err := f.Aggressive(context.Background(), input(raw, 0)); err == nil {
			t.Errorf("%s: Aggressive accepted output that is not rsync's; it must decline so the chain degrades", name)
		}
	}
}

func TestAlreadyCompactOutputEitherDeclinesOrShrinks(t *testing.T) {
	// The no-change case is only four lines. Whichever branch the formatter takes, the
	// outcome must be safe: decline, or produce something genuinely smaller. Written
	// unconditionally because the earlier version wrapped its assertion in `if err == nil`,
	// so a decline made the test pass without checking anything at all.
	f := New()
	out, err := f.Aggressive(context.Background(), input(gnuNoChanges, 0))
	if err != nil {
		return // declined: the chain degrades, which is a correct outcome
	}
	if len(out.Body) >= len(gnuNoChanges) {
		t.Errorf("accepted already-compact output and produced something no smaller (%d >= %d)\ngot:\n%s",
			len(out.Body), len(gnuNoChanges), out.Body)
	}
}

func TestRelaxedKeepsEveryPathAndDiagnostic(t *testing.T) {
	f := New()
	out, err := f.Relaxed(context.Background(), input(gnuProgress, 0))
	if err != nil {
		t.Fatalf("Relaxed() error = %v", err)
	}
	body := string(out.Body)
	for _, want := range []string{"file1.bin", "file10.bin", "file11.bin", "file12.bin", "sent 32,195 bytes"} {
		if !strings.Contains(body, want) {
			t.Errorf("Relaxed dropped %q, but it must only remove noise\ngot:\n%s", want, body)
		}
	}
	if strings.Contains(body, "to-chk=") {
		t.Errorf("Relaxed kept progress noise\ngot:\n%s", body)
	}
	if !strings.Contains(body, "noise lines elided") {
		t.Errorf("Relaxed elided without disclosing it\ngot:\n%s", body)
	}
}

func TestRelaxedDeclinesWhenNothingIsNoise(t *testing.T) {
	f := New()
	if _, err := f.Relaxed(context.Background(), input(gnuWithDelete, 0)); err == nil {
		t.Error("Relaxed accepted output with no noise to remove; the render would not be smaller and the chain rejects that as an anomaly")
	}
}

func TestUnrecognisedLinesAreNeverSilentlyDropped(t *testing.T) {
	// A non-indented unknown line is treated as a transferred PATH, which is the right
	// guess for rsync's file list and is why this case alone did not exercise the `other`
	// bucket at all — mutation-testing showed `other` could be dropped entirely with this
	// test still green.
	got := render(t, gnuWithDelete+"some totally unexpected line from a future rsync\n", 0)
	if !strings.Contains(got, "some totally unexpected line") {
		t.Errorf("an unrecognised line vanished\ngot:\n%s", got)
	}

	// An INDENTED unknown line is the one that reaches `other`: it is not a path (rsync
	// does not indent those) and not a progress line. It must still be emitted, because a
	// formatter that discards what it cannot parse is indistinguishable from one that
	// works — the reader has no way to tell something was there.
	indented := gnuWithDelete + "    some indented diagnostic from a future rsync version\n"
	p, err := parse(strings.NewReader(indented))
	if err != nil {
		t.Fatalf("parse error: %v", err)
	}
	if len(p.other) != 1 {
		t.Fatalf("indented unknown line did not reach the `other` bucket: other=%v transferred=%v", p.other, p.transferred)
	}
	got = render(t, indented, 0)
	if !strings.Contains(got, "some indented diagnostic") {
		t.Errorf("an unrecognised INDENTED line vanished from the render\ngot:\n%s", got)
	}
}

// TestElisionsAreAlwaysDisclosed — sctx's contract is that every elision carries an
// explicit marker. Mutation-testing found nothing asserted this: both deleting the `…+N`
// counter and raising maxListed so nothing elides left the suite green, which means the
// formatter could silently hide 800 paths and no test would notice.
func TestElisionsAreAlwaysDisclosed(t *testing.T) {
	var b strings.Builder
	b.WriteString("sending incremental file list\n")
	for i := range 40 {
		fmt.Fprintf(&b, "app/file%02d.txt\n", i)
	}
	b.WriteString("\nsent 12,345 bytes  received 678 bytes  1,000.00 bytes/sec\n")
	b.WriteString("total size is 99,999  speedup is 8.10\n")
	raw := b.String()

	got := render(t, raw, 0)
	if !strings.Contains(got, "40 transferred") {
		t.Errorf("count wrong; every path must be counted even when not listed\ngot:\n%s", got)
	}
	if !strings.Contains(got, "…+") {
		t.Errorf("40 paths were reduced to a sample with no elision marker; the reader cannot tell anything was omitted\ngot:\n%s", got)
	}
	// The disclosed remainder must be honest: 40 paths, maxListed shown, rest counted.
	if !strings.Contains(got, fmt.Sprintf("…+%d", 40-maxListed)) {
		t.Errorf("elision count is not 40-%d; a wrong count is worse than none\ngot:\n%s", maxListed, got)
	}
}

// TestOpenrsyncPidPrefixedErrorIsADiagnostic — openrsync writes `rsync(18346): error: ...`.
// A pattern anchored on `rsync:` or `rsync ` misses the parenthesised PID, and the line is
// then classified as a transferred PATH: an error reported as a successful transfer. Found
// by running the real binary end to end on macOS, not from the fixtures.
func TestOpenrsyncPidPrefixedErrorIsADiagnostic(t *testing.T) {
	p, err := parse(strings.NewReader(openrsyncError))
	if err != nil {
		t.Fatalf("parse error: %v", err)
	}
	if len(p.errors) != 1 {
		t.Errorf("openrsync diagnostic not recognised: errors=%v transferred=%v", p.errors, p.transferred)
	}
	for _, path := range p.transferred {
		if strings.Contains(path, "error:") {
			t.Errorf("a diagnostic was parsed as a transferred path: %q", path)
		}
	}
}
