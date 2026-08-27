package kubectl

import (
	"context"
	"strings"
	"testing"

	"github.com/synapctx/sctx/internal/domain/format"
)

// TestAggressiveGetOutputModes covers get -o json / wide / yaml routing.
func TestAggressiveGetOutputModes(t *testing.T) {
	f := New()

	t.Run("-o json delegates to jsoncompact", func(t *testing.T) {
		raw := `{
  "items": [
    {"metadata": {"name": "a"}},
    {"metadata": {"name": "b"}}
  ]
}`
		in := format.Input{Argv: []string{"kubectl", "get", "pods", "-o", "json"}, Stdout: strings.NewReader(raw)}
		out, err := f.Aggressive(context.Background(), in)
		if err != nil {
			t.Fatalf("Aggressive() error = %v", err)
		}
		if strings.Contains(string(out.Body), "\n") {
			t.Errorf("body still contains whitespace formatting: %q", out.Body)
		}
		if !strings.Contains(string(out.Body), `"name":"a"`) {
			t.Errorf("body missing expected content: %q", out.Body)
		}
		if len(out.Body) >= len(raw) {
			t.Errorf("body (%d) not smaller than raw (%d)", len(out.Body), len(raw))
		}
	})

	t.Run("-o wide caps rows and keeps header", func(t *testing.T) {
		var b strings.Builder
		b.WriteString("NAME    READY   STATUS    RESTARTS   AGE   IP           NODE\n")
		for i := range 40 {
			b.WriteString("pod-" + string(rune('a'+(i%26))) + "     1/1     Running   0          1d    10.0.0.1     node-1\n")
		}
		in := format.Input{Argv: []string{"kubectl", "get", "pods", "-o", "wide"}, Stdout: strings.NewReader(b.String())}
		out, err := f.Aggressive(context.Background(), in)
		if err != nil {
			t.Fatalf("Aggressive() error = %v", err)
		}
		body := string(out.Body)
		if !strings.HasPrefix(body, "NAME") {
			t.Errorf("body missing header, got: %q", body)
		}
		if !strings.Contains(body, "…+10 rows") {
			t.Errorf("body missing cap marker, got: %q", body)
		}
	})

	t.Run("-o yaml leaves to another tier", func(t *testing.T) {
		in := format.Input{Argv: []string{"kubectl", "get", "pods", "-o", "yaml"}, Stdout: strings.NewReader("apiVersion: v1\nkind: List\n")}
		if _, err := f.Aggressive(context.Background(), in); err != format.ErrTierInapplicable {
			t.Errorf("err = %v, want ErrTierInapplicable", err)
		}
	})

	t.Run("-o custom-columns leaves to another tier", func(t *testing.T) {
		in := format.Input{Argv: []string{"kubectl", "get", "pods", "-o", "custom-columns=NAME:.metadata.name"}, Stdout: strings.NewReader("NAME\na\n")}
		if _, err := f.Aggressive(context.Background(), in); err != format.ErrTierInapplicable {
			t.Errorf("err = %v, want ErrTierInapplicable", err)
		}
	})
}

const kubectlTopPodsFixture = `NAME       CPU(cores)   MEMORY(bytes)
web-1      5m           30Mi
api-1      250m         120Mi
cache-1    50m          80Mi
db-1       500m         200Mi
`

func TestAggressiveTopShortTableStaysNative(t *testing.T) {
	f := New()
	in := format.Input{Argv: []string{"kubectl", "top", "pods"}, Stdout: strings.NewReader(kubectlTopPodsFixture)}
	if _, err := f.Aggressive(context.Background(), in); err != format.ErrTierInapplicable {
		t.Fatalf("Aggressive() error = %v, want inapplicable", err)
	}
}

func TestAggressiveTopCapsRows(t *testing.T) {
	f := New()
	var b strings.Builder
	b.WriteString("NAME       CPU(cores)   MEMORY(bytes)\n")
	for i := range 30 {
		b.WriteString("node-" + string(rune('a'+(i%26))) + "    " + "10m          100Mi\n")
	}
	in := format.Input{Argv: []string{"kubectl", "top", "nodes"}, Stdout: strings.NewReader(b.String())}
	out, err := f.Aggressive(context.Background(), in)
	if err != nil {
		t.Fatalf("Aggressive() error = %v", err)
	}
	if !strings.Contains(string(out.Body), "…+10 rows") {
		t.Errorf("body missing cap marker, got: %q", out.Body)
	}
}

const kubectlEventsFixture = `LAST SEEN   TYPE      REASON      OBJECT           MESSAGE
5m          Normal    Scheduled   pod/web-1        Successfully assigned
4m          Warning   Failed      pod/api-1        Error pulling image
3m          Normal    Pulled      pod/web-1        Container image pulled
2m          Warning   BackOff     pod/api-1        Back-off restarting
`

func TestAggressiveEvents(t *testing.T) {
	f := New()

	t.Run("kubectl events promotes warnings", func(t *testing.T) {
		in := format.Input{Argv: []string{"kubectl", "events"}, Stdout: strings.NewReader(kubectlEventsFixture)}
		out, err := f.Aggressive(context.Background(), in)
		if err != nil {
			t.Fatalf("Aggressive() error = %v", err)
		}
		body := string(out.Body)
		failedIdx := strings.Index(body, "Failed")
		scheduledIdx := strings.Index(body, "Scheduled")
		if failedIdx < 0 || scheduledIdx < 0 {
			t.Fatalf("body missing rows: %q", body)
		}
		if failedIdx > scheduledIdx {
			t.Errorf("Warning row not promoted ahead of Normal row: %q", body)
		}
	})

	t.Run("get events routes to the events renderer", func(t *testing.T) {
		in := format.Input{Argv: []string{"kubectl", "get", "events"}, Stdout: strings.NewReader(kubectlEventsFixture)}
		out, err := f.Aggressive(context.Background(), in)
		if err != nil {
			t.Fatalf("Aggressive() error = %v", err)
		}
		if !strings.Contains(string(out.Body), "REASON") {
			t.Errorf("body missing header, got: %q", out.Body)
		}
	})
}

const kubectlRolloutStatusFixture = `Waiting for deployment "web" rollout to finish: 0 of 3 updated replicas are available...
Waiting for deployment "web" rollout to finish: 1 of 3 updated replicas are available...
Waiting for deployment "web" rollout to finish: 1 of 3 updated replicas are available...
Waiting for deployment "web" rollout to finish: 2 of 3 updated replicas are available...
deployment "web" successfully rolled out
`

func TestAggressiveRolloutStatus(t *testing.T) {
	f := New()
	in := format.Input{Argv: []string{"kubectl", "rollout", "status", "deployment/web"}, Stdout: strings.NewReader(kubectlRolloutStatusFixture)}
	out, err := f.Aggressive(context.Background(), in)
	if err != nil {
		t.Fatalf("Aggressive() error = %v", err)
	}
	body := string(out.Body)
	if !strings.Contains(body, "×2") {
		t.Errorf("body missing collapse marker for repeated line, got: %q", body)
	}
	if !strings.Contains(body, "successfully rolled out") {
		t.Errorf("body missing final success line, got: %q", body)
	}
	if len(out.Body) >= len(kubectlRolloutStatusFixture) {
		t.Errorf("body (%d) not smaller than raw (%d)", len(out.Body), len(kubectlRolloutStatusFixture))
	}
}

func TestAggressiveRolloutStatusOneLineStaysNative(t *testing.T) {
	in := format.Input{Argv: []string{"kubectl", "rollout", "status", "deployment/web"}, Stdout: strings.NewReader("deployment \"web\" successfully rolled out\n")}
	if _, err := New().Aggressive(context.Background(), in); err != format.ErrTierInapplicable {
		t.Fatalf("Aggressive() error = %v, want inapplicable", err)
	}
}

func TestAggressiveRolloutHistory(t *testing.T) {
	f := New()
	var b strings.Builder
	b.WriteString("deployment.apps/web\n")
	b.WriteString("REVISION  CHANGE-CAUSE\n")
	for i := 1; i <= 25; i++ {
		b.WriteString("1         <none>\n")
	}
	in := format.Input{Argv: []string{"kubectl", "rollout", "history", "deployment/web"}, Stdout: strings.NewReader(b.String())}
	out, err := f.Aggressive(context.Background(), in)
	if err != nil {
		t.Fatalf("Aggressive() error = %v", err)
	}
	body := string(out.Body)
	if !strings.HasPrefix(body, "deployment.apps/web") {
		t.Errorf("body missing preamble, got: %q", body)
	}
	if !strings.Contains(body, "REVISION") {
		t.Errorf("body missing header, got: %q", body)
	}
	if !strings.Contains(body, "…+5 rows") {
		t.Errorf("body missing cap marker, got: %q", body)
	}
}

func TestAggressiveAPIResources(t *testing.T) {
	f := New()
	var b strings.Builder
	b.WriteString("NAME          SHORTNAMES   APIVERSION   NAMESPACED   KIND\n")
	for i := range 60 {
		b.WriteString("resource" + string(rune('a'+(i%26))) + "   r" + string(rune('a'+(i%26))) + "           v1           true         Resource\n")
	}
	in := format.Input{Argv: []string{"kubectl", "api-resources"}, Stdout: strings.NewReader(b.String())}
	out, err := f.Aggressive(context.Background(), in)
	if err != nil {
		t.Fatalf("Aggressive() error = %v", err)
	}
	if !strings.Contains(string(out.Body), "…+20 rows") {
		t.Errorf("body missing cap marker, got: %q", out.Body)
	}
}

const kubectlApplyFixture = `deployment.apps/web configured
service/web unchanged
deployment.apps/api created
deployment.apps/cache created
deployment.apps/db created
configmap/settings unchanged
Warning: resource namespaces "old" is deprecated
`

func TestAggressiveApplyGroupsByVerb(t *testing.T) {
	f := New()
	in := format.Input{Argv: []string{"kubectl", "apply", "-f", "manifests/"}, Stdout: strings.NewReader(kubectlApplyFixture)}
	out, err := f.Aggressive(context.Background(), in)
	if err != nil {
		t.Fatalf("Aggressive() error = %v", err)
	}
	body := string(out.Body)
	for _, want := range []string{"3 created:", "1 configured:", "2 unchanged:", "deployment.apps/api", "deployment.apps/web"} {
		if !strings.Contains(body, want) {
			t.Errorf("body missing %q, got: %q", want, body)
		}
	}
	if !strings.Contains(body, "Warning: resource namespaces") {
		t.Errorf("body dropped verbatim warning line, got: %q", body)
	}
}

func TestAggressiveDeleteGroupsByVerb(t *testing.T) {
	f := New()
	raw := "pod/a deleted\npod/b deleted\nservice/c deleted\n"
	in := format.Input{Argv: []string{"kubectl", "delete", "-f", "manifests/"}, Stdout: strings.NewReader(raw)}
	out, err := f.Aggressive(context.Background(), in)
	if err != nil {
		t.Fatalf("Aggressive() error = %v", err)
	}
	if !strings.Contains(string(out.Body), "3 deleted:") {
		t.Errorf("body missing grouped deleted verb, got: %q", out.Body)
	}
}

func TestAggressiveDeletePreservesNamespaceSuffix(t *testing.T) {
	raw := "configmap \"a\" deleted from fixture namespace\nconfigmap \"b\" deleted from fixture namespace\n"
	out, err := New().Aggressive(context.Background(), format.Input{Argv: []string{"kubectl", "delete", "configmap", "a", "b"}, Stdout: strings.NewReader(raw)})
	if err != nil {
		t.Fatal(err)
	}
	if body := string(out.Body); !strings.Contains(body, "2 deleted from fixture namespace:") || !strings.Contains(body, "configmap \"a\"") {
		t.Fatalf("body = %q", body)
	}
}

func TestAggressiveResultLinesPreservesDryRunMode(t *testing.T) {
	raw := "deployment.apps/web configured (server dry run)\nservice/web unchanged (server dry run)\ndeployment.apps/api configured (server dry run)\n"
	in := format.Input{Argv: []string{"kubectl", "apply", "--dry-run=server", "-f", "x.yaml"}, Stdout: strings.NewReader(raw)}
	out, err := New().Aggressive(context.Background(), in)
	if err != nil {
		t.Fatal(err)
	}
	body := string(out.Body)
	for _, want := range []string{"2 configured (server dry run):", "1 unchanged (server dry run):", "deployment.apps/web", "service/web"} {
		if !strings.Contains(body, want) {
			t.Errorf("body missing %q: %q", want, body)
		}
	}
}

func TestEveryResultSubcommandRoutesThroughNativeVerbParser(t *testing.T) {
	tests := []struct {
		command string
		line    string
	}{
		{"apply", "deployment.apps/x configured\n"},
		{"create", "configmap/x created\n"},
		{"delete", "pod/x deleted\n"},
		{"patch", "configmap/x patched\n"},
		{"scale", "deployment.apps/x scaled\n"},
		{"label", "pod/x labeled\n"},
		{"annotate", "pod/x annotated\n"},
	}
	for _, tc := range tests {
		t.Run(tc.command, func(t *testing.T) {
			// Two lines are required for the grouped render to be smaller than
			// native output in the real tier chain; direct routing is what this
			// inspection test verifies.
			raw := tc.line + tc.line
			out, err := New().Aggressive(context.Background(), format.Input{Argv: []string{"kubectl", tc.command}, Stdout: strings.NewReader(raw)})
			if err != nil {
				t.Fatal(err)
			}
			if !strings.Contains(string(out.Body), "2 ") {
				t.Fatalf("body = %q", out.Body)
			}
		})
	}
}

// TestAggressiveNewSubcommandsNonZeroExitDegrades verifies error signal is
// never compressed away for any of the newly added subcommands.
func TestAggressiveNewSubcommandsNonZeroExitDegrades(t *testing.T) {
	f := New()
	tests := []struct {
		name string
		argv []string
	}{
		{"top", []string{"kubectl", "top", "pods"}},
		{"events", []string{"kubectl", "events"}},
		{"rollout status", []string{"kubectl", "rollout", "status", "deployment/web"}},
		{"rollout history", []string{"kubectl", "rollout", "history", "deployment/web"}},
		{"api-resources", []string{"kubectl", "api-resources"}},
		{"apply", []string{"kubectl", "apply", "-f", "x.yaml"}},
		{"get -o json", []string{"kubectl", "get", "pods", "-o", "json"}},
		{"get -o wide", []string{"kubectl", "get", "pods", "-o", "wide"}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			in := format.Input{
				Argv:     tc.argv,
				Stdout:   strings.NewReader("some output\n"),
				Stderr:   strings.NewReader("Error: something went wrong\n"),
				ExitCode: 1,
			}
			if _, err := f.Aggressive(context.Background(), in); err != format.ErrTierInapplicable {
				t.Errorf("err = %v, want ErrTierInapplicable", err)
			}
		})
	}
}
