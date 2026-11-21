# gotestsum fixtures

Real captures (gotestsum 1.13.x, go1.27.4, macOS arm64) from a module with
passing, failing (table subtests), panicking, racing, timing-out, TestMain-exit
and non-compiling packages. `<name>.{stdout,stderr,exit}.txt`; names map to
`--format` values (default=pkgname, testname, verbose=standard-verbose,
quiet=standard-quiet, dots, dots2=dots-v2, testdox, pkgdetail=
pkgname-and-test-fails) plus `rerun`, `okonly`, `okpkg`, `build`, `main`,
`timeout`, `race`, `nomatch`, `nosummary` (`--no-summary=failed,skipped`).
Paths scrubbed to /work and /usr/local/go.
