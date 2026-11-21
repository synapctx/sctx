package fixturehygiene

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"
)

// forbidden are patterns no committed fixture may contain. Captures must be
// scrubbed to neutral paths such as /work/proj before they are copied into
// testdata.
var forbidden = []struct {
	name string
	re   *regexp.Regexp
}{
	{"macOS home directory", regexp.MustCompile(`/Users/[A-Za-z0-9._-]+/`)},
	{"macOS temp directory", regexp.MustCompile(`/private/(tmp|var)/`)},
	{"Linux home directory", regexp.MustCompile(`/home/[A-Za-z0-9._-]+/`)},
	{"Windows home directory", regexp.MustCompile(`(?i)[A-Z]:\\Users\\[A-Za-z0-9._ -]+\\`)},
	{"competitor name", regexp.MustCompile(`(?i)\brtk\b`)},
}

// allowedHomes are neutral Linux home directories that carry no identity:
// the placeholders scrubbed captures use, the public GitHub Actions runners,
// and Homebrew's standard Linux prefix.
var allowedHomes = map[string]bool{"/home/user/": true, "/home/dev/": true, "/home/runner/": true, "/home/runneradmin/": true, "/home/linuxbrew/": true}

// repoRoot walks up from this package to the directory holding go.mod.
func repoRoot(t *testing.T) string {
	t.Helper()
	dir, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			t.Fatal("go.mod not found above the test directory")
		}
		dir = parent
	}
}

func TestFixturesCarryNoPrivatePaths(t *testing.T) {
	root := repoRoot(t)
	scanned := 0
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			switch d.Name() {
			case ".git", ".claude", "bin", "node_modules":
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.Contains(filepath.ToSlash(path), "/testdata/") {
			return nil
		}
		b, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		scanned++
		for _, f := range forbidden {
			for _, loc := range f.re.FindAllIndex(b, -1) {
				if allowedHomes[string(b[loc[0]:loc[1]])] {
					continue
				}
				line := 1 + bytes.Count(b[:loc[0]], []byte("\n"))
				rel, _ := filepath.Rel(root, path)
				t.Errorf("%s:%d: %s %q — scrub the fixture before committing it", rel, line, f.name, b[loc[0]:loc[1]])
				break
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if scanned == 0 {
		t.Fatal("no testdata files found: the walk is broken, not the fixtures clean")
	}
}

// deniedHashes are the SHA-256 digests (hex) of lowercased tokens that must
// never appear in this public repository: private organisation, host and
// workload names. Only digests are committed, so the denylist cannot be READ
// as a list of names — but the digests are unsalted and the names are short, so
// a guess can be CONFIRMED against them; do not treat them as secret. A hit is
// reported as file:line and the digest; to
// learn which token it is, hash candidates with `printf %s token | shasum -a 256`.
var deniedHashes = map[string]bool{
	"fb555e88ae47204fdf2a966f3a9c941c8191dd54d0186dca9bee65ce383a83dd": true,
	"b7ebd1e9c8f6ea1f4d28d136a854cade8ff4dbd9d7f5c015e35895c7da31a9b7": true,
	"1294f34b7e0433925e3f745622dff1bcf9fa17fbb525a9e6660f0c77f93eb7bf": true,
	"05c8f03ddfe5edeaa13627c3ab8a3b2a426b7158be4808bb4997e2c36e9947ef": true,
	"c1c2e50dfded73d2dba75664fd574f781fa74e354356a7f8b5da5575f83ae2ed": true,
	"94aa886dc88dd1bddd4606b8db29ac0da0aa5b4e818c746347785dcbfddaa074": true,
	"cf974e9997085a7b8496462e8aa3e9a25aa694dec638f681bd4ecbef0c497563": true,
	"dc1609ec128270b707afbd67c3369d7ef49688b9823e6d16c11ab92fc4c7ebe5": true,
	"c6b19e3b3632d849c922116032d9e1f3daf6b4e8d577e3a601d5f50a54f57737": true,
	"251c9fc9cfc4fc2c6e5e5fe3c1782017a8cbb8c178602ab3f74e086529f659aa": true,
	"211c63a81294b3a8e8dca7f1661ba4d6cfdcab70ac7e068f96255e5547acd342": true,
	"35aa6e31e5ed06f6263b7e8a2e14b5396c260ad878cdcf91b8d17876a2f4230a": true,
	"5ddd61d07c08b0bc806648a0f0d291912f96f4a7f08efd0f2a7b707a8186e95e": true,
	"fdf0979acc9149242916c59ba0bce12410a7ed5c1e9ac16e7beed8fad75f41f6": true,
	"6307a4ef66984e7b75a09088dd54a010254848c6b10d452b19462f56a0abe0d2": true,
}

// maxJoinedParts is the longest hyphen-joined run of parts that is hashed as
// one token, so a denied name such as a five-part host prefix still matches
// inside a longer hyphenated or dotted identifier.
const maxJoinedParts = 5

// tokenHits tokenizes text on every byte outside [a-z0-9-], then hashes each
// token and every hyphen-joined run of 1..maxJoinedParts of its parts. It
// returns the digest of each denied match with its 1-based line.
func tokenHits(text string, cache map[string]bool) []hit {
	var hits []hit
	text = strings.ToLower(text)
	line, start := 1, -1
	flush := func(end int) {
		if start < 0 {
			return
		}
		parts := strings.Split(strings.Trim(text[start:end], "-"), "-")
		start = -1
		for i := range parts {
			for n := 1; n <= maxJoinedParts && i+n <= len(parts); n++ {
				tok := strings.Join(parts[i:i+n], "-")
				denied, ok := cache[tok]
				if !ok {
					sum := sha256.Sum256([]byte(tok))
					denied = deniedHashes[hex.EncodeToString(sum[:])]
					cache[tok] = denied
				}
				if denied {
					sum := sha256.Sum256([]byte(tok))
					hits = append(hits, hit{line: line, digest: hex.EncodeToString(sum[:])})
				}
			}
		}
	}
	for i := 0; i < len(text); i++ {
		c := text[i]
		if c == '-' || c >= '0' && c <= '9' || c >= 'a' && c <= 'z' {
			if start < 0 {
				start = i
			}
			continue
		}
		flush(i)
		if c == '\n' {
			line++
		}
	}
	flush(len(text))
	return hits
}

type hit struct {
	line   int
	digest string
}

func TestTokenHits(t *testing.T) {
	cache := map[string]bool{}
	for name, tc := range map[string]struct {
		in   string
		want int
	}{
		"clean":          {"kube-system coredns-5d78c9869d-abcde", 0},
		"hash unrelated": {"deadbeef " + strings.Repeat("a", 64), 0},
	} {
		if got := tokenHits(tc.in, cache); len(got) != tc.want {
			t.Errorf("%s: %d hits, want %d", name, len(got), tc.want)
		}
	}
	// A token built at run time stands in for a denied name: it must match
	// when embedded in a longer hyphenated or dotted identifier, and across
	// lines, without the denylist naming anything in plaintext.
	probe := "zzprobe-token"
	sum := sha256.Sum256([]byte(probe))
	deniedHashes[hex.EncodeToString(sum[:])] = true
	defer delete(deniedHashes, hex.EncodeToString(sum[:]))
	for name, in := range map[string]string{
		"plain":      probe,
		"dotted":     "console." + probe + ".com",
		"hyphenated": "host-a-" + probe + "-x-y",
		"upper":      strings.ToUpper(probe),
	} {
		if got := tokenHits("x\n"+in, cache); len(got) != 1 || got[0].line != 2 {
			t.Errorf("%s: hits %v, want exactly one on line 2", name, got)
		}
	}
}

func TestFixturesCarryNoDeniedNames(t *testing.T) {
	root := repoRoot(t)
	cache := map[string]bool{}
	fixtures, testFiles := 0, 0
	report := func(path string, hits []hit) {
		rel, _ := filepath.Rel(root, path)
		for _, h := range hits {
			t.Errorf("%s:%d: denied private name (sha256 %s) — replace it with a public, synthetic one", rel, h.line, h.digest)
		}
	}
	fset := token.NewFileSet()
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			switch d.Name() {
			case ".git", ".claude", "bin", "node_modules":
				return filepath.SkipDir
			}
			return nil
		}
		slash := filepath.ToSlash(path)
		switch {
		case strings.Contains(slash, "/testdata/"):
			b, err := os.ReadFile(path)
			if err != nil {
				return err
			}
			fixtures++
			report(path, tokenHits(string(b), cache))
		case strings.HasSuffix(slash, "_test.go") && (strings.Contains(slash, "/internal/") || strings.Contains(slash, "/cmd/")):
			f, err := parser.ParseFile(fset, path, nil, parser.SkipObjectResolution)
			if err != nil {
				return err
			}
			testFiles++
			var hits []hit
			ast.Inspect(f, func(n ast.Node) bool {
				lit, ok := n.(*ast.BasicLit)
				if !ok || lit.Kind != token.STRING {
					return true
				}
				val, err := strconv.Unquote(lit.Value)
				if err != nil {
					val = lit.Value
				}
				base := fset.Position(lit.Pos()).Line - 1
				for _, h := range tokenHits(val, cache) {
					hits = append(hits, hit{line: base + h.line, digest: h.digest})
				}
				return true
			})
			report(path, hits)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	// Floors, so a broken walk or a moved tree cannot pass by scanning nothing.
	if fixtures < 1000 || testFiles < 100 {
		t.Fatalf("scanned %d fixtures and %d test files: the walk is broken, not the repository clean", fixtures, testFiles)
	}
	t.Logf("scanned %d fixture files and %d test files", fixtures, testFiles)
}
