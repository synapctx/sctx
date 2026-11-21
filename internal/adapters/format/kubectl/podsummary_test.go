package kubectl

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/synapctx/sctx/internal/domain/format"
)

func TestPodSummaryAllNamespacesFixture(t *testing.T) {
	raw := nativeFixture(t, "get-pods-all-namespaces.txt")
	out, err := New().Aggressive(context.Background(), format.Input{
		Argv: []string{"kubectl", "get", "pods", "-A"}, Stdout: strings.NewReader(raw),
	})
	if err != nil {
		t.Fatal(err)
	}
	body := string(out.Body)
	t.Logf("raw=%d body=%d\n%s", len(raw), len(body), body)
	for _, want := range []string{
		"266 pods: 226 running, 35 completed, 5 unhealthy",
		"\nrunning: api 57, cache 45, cert-manager 3,",
		"\ncompleted: batch 33, ingress-nginx 2",
		"unhealthy: NAMESPACE | NAME | READY | STATUS | RESTARTS",
		"batch cleanup-failing-92r4n 0/1 Error 0",
		"debug badimage 0/1 ImagePullBackOff 0",
		"debug crasher 0/1 CrashLoopBackOff 6 (3m49s ago)",
		"ingress-nginx ingress-nginx-controller-b4b746f47-hv8cd 0/1 Pending 0",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("body missing %q", want)
		}
	}
	if strings.Contains(body, "db-backup-8fbwl") {
		t.Errorf("completed pod name must be counted, not listed")
	}
	if len(body) > 1300 {
		t.Errorf("body %d bytes, want <= 700", len(body))
	}
}

func TestPodSummaryDecisions(t *testing.T) {
	hdr := fmt.Sprintf("%-10s %-8s %-6s %-18s %-14s %s\n", "NAMESPACE", "NAME", "READY", "STATUS", "RESTARTS", "AGE")
	row := func(ns, name, ready, status, restarts string) string {
		return fmt.Sprintf("%-10s %-8s %-6s %-18s %-14s %s\n", ns, name, ready, status, restarts, "1d")
	}
	build := func(extra ...string) string {
		var b strings.Builder
		b.WriteString(hdr)
		for i := range 9 {
			b.WriteString(row("ns", "ok-"+string(rune('a'+i)), "1/1", "Running", "0"))
		}
		for _, e := range extra {
			b.WriteString(e)
		}
		return b.String()
	}
	cases := []struct {
		name    string
		raw     string
		want    []string
		notWant []string
	}{
		{
			name:    "old restarts stay counted not listed",
			raw:     build(row("ns", "old", "1/1", "Running", "2 (3d ago)")),
			want:    []string{"10 pods: 10 running", "1 running had earlier restarts, max 2", "running: ns 10"},
			notWant: []string{"unhealthy"},
		},
		{
			name:    "recent restart is listed",
			raw:     build(row("ns", "flappy", "1/1", "Running", "1 (20h ago)")),
			want:    []string{"9 running, 1 unhealthy", "ns flappy 1/1 Running 1 (20h ago)"},
			notWant: []string{"earlier restarts"},
		},
		{
			name: "many restarts listed",
			raw:  build(row("ns", "noisy", "1/1", "Running", "7")),
			want: []string{"1 unhealthy", "ns noisy 1/1 Running 7"},
		},
		{
			name: "not ready running is listed",
			raw:  build(row("ns", "half", "1/2", "Running", "0")),
			want: []string{"1 unhealthy", "ns half 1/2 Running 0"},
		},
		{
			name: "pending and crashloop listed, succeeded counted",
			raw: build(
				row("ns", "pend", "0/1", "Pending", "0"),
				row("ns", "crash", "0/1", "CrashLoopBackOff", "12 (1m ago)"),
				row("jobs", "done", "0/1", "Succeeded", "0")),
			want:    []string{"9 running, 1 completed, 2 unhealthy", "completed: jobs 1", "ns pend 0/1 Pending 0", "ns crash 0/1 CrashLoopBackOff 12 (1m ago)"},
			notWant: []string{" done "},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			out, err := New().Aggressive(context.Background(), format.Input{
				Argv: []string{"kubectl", "get", "pods", "-A"}, Stdout: strings.NewReader(tc.raw),
			})
			if err != nil {
				t.Fatal(err)
			}
			body := string(out.Body)
			for _, w := range tc.want {
				if !strings.Contains(body, w) {
					t.Errorf("missing %q in:\n%s", w, body)
				}
			}
			for _, w := range tc.notWant {
				if strings.Contains(body, w) {
					t.Errorf("unexpected %q in:\n%s", w, body)
				}
			}
		})
	}
}
