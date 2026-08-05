package run

import (
	"bytes"
	"context"
	"io"
	"testing"

	domexec "github.com/synapctx/sctx/internal/domain/exec"
	"github.com/synapctx/sctx/internal/domain/format"
	"github.com/synapctx/sctx/internal/domain/telemetry"
)

// spillString is a minimal domexec.Spill backed by an in-memory string.
type spillString struct{ s string }

func (s spillString) Bytes() (io.Reader, error) { return bytes.NewReader([]byte(s.s)), nil }
func (s spillString) Len() int64                { return int64(len(s.s)) }
func (s spillString) Spilled() bool             { return false }
func (s spillString) Close() error              { return nil }

type fakeRunner struct {
	stdout   string
	stderr   string
	exitCode int
}

func (r fakeRunner) Run(_ context.Context, _ domexec.Command) (domexec.Outcome, error) {
	return domexec.Outcome{
		ExitCode: r.exitCode,
		Stdout:   spillString{r.stdout},
		Stderr:   spillString{r.stderr},
	}, nil
}

type fakeEmitter struct {
	events []telemetry.Event
}

func (e *fakeEmitter) Emit(ev telemetry.Event)       { e.events = append(e.events, ev) }
func (e *fakeEmitter) Flush(_ context.Context) error { return nil }

func TestExecuteEmitsFormatterMatchedAndProgram(t *testing.T) {
	tests := []struct {
		name            string
		argv            []string
		stdout          string
		registerMatcher bool
		enableSniff     bool
		wantProgram     string
		wantMatched     bool
	}{
		{
			name:            "registered formatter matches",
			argv:            []string{"go", "test", "./..."},
			stdout:          "ok\n",
			registerMatcher: true,
			wantProgram:     "go test",
			wantMatched:     true,
		},
		{
			name:        "no formatter, no sniff: unmatched",
			argv:        []string{"terraform", "plan", "-out", "x"},
			stdout:      "plain text\n",
			wantProgram: "terraform plan",
			wantMatched: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			registry := NewRegistry()
			if tt.registerMatcher {
				registry.Register(&fakeFormatter{
					match:      format.Match{Command: "go", Subcommands: []string{"test"}},
					aggressive: func(format.Input) (format.Rendered, error) { return format.Rendered{Body: []byte("x")}, nil },
					relaxed:    func(format.Input) (format.Rendered, error) { return format.Rendered{Body: []byte("x")}, nil },
				})
			}

			emitter := &fakeEmitter{}
			svc := NewService(registry, fakeRunner{stdout: tt.stdout, exitCode: 0}, nil, emitter, nil, &bytes.Buffer{}, &bytes.Buffer{}, Options{Version: "v"})

			if _, err := svc.Execute(context.Background(), tt.argv); err != nil {
				t.Fatalf("Execute: %v", err)
			}

			if len(emitter.events) != 1 {
				t.Fatalf("events = %d, want 1", len(emitter.events))
			}
			ev := emitter.events[0]
			if ev.Program != tt.wantProgram {
				t.Errorf("Program = %q, want %q", ev.Program, tt.wantProgram)
			}
			if ev.FormatterMatched != tt.wantMatched {
				t.Errorf("FormatterMatched = %v, want %v", ev.FormatterMatched, tt.wantMatched)
			}
		})
	}
}
