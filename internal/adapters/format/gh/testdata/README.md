# GitHub CLI native fixtures

These fixtures preserve output shapes captured from `gh 2.97.0` on 2026-08-15.
Public, read-only commands targeted `cli/cli`; identifiers and URLs are retained
because they are part of the native grammar. No issue, pull request, workflow,
repository, or release was mutated while capturing them.

The error fixture models native transport/resource diagnostics. Pagination
fixtures are deliberately synthetic combinations of native JSON pages so tests
can prove adjacent documents remain authoritative while a slurped JSON document
may be compacted.
