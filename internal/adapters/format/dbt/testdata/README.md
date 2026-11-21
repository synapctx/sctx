Captured with dbt-core 1.12.7 + dbt-postgres 1.9.1 (pip-installed into a
`python:3.12-slim` arm64 image; the official dbt-postgres image has no arm64
build) against a `postgres:17` server on a user-defined docker network. Project:
13 staging views, 1 mart table, 5 data tests; `runfail`/`buildfail` add a
model selecting from a missing table plus a dependent that is skipped.
`runfail_nc` is `--no-use-colors`. dbt writes its log to stdout even on failure
(`*.exit` has the real status: `compfail` exits 2, `debug` exits 1 because git
is absent in the image). Colour escapes are kept as captured.
