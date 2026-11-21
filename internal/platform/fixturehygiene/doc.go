// Package fixturehygiene holds a single repository-wide test: every testdata
// fixture in this PUBLIC repository must be free of machine-local paths and
// private identifiers. Fixtures are captured from real binaries, and a capture
// carries whatever the capturing machine had — home directories, scratch
// paths, account names. The test is the only thing standing between such a
// capture and a public commit.
package fixturehygiene
