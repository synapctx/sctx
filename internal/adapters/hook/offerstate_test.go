package hook

import (
	"testing"
	"time"
)

func TestOfferStateDebounce(t *testing.T) {
	t.Setenv("SCT__SPOOL_DIR", t.TempDir())

	var st offerState
	if st.debounced("a/b.go") {
		t.Fatal("empty state must never debounce")
	}

	st.noteFileOffer("a/b.go", forFileResponse{Mode: "full"}, time.Now())
	if !st.debounced("a/b.go") {
		t.Fatal("a file offered just now must be debounced")
	}
	if st.debounced("other.go") {
		t.Fatal("debounce must be per file")
	}

	// An offer 61s in the past no longer debounces.
	st.Files["a/b.go"] = fileOffer{LastOfferedAt: time.Now().Add(-61 * time.Second)}
	if st.debounced("a/b.go") {
		t.Fatal("an offer older than the 60s window must not debounce")
	}
}

func TestOfferStateFullOffersCap(t *testing.T) {
	var st offerState
	for i := 0; i < 3; i++ {
		st.noteFileOffer("f.go", forFileResponse{Mode: "full"}, time.Now())
	}
	if st.FullOffers != 3 {
		t.Fatalf("FullOffers = %d, want 3", st.FullOffers)
	}
	// A pointer response never increments the full-offer counter.
	st.noteFileOffer("g.go", forFileResponse{Mode: "pointer", Pointer: &struct {
		Count  int    `json:"count"`
		Newest string `json:"newest"`
	}{Count: 2, Newest: "2026-01-01T00:00:00Z"}}, time.Now())
	if st.FullOffers != 3 {
		t.Fatalf("FullOffers after a pointer response = %d, want unchanged 3", st.FullOffers)
	}
	if !st.Files["g.go"].PointerShown {
		t.Fatal("pointer response must record PointerShown")
	}
}

func TestOfferStateNewestStampTracksMax(t *testing.T) {
	var st offerState
	resp := forFileResponse{
		Mode: "full",
		Notes: []surfaceNote{
			{Text: "a", CreatedAt: "2026-01-01T00:00:00Z"},
			{Text: "b", CreatedAt: "2026-03-01T00:00:00Z", VerifiedAt: "2026-04-01T00:00:00Z"},
		},
	}
	st.noteFileOffer("f.go", resp, time.Now())
	want, _ := time.Parse(time.RFC3339, "2026-04-01T00:00:00Z")
	if !st.Files["f.go"].NewestStamp.Equal(want) {
		t.Fatalf("NewestStamp = %v, want %v", st.Files["f.go"].NewestStamp, want)
	}
}

func TestOfferStateSymbolAskWindow(t *testing.T) {
	var st offerState
	if st.symbolRecentlyAsked("Foo") {
		t.Fatal("a symbol never asked about must not be recently asked")
	}
	st.markSymbolAsked("Foo")
	if !st.symbolRecentlyAsked("Foo") {
		t.Fatal("a symbol just asked about must be recently asked")
	}
	st.Symbols["Foo"] = time.Now().Add(-11 * time.Minute)
	if st.symbolRecentlyAsked("Foo") {
		t.Fatal("a symbol asked about 11 minutes ago must have fallen outside the 10-minute window")
	}
}

func TestOfferStatePersistsAcrossLoadSave(t *testing.T) {
	t.Setenv("SCT__SPOOL_DIR", t.TempDir())

	st := loadOfferState("sess-1")
	st.noteFileOffer("main.go", forFileResponse{Mode: "full", Notes: []surfaceNote{{Text: "x", CreatedAt: "2026-02-02T00:00:00Z"}}}, time.Now())
	st.markSymbolAsked("Handler")
	saveOfferState("sess-1", st)

	reloaded := loadOfferState("sess-1")
	if reloaded.FullOffers != 1 {
		t.Fatalf("FullOffers after reload = %d, want 1", reloaded.FullOffers)
	}
	if !reloaded.debounced("main.go") {
		t.Fatal("a reloaded state must still debounce a just-offered file")
	}
	if !reloaded.symbolRecentlyAsked("Handler") {
		t.Fatal("a reloaded state must still remember a recently asked symbol")
	}

	// A different session id must not see the first session's state.
	other := loadOfferState("sess-2")
	if other.debounced("main.go") {
		t.Fatal("session state must not leak across sessions")
	}
}
