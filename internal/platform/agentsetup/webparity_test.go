package agentsetup

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/synapctx/sctx/pkg/agentdoc"
)

// PASTING WHAT THE WEBSITE SHOWS MUST LEAVE A MACHINE THE INSTALLER CALLS DONE.
//
// synapctx.com/sctx/ renders its setup guidance from pkg/agentdoc — the same
// definitions this installer uses — so that someone who follows the page by hand
// and someone who runs `sctx setup --install` end up with the same file. This is
// the test that keeps that true across both repositories.
//
// The failure it exists to catch is not a crash. If the page's output is not
// recognised as ours, the next `--install` APPENDS a second copy of the same
// document, and from then on it loads twice into every session. Nothing reports an
// error; the machine just quietly pays double.
func TestAFileWrittenTheWayTheWebsiteDescribesItReportsTaught(t *testing.T) {
	for _, a := range agentdoc.KnownAgents {
		t.Run(a.ID, func(t *testing.T) {
			home := t.TempDir()
			// The agent's own configuration, so detection has something real to
			// find — never a file we write.
			if err := os.MkdirAll(filepath.Join(home, a.Detect[0]), 0o755); err != nil {
				t.Fatal(err)
			}
			root := filepath.Join(home, a.Root)
			if err := os.MkdirAll(filepath.Dir(root), 0o755); err != nil {
				t.Fatal(err)
			}

			docs := []agentdoc.Doc{agentdoc.SctxDoc}
			// Exactly what the page tells a reader to do: append the wrapped block
			// to the instruction file, and for an include-capable agent write the
			// sidecar document beside it.
			block := agentdoc.Wrap(agentdoc.BlockBody(a, nil, docs, ""))
			if err := os.WriteFile(root, []byte("# My own instructions\n\n"+block), 0o644); err != nil {
				t.Fatal(err)
			}
			if a.Includes {
				side := filepath.Join(filepath.Dir(root), agentdoc.SctxDoc.Name)
				// StampedBody, because that is what the page serves. A manual
				// install must land in the SAME state as `sctx setup --install`,
				// including its provenance — otherwise every hand-configured
				// machine is permanently "unverifiable" and never receives a
				// template fix without a --force nobody knows to run.
				if err := os.WriteFile(side, []byte(agentdoc.StampedBody(agentdoc.SctxDoc.Body(nil))), 0o644); err != nil {
					t.Fatal(err)
				}
			}

			st, err := Inspect(home, nil, docs...)
			if err != nil {
				t.Fatalf("inspect: %v", err)
			}
			if !st.Complete() {
				t.Fatalf("the installer does not recognise its own published guidance: %+v", st.Targets)
			}
			for _, tg := range st.Targets {
				for _, s := range tg.Sidecars {
					if s.State != agentdoc.SidecarCurrent {
						t.Errorf("a document pasted exactly as the page describes reads as %v,\n"+
							"not current — the manual and automatic paths have diverged", s.State)
					}
				}
			}

			// And the proof of the consequence: a subsequent install must be a
			// no-op rather than a second copy.
			if _, err := Install(home, nil, docs...); err != nil {
				t.Fatalf("install: %v", err)
			}
			body, err := os.ReadFile(root)
			if err != nil {
				t.Fatal(err)
			}
			if n := strings.Count(string(body), agentdoc.BeginMarker); n != 1 {
				t.Errorf("%d managed blocks after install, want 1:\n%s", n, body)
			}
			if !strings.Contains(string(body), "# My own instructions") {
				t.Error("the developer's own content did not survive")
			}
		})
	}
}
