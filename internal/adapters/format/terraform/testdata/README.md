# terraform / tofu fixtures

REAL output captured on macOS arm64 with terraform 1.x and OpenTofu 1.13.9
against local-only configs (random, local, null providers), stdout and stderr
merged, ANSI colour left in. `*.exit` holds the exit code. `tfbad-*`/`tofubad-*`
are failing runs (unclosed block, missing variable, failing apply with a
provisioner error). `golden/` holds the aggressive renders; refresh with
`go test -update` and review the diff.
