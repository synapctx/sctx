# Native `dig` fixtures

Captured read-only on macOS arm64 with DiG 9.10.6 on 2026-08-15:

- `dig example.com A`
- `dig nonexistent-sctx-test.invalid A`

Stdout is stored byte-for-byte. Both commands exited 0 with empty stderr. IDs,
TTLs, resolver addresses, timing and SOA serials are capture-time values; the
tests assert grammar and preservation rather than current DNS data.
