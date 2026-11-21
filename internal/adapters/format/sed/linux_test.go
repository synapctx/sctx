package sed

import (
	"os"
	"strings"
	"testing"
)

// Fixtures are real `sed -n 1,70p` output: linux-gnu-* from GNU sed 4.9 on
// ubuntu:24.04, linux-busybox-* from busybox sed 1.36 on alpine:3.20.

func TestLinuxSedReadShapes(t *testing.T) {
	for _, distro := range []string{"gnu", "busybox"} {
		t.Run(distro+"/dups", func(t *testing.T) {
			b, err := os.ReadFile("testdata/linux-" + distro + "-sed-n-dups.txt")
			if err != nil {
				t.Fatal(err)
			}
			raw := string(b)
			out, err := New().Relaxed(t.Context(), input([]string{"sed", "-n", "1,70p", "logs/dups.log"}, raw, 0))
			if err != nil {
				t.Fatalf("relaxed: %v", err)
			}
			if len(out.Body) >= len(raw) || !strings.Contains(string(out.Body), "WARN retrying connection to db:5432 ×70") {
				t.Fatalf("repeat not collapsed with a count:\n%s", out.Body)
			}
		})
		t.Run(distro+"/plain prose lines are never dropped", func(t *testing.T) {
			b, err := os.ReadFile("testdata/linux-" + distro + "-sed-n.txt")
			if err != nil {
				t.Fatal(err)
			}
			raw := string(b)
			if out, err := New().Aggressive(t.Context(), input([]string{"sed", "-n", "1,20p", "logs/app.log"}, raw, 0)); err == nil {
				t.Fatalf("aggressive rewrote plain log lines:\n%s", out.Body)
			}
			if out, err := New().Relaxed(t.Context(), input([]string{"sed", "-n", "1,20p", "logs/app.log"}, raw, 0)); err == nil && !strings.Contains(string(out.Body), "id=19") {
				t.Fatalf("relaxed lost a line:\n%s", out.Body)
			}
		})
	}
}
