package hook

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"testing"

	"github.com/synapctx/sctx/internal/platform/config"
)

// TestMain redirects the telemetry spool for EVERY test in this package.
//
// spoolCoverageGap writes to $SCT__SPOOL_DIR, defaulting to ~/.config/sctx/spool — the
// developer's REAL spool, which is flushed to the platform. Tests that exercise the
// fallback path therefore emitted genuine coverage-gap events, and the fixtures became
// production data: 69 of the 76 `cargo build` events in SynapCTX's own telemetry were
// written by this package's tests, in a repository that contains no Cargo.toml.
//
// That corrupted the very instrument used to choose what to build next. `cargo build` was
// the top-ranked coverage gap and therefore the obvious next formatter, for a command
// nobody runs. The meter was measuring its own test suite.
//
// Eight tests already did this correctly with t.TempDir(), which is exactly the problem:
// an opt-in guard is one a new test forgets. Setting it here covers the whole package, and
// a per-test t.Setenv still overrides it for tests that need their own directory.
func TestMain(m *testing.M) {
	dir, err := os.MkdirTemp("", "sctx-hook-spool-")
	if err != nil {
		// Fail loudly rather than silently fall back to the real spool.
		panic("hook tests: cannot create a temp spool dir, refusing to run against the real one: " + err.Error())
	}
	if err := os.Setenv("SCT__SPOOL_DIR", dir); err != nil {
		panic("hook tests: cannot redirect SCT__SPOOL_DIR: " + err.Error())
	}

	// The same isolation, for the same reason, applied to CONSENT. Coverage-gap
	// recording now consults the customer's decision, which config.Load reads
	// from $HOME — so without this the tests assert against whatever the
	// developer happens to have chosen on their own machine, and flip from green
	// to red when they change their mind or when a disclosure bump makes their
	// answer stale. That is not a test, it is a reading of the developer.
	home, err := os.MkdirTemp("", "sctx-hook-home-")
	if err != nil {
		panic("hook tests: cannot create a temp home: " + err.Error())
	}
	if err := os.MkdirAll(filepath.Join(home, ".config", "sctx"), 0o700); err != nil {
		panic("hook tests: cannot create a temp config dir: " + err.Error())
	}
	if err := os.WriteFile(filepath.Join(home, ".config", "sctx", "config.toml"),
		[]byte(grantedConfig()), 0o600); err != nil {
		panic("hook tests: cannot write a temp config: " + err.Error())
	}
	if err := os.Setenv("HOME", home); err != nil {
		panic("hook tests: cannot redirect HOME: " + err.Error())
	}

	code := m.Run()
	_ = os.RemoveAll(dir)
	_ = os.RemoveAll(home)
	os.Exit(code)
}

// TestSpoolIsRedirectedAwayFromTheRealOne fails if TestMain is ever removed or weakened.
// Without it, the guard above is invisible: the tests still pass while quietly writing
// production telemetry, which is precisely how this went unnoticed long enough to make
// `cargo build` the top-ranked coverage gap.
func TestSpoolIsRedirectedAwayFromTheRealOne(t *testing.T) {
	dir := os.Getenv("SCT__SPOOL_DIR")
	if dir == "" {
		t.Fatal("SCT__SPOOL_DIR is unset, so spoolCoverageGap would write to the developer's real spool and ship test fixtures to the platform")
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return // cannot determine the real spool path; the non-empty check above still holds
	}
	if real := filepath.Join(home, ".config", "sctx", "spool"); dir == real {
		t.Fatalf("SCT__SPOOL_DIR points at the REAL spool %q; test fixtures would be delivered as genuine usage", real)
	}
}

// grantedConfig is a config file consenting to improvement telemetry at the
// CURRENT disclosure. Built from the constant rather than hardcoded, so a
// disclosure bump does not silently turn every gap test into a no-op.
func grantedConfig() string {
	return fmt.Sprintf("telemetry_consent = %q\ntelemetry_disclosure = %q\n",
		config.ConsentGranted, strconv.Itoa(config.CurrentDisclosure))
}
