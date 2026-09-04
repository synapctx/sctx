package hook

import (
	"strings"
	"testing"
)

func TestMemoryContextFullMode(t *testing.T) {
	resp := forFileResponse{
		Mode: "full",
		Notes: []surfaceNote{
			{Text: "decision one", Kind: "decision", CreatedAt: "2026-01-01T00:00:00Z", VerifiedAt: "2026-01-02T00:00:00Z"},
			{Text: "lesson two", Kind: "lesson", CreatedAt: "2026-01-03T00:00:00Z", Staleness: "aging"},
			{Text: "should be dropped, cap is two", Kind: "decision", CreatedAt: "2026-01-04T00:00:00Z"},
		},
	}
	got := memoryContext("svc/handler.go", resp)
	if strings.Count(got, "•") != 2 {
		t.Fatalf("full mode must render at most 2 notes, got:\n%s", got)
	}
	if !strings.Contains(got, "[decision · 2026-01-01 · verified]") {
		t.Errorf("missing verified label, got:\n%s", got)
	}
	if !strings.Contains(got, "[lesson · 2026-01-03 · unverified · aging]") {
		t.Errorf("missing unverified/stale label, got:\n%s", got)
	}
	if strings.Contains(got, "should be dropped") {
		t.Errorf("a third note leaked past the cap of 2:\n%s", got)
	}
}

func TestMemoryContextFullModeCharCap(t *testing.T) {
	long := strings.Repeat("x", 500)
	resp := forFileResponse{Mode: "full", Notes: []surfaceNote{{Text: long, CreatedAt: "2026-01-01T00:00:00Z"}}}
	got := memoryContext("f.go", resp)
	if strings.Contains(got, strings.Repeat("x", 281)) {
		t.Errorf("a note must be truncated to 280 chars")
	}
}

func TestMemoryContextPointerMode(t *testing.T) {
	resp := forFileResponse{
		Mode: "pointer",
		Pointer: &struct {
			Count  int    `json:"count"`
			Newest string `json:"newest"`
		}{Count: 5, Newest: "2026-03-04T00:00:00Z"},
	}
	got := memoryContext("svc/handler.go", resp)
	want := "SynapCTX: 5 notes on svc/handler.go, newest 2026-03-04 — recall_memory"
	if got != want {
		t.Fatalf("pointer render = %q, want %q", got, want)
	}
}

func TestMemoryContextSilentMode(t *testing.T) {
	resp := forFileResponse{Mode: "silent", Notes: []surfaceNote{{Text: "should never render"}}}
	if got := memoryContext("f.go", resp); got != "" {
		t.Fatalf("silent mode must render nothing, got %q", got)
	}
}

func TestMemoryContextAbsentModeTreatedAsFull(t *testing.T) {
	// An old proxy that predates this feature omits "mode" entirely.
	resp := forFileResponse{Notes: []surfaceNote{{Text: "note", CreatedAt: "2026-01-01T00:00:00Z"}}}
	got := memoryContext("f.go", resp)
	if !strings.Contains(got, "note") {
		t.Fatalf("an absent mode must be treated as full, got %q", got)
	}
}

func TestSymbolEditContextRendersCallAndSkipsUnresolved(t *testing.T) {
	resp := symbolEditResponse{Symbols: []struct {
		Name              string   `json:"name"`
		SymbolPath        string   `json:"symbolPath"`
		OtherRepositories []string `json:"otherRepositories"`
		References        int      `json:"references"`
		Call              string   `json:"call"`
	}{
		{Name: "DoThing", SymbolPath: "pkg.DoThing", OtherRepositories: []string{"acme/a", "acme/b"}, References: 4},
		{Name: "NoRefsElsewhere", SymbolPath: "pkg.NoRefsElsewhere"},
	}}
	got := symbolEditContext("acme", false, resp)
	want := `SynapCTX: DoThing is used in acme/a, acme/b (4 references) — mcp__acme__find_references {"symbol_path": "pkg.DoThing"}`
	if got != want {
		t.Fatalf("symbolEditContext() = %q, want %q", got, want)
	}
}

func TestSymbolEditContextEmptyWhenNothingElsewhere(t *testing.T) {
	if got := symbolEditContext("acme", false, symbolEditResponse{}); got != "" {
		t.Fatalf("no symbols with other-repository references must render nothing, got %q", got)
	}
}

func TestRenderWorkspaceBriefTokenBound(t *testing.T) {
	var notes []struct {
		ID           string   `json:"id"`
		Kind         string   `json:"kind"`
		CreatedAt    string   `json:"createdAt"`
		Text         string   `json:"text"`
		Repositories []string `json:"repositories"`
	}
	for i := 0; i < 4; i++ {
		notes = append(notes, struct {
			ID           string   `json:"id"`
			Kind         string   `json:"kind"`
			CreatedAt    string   `json:"createdAt"`
			Text         string   `json:"text"`
			Repositories []string `json:"repositories"`
		}{Kind: "decision", CreatedAt: "2026-01-01T00:00:00Z", Text: strings.Repeat("y", 280)})
	}
	resp := forWorkspaceResponse{
		Organization: "acme",
		Notes:        notes,
	}
	for i := 0; i < 8; i++ {
		resp.Repositories = append(resp.Repositories, struct {
			Name             string `json:"name"`
			IndexingStatus   string `json:"indexingStatus"`
			PrimaryRef       string `json:"primaryRef"`
			IndexedAt        string `json:"indexedAt"`
			IndexedCommitSha string `json:"indexedCommitSha"`
		}{Name: "acme/repo", IndexingStatus: "ready", IndexedAt: "2026-01-01T00:00:00Z"})
	}
	got := renderWorkspaceBrief(resp, "acme", false)
	if len(got) > workspaceBriefMaxTokens*bytesPerToken {
		t.Fatalf("workspace brief is %d bytes, want <= %d", len(got), workspaceBriefMaxTokens*bytesPerToken)
	}
	if !strings.Contains(got, "=== SynapCTX workspace brief — org acme ===") {
		t.Errorf("missing header, got:\n%s", got)
	}
}

func TestRenderWorkspaceBriefEmptyOnEmptyResponse(t *testing.T) {
	if got := renderWorkspaceBrief(forWorkspaceResponse{}, "acme", false); got != "" {
		t.Fatalf("an empty workspace response must render nothing, got %q", got)
	}
}
