package hook

import "testing"

// THE GAP METER MUST NOT ASK FOR FORMATTERS THAT CANNOT EXIST.
//
// It asked seven times, in escalating counts (25 → 27 → 34 → 45 → 48 → 64), for a
// `python3` formatter. There cannot be one: python3's output is whatever the script
// prints, so the shape is user code's, not the program's, and there is nothing to
// parse. Measured on the live estate 2026-08-04, 374 of the recorded gaps were of
// this kind and they were burying `terraform plan` (13) — a real target, because
// plan output is huge and highly structured.
func TestTheMeterIgnoresProgramsAFormatterCannotServe(t *testing.T) {
	// Every one of these was really recorded as a coverage gap.
	for _, cmd := range []string{
		"python3 script.py",
		"python3 - <<PY\nprint(1)\nPY",
		"python build.py",
		"sed -i s/a/b/ file.go",
		"awk '{print $1}' file",
		"perl -0pi -e s/a/b/ file",
		"node build.js",
		"bash -c 'echo hi'",
		"cp a b",
		"mv a b",
		"sleep 5",
		"wc -l file",
		"mkdir -p x/y",
		"chmod +x script",
		"which go",
	} {
		if seg, ok := gapSegment(cmd); ok {
			t.Errorf("gapSegment(%q) reported a gap (%q).\n"+
				"  A formatter for this cannot exist — either the output shape is decided by user\n"+
				"  code rather than by the program, or there is no output to compress. Counting it\n"+
				"  competes with real targets for attention, which is how `terraform plan` stayed\n"+
				"  buried under 374 events of this kind.", cmd, seg)
		}
	}
}

// A SCRIPT IS NOT A PROGRAM WE CAN SHIP A FORMATTER FOR. `deploy-bundle.sh` — one of
// our own operational scripts — was reported 25 times.
func TestTheMeterIgnoresLocalScripts(t *testing.T) {
	for _, cmd := range []string{
		"./scripts/deploy-bundle.sh tenant-control-service",
		"deploy-bundle.sh",
		"./run.bash",
		"scripts/check-public-routes.sh",
		"./tools/gen.py",
	} {
		if seg, ok := gapSegment(cmd); ok {
			t.Errorf("gapSegment(%q) reported a gap (%q) for a script.\n"+
				"  Its output is defined by its own source, which we do not ship.", cmd, seg)
		}
	}
}

// THE EXCLUSIONS MUST NOT SWALLOW A REAL GAP SITTING BESIDE ONE.
//
// `continue` semantics, matching noiseBuiltins: in `python3 build.py && terraform plan`
// the reportable gap is terraform plan, and stopping at the interpreter would hide it.
// This is the property that makes the exclusion safe rather than merely quieter.
func TestAnExcludedProgramDoesNotHideARealGapBesideIt(t *testing.T) {
	cases := []struct{ cmd, wantProgram string }{
		{"python3 gen.py && vault read secret/db", "vault read"},
		{"sed -i s/a/b/ x && mix test", "mix test"},
		{"mkdir -p out && mix test", "mix test"},
		{"./scripts/prep.sh && vault write secret/db x=1", "vault write"},
	}
	for _, tt := range cases {
		seg, ok := gapSegment(tt.cmd)
		if !ok {
			t.Errorf("gapSegment(%q) reported NO gap; the excluded program masked a real one", tt.cmd)
			continue
		}
		if got := deriveProgram(seg); got != tt.wantProgram {
			t.Errorf("gapSegment(%q) reported %q, want %q", tt.cmd, got, tt.wantProgram)
		}
	}
}

// AND THE REAL TARGETS MUST STILL BE REPORTED. An exclusion list is only as good as
// what it leaves alone — this is the half that would make the change a net loss if it
// broke, and `terraform plan` is the specific thing decontamination surfaced.
func TestRealCoverageGapsAreStillReported(t *testing.T) {
	cases := []struct{ cmd, wantProgram string }{
		{"vault read secret/db", "vault read"},
		{"vault write secret/db x=1", "vault write"},
		{"mix test --release", "mix test"},
		// An enumerated formatter surface still reports uncovered subcommands. Git is
		// deliberately absent: all finite verbs now reach fallback coverage.
		{"go env GOPATH", "go env"},
	}
	for _, tt := range cases {
		seg, ok := gapSegment(tt.cmd)
		if !ok {
			t.Fatalf("gapSegment(%q) reported no gap — a real target went silent", tt.cmd)
		}
		if got := deriveProgram(seg); got != tt.wantProgram {
			t.Errorf("gapSegment(%q) reported %q, want %q", tt.cmd, got, tt.wantProgram)
		}
	}
}

// The exclusion applies to the METER ONLY. Wrapping is a separate decision made by
// matchSegment against subcommandTable, and nothing here may change what gets
// wrapped — a command that was compressed yesterday must still be compressed today.
func TestTheExclusionDoesNotChangeWhatGetsWrapped(t *testing.T) {
	// Programs in the exclusion list are not in subcommandTable either, so they were
	// never wrapped; this pins that the two decisions stayed independent.
	for _, prog := range []string{"python3", "sed", "awk", "cp", "wc", "bash"} {
		if _, wrapped := subcommandTable[prog]; wrapped {
			t.Errorf("%q is both excluded from the meter and present in subcommandTable.\n"+
				"  If a formatter genuinely exists for it, it does not belong in unformattable —\n"+
				"  the two lists would then disagree about whether it is servable.", prog)
		}
	}
	// And a covered program must still rewrite exactly as before.
	for _, cmd := range []string{"go test ./...", "git status", "rsync -a a b"} {
		if out, ok := Rewrite(cmd); !ok {
			t.Errorf("Rewrite(%q) declined; the meter change must not affect wrapping", cmd)
		} else if out == cmd {
			t.Errorf("Rewrite(%q) returned it unchanged", cmd)
		}
	}
}
