package run

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	domexec "github.com/synapctx/sctx/internal/domain/exec"
	"github.com/synapctx/sctx/internal/domain/format"
	"github.com/synapctx/sctx/internal/domain/telemetry"
	"github.com/synapctx/sctx/internal/platform/rawcache"
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

func TestExecuteRawRecoveryIsLocalAndOnlyForElision(t *testing.T) {
	const secretRaw = "first\nsecret-sentinel\nlast\n"
	newService := func(t *testing.T, elided bool) (*Service, *bytes.Buffer, *fakeEmitter, string) {
		t.Helper()
		registry := NewRegistry()
		registry.Register(&fakeFormatter{
			match: format.Match{Command: "tool"},
			aggressive: func(format.Input) (format.Rendered, error) {
				return format.Rendered{Body: []byte("summary"), Elided: elided}, nil
			},
			relaxed: func(format.Input) (format.Rendered, error) {
				return format.Rendered{}, format.ErrTierInapplicable
			},
		})
		root := filepath.Join(t.TempDir(), "raw")
		stderr := &bytes.Buffer{}
		emitter := &fakeEmitter{}
		svc := NewService(registry, fakeRunner{stdout: secretRaw}, nil, emitter, nil,
			&bytes.Buffer{}, stderr, Options{
				Version:  "v",
				RawCache: rawcache.New(root, time.Hour, 1024),
			})
		return svc, stderr, emitter, root
	}

	t.Run("elided output is recoverable", func(t *testing.T) {
		svc, stderr, emitter, root := newService(t, true)
		if _, err := svc.Execute(context.Background(), []string{"tool"}); err != nil {
			t.Fatal(err)
		}
		entries, err := os.ReadDir(root)
		if err != nil || len(entries) != 1 {
			t.Fatalf("cache entries = %d, %v", len(entries), err)
		}
		path := filepath.Join(root, entries[0].Name(), "stdout")
		got, err := os.ReadFile(path)
		if err != nil || string(got) != secretRaw {
			t.Fatalf("cached stdout = %q, %v", got, err)
		}
		if !strings.Contains(stderr.String(), filepath.Dir(path)) {
			t.Fatalf("recovery hint = %q", stderr.String())
		}
		payload, err := json.Marshal(emitter.events)
		if err != nil {
			t.Fatal(err)
		}
		if bytes.Contains(payload, []byte("secret-sentinel")) || bytes.Contains(payload, []byte(root)) {
			t.Fatalf("telemetry contains raw output or recovery path: %s", payload)
		}
	})

	t.Run("lossless render creates no cache or hint", func(t *testing.T) {
		svc, stderr, _, root := newService(t, false)
		if _, err := svc.Execute(context.Background(), []string{"tool"}); err != nil {
			t.Fatal(err)
		}
		if stderr.Len() != 0 {
			t.Fatalf("unexpected recovery hint: %q", stderr.String())
		}
		if _, err := os.Stat(root); !os.IsNotExist(err) {
			t.Fatalf("lossless render created cache: %v", err)
		}
	})
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

func TestExecuteEmitsUnambiguousFormatterDimensions(t *testing.T) {
	tests := []struct {
		name              string
		argv              []string
		stdout            string
		registerMatcher   bool
		registeredDecline bool
		enableSniff       bool
		forceTier         string
		wantProgram       string
		wantMatched       bool
		wantKind          string
		wantReduced       bool
		wantReason        string
	}{
		{
			name:            "registered formatter matches",
			argv:            []string{"go", "test", "./..."},
			stdout:          "native output with enough bytes to save tokens\n",
			registerMatcher: true,
			wantProgram:     "go test",
			wantMatched:     true,
			wantKind:        telemetry.FormatterKindDedicated,
			wantReduced:     true,
		},
		{
			name:        "no formatter, no sniff: unmatched",
			argv:        []string{"terraform", "plan", "-out", "x"},
			stdout:      "plain text\n",
			wantProgram: "terraform plan",
			wantMatched: false,
			wantKind:    telemetry.FormatterKindNone,
			wantReason:  telemetry.DeclineUnsupportedCommand,
		},
		{
			name:        "generic compression is reduced but not dedicated",
			argv:        []string{"internal-tool", "report"},
			stdout:      "native output with enough bytes to save tokens\n",
			enableSniff: true,
			wantProgram: "internal-tool",
			wantKind:    telemetry.FormatterKindGeneric,
			wantReduced: true,
		},
		{
			name:              "dedicated formatter can decline",
			argv:              []string{"go", "test", "./..."},
			stdout:            "unrecognized native output\n",
			registerMatcher:   true,
			registeredDecline: true,
			wantProgram:       "go test",
			wantMatched:       true,
			wantKind:          telemetry.FormatterKindDedicated,
			wantReason:        telemetry.DeclineUnrecognizedOutput,
		},
		{
			name:        "explicit force-tier bypass is classified",
			argv:        []string{"internal-tool", "report"},
			stdout:      "native output\n",
			enableSniff: true,
			forceTier:   "verbatim",
			wantProgram: "internal-tool",
			wantKind:    telemetry.FormatterKindGeneric,
			wantReason:  telemetry.DeclineExplicitBypass,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			registry := NewRegistry()
			if tt.registerMatcher {
				aggressive := func(format.Input) (format.Rendered, error) { return format.Rendered{Body: []byte("x")}, nil }
				if tt.registeredDecline {
					aggressive = func(format.Input) (format.Rendered, error) { return format.Rendered{}, format.ErrTierInapplicable }
				}
				registry.Register(&fakeFormatter{
					match:      format.Match{Command: "go", Subcommands: []string{"test"}},
					aggressive: aggressive,
					relaxed: func(format.Input) (format.Rendered, error) {
						if tt.registeredDecline {
							return format.Rendered{}, format.ErrTierInapplicable
						}
						return format.Rendered{Body: []byte("x")}, nil
					},
				})
			}

			emitter := &fakeEmitter{}
			var sniff format.Formatter
			if tt.enableSniff {
				sniff = &fakeFormatter{
					aggressive: func(format.Input) (format.Rendered, error) { return format.Rendered{Body: []byte("x")}, nil },
					relaxed:    func(format.Input) (format.Rendered, error) { return format.Rendered{}, format.ErrTierInapplicable },
				}
			}
			svc := NewService(registry, fakeRunner{stdout: tt.stdout, exitCode: 0}, nil, emitter, sniff, &bytes.Buffer{}, &bytes.Buffer{}, Options{Version: "v", ForceTier: tt.forceTier})

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
			if ev.FormatterKind != tt.wantKind || ev.OutputReduced != tt.wantReduced || ev.DeclineReason != tt.wantReason {
				t.Errorf("dimensions = kind %q reduced %t reason %q; want %q %t %q",
					ev.FormatterKind, ev.OutputReduced, ev.DeclineReason, tt.wantKind, tt.wantReduced, tt.wantReason)
			}
		})
	}
}
