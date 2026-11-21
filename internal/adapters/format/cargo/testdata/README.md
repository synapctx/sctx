# cargo fixtures

Captured from real binaries on macOS arm64 (cargo 1.100.0, clippy 0.1.100,
cargo-nextest 0.9.152) against small fixture crates; stdout and stderr were
redirected to separate files (`<name>.stdout.txt`, `<name>.stderr.txt`,
`<name>.exit.txt`). Absolute paths are scrubbed to `/work`. `fmt-bad` keeps the
colour escapes `cargo fmt --check` emits even when piped.

| Fixture | Command | Exit |
|---|---|---:|
| build-ok / build-warn / build-cerr | `cargo build` (first build, warning, 3 compile errors) | 0 / 0 / 101 |
| check-cerr | `cargo check` with compile errors | 101 |
| test-ok / test-fail / test-fail-ff / test-quiet / test-bt | `cargo test` (all pass; 3 unit + 1 doctest fail; fail-fast; `-q`; `RUST_BACKTRACE=1`) | 0 / 101 |
| test-dupwarn / ws-test | warning repeated per target; two-crate workspace with one failing test | 0 / 101 |
| clippy / clippy-deny / clippy-ok | `cargo clippy`, `-- -D warnings`, clean | 0 / 101 / 0 |
| doc / run / install / install-nf | `cargo doc`, `cargo run`, `cargo install --path`, unknown crate | 0 / 0 / 0 / 101 |
| fmt-bad | `cargo fmt --check` | 1 |
| nx-ok / nx-fail / nx-ws | `cargo nextest run` | 0 / 100 / 100 |
