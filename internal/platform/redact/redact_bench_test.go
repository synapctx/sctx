package redact

import (
	"math/rand"
	"strings"
	"testing"
)

// oneMiBFixture builds a ~1 MiB byte slice of mixed log-like lines with a
// handful of real secrets sprinkled in, so the benchmark reflects a
// realistic scan rather than either a worst case (all matches) or a best
// case (no matches at all).
func oneMiBFixture() []byte {
	r := rand.New(rand.NewSource(1))
	words := []string{"connecting", "to", "host", "response", "status", "200", "OK", "retrying", "user", "session", "closed", "handler", "dispatch"}
	var b strings.Builder
	secrets := []string{
		"AKIAABCDEFGHIJKLMNOP",
		"Authorization: Bearer eyJhbGciOiJIUzI1NiJ9.eyJzdWIiOiIxMjM0NTY3ODkwIn0.dozjgNryP4J3jVmNHl0w5N_XgL0n3I9PlFUP0THsR8U",
		"sk_live_" + strings.Repeat("a", 20),
	}
	line := 0
	for b.Len() < 1<<20 {
		line++
		for i := 0; i < 8; i++ {
			b.WriteString(words[r.Intn(len(words))])
			b.WriteByte(' ')
		}
		if line%500 == 0 {
			b.WriteString(secrets[r.Intn(len(secrets))])
			b.WriteByte(' ')
		}
		b.WriteByte('\n')
	}
	return []byte(b.String())
}

func BenchmarkApplyOneMiB(b *testing.B) {
	fixture := oneMiBFixture()
	b.SetBytes(int64(len(fixture)))
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		Apply(fixture)
	}
}
