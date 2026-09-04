package spool

import (
	"context"
	"encoding/json/jsontext"
	json "encoding/json/v2"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/synapctx/sctx/internal/domain/telemetry"
)

// serviceOnlyResolver permits the customer's own report and refuses the half we
// aggregate — the state of any customer who declined.
type serviceOnlyResolver struct{}

func (serviceOnlyResolver) TokenForOrg(string) (string, bool) { return "tok", true }
func (serviceOnlyResolver) PermitsPurpose(p string) bool      { return p == telemetry.PurposeService }

// THE property the split exists for, at the boundary where it is enforced. A
// customer who declined keeps their savings report — that is their data about
// the product they bought — and loses only what we would aggregate.
func TestFlushSendsServiceDataAndDropsUnconsentedImprovementData(t *testing.T) {
	var got []map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Events []map[string]any `json:"events"`
		}
		_ = json.UnmarshalDecode(jsontext.NewDecoder(r.Body), &body)
		got = append(got, body.Events...)
		w.Write([]byte(`{"accepted":1}`))
	}))
	defer srv.Close()

	dir := t.TempDir()
	lines := `{"id":"a","kind":"exec_savings","program":"go test","repositoryName":"acme/api"}
{"id":"b","kind":"coverage_gap","program":"cargo build","repositoryName":"acme/api"}
`
	if err := os.WriteFile(filepath.Join(dir, "pending.jsonl"), []byte(lines), 0o600); err != nil {
		t.Fatal(err)
	}

	e := NewEmitter(dir, srv.URL, serviceOnlyResolver{}, "1.2.3", "test-client")
	if _, err := e.FlushWithTimeout(context.Background(), 5*time.Second); err != nil {
		t.Fatalf("flush: %v", err)
	}

	if len(got) != 1 {
		t.Fatalf("sent %d events, want 1: %+v", len(got), got)
	}
	if got[0]["kind"] != "exec_savings" {
		t.Errorf("sent %v, want the customer's own savings report", got[0]["kind"])
	}

	// And the refused event must not be RETAINED either: holding it against the
	// chance they later say yes is keeping data they declined.
	rest, err := os.ReadFile(filepath.Join(dir, "pending.jsonl"))
	if err == nil && len(rest) > 0 {
		var ev map[string]any
		_ = json.Unmarshal(rest, &ev)
		if ev["kind"] == "coverage_gap" {
			t.Errorf("a declined event was retained for a later flush:\n%s", rest)
		}
	}
}
