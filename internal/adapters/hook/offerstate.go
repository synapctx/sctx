package hook

import (
	json "encoding/json/v2"
	"os"
	"path/filepath"
	"time"
)

// Proactive guidance v2 (2026-09-04): the client half of decision D1 in the
// shared plan — novelty state is split between this file's per-session
// debounce and the proxy's own cross-session history, and the proxy stays
// stateless per request.
//
// fileOfferDebounce is the local guard: a 60s window in which the SAME file
// is never asked about twice over the network, no matter what the proxy
// would have said. It closes the window ES refresh lag leaves open — two
// edits seconds apart both offering the same notes — at zero network cost,
// and it is the reason 341 of the measured 409 same-file repeats within an
// hour were within 5 minutes (see guidance-evidence.md): most of that burst
// never needs to leave this machine.
const fileOfferDebounce = 60 * time.Second

// symbolAskWindow bounds how often the blast-radius nudge (exported.go) asks
// about the SAME changed symbol name in one session — an agent that edits the
// same exported declaration several times in a row does not need to be told
// about its call sites again ten seconds later.
const symbolAskWindow = 10 * time.Minute

// fileOffer is what this hook remembers about ONE file, for one session.
type fileOffer struct {
	// LastOfferedAt gates the network call itself (fileOfferDebounce).
	LastOfferedAt time.Time `json:"lastOfferedAt"`
	// NewestStamp is the newest memory createdAt/verifiedAt this session has
	// already been SHOWN for this file — sent back as sessionState.newestStamp
	// so the proxy can tell "nothing new" from "first time asking".
	NewestStamp time.Time `json:"newestStamp"`
	// PointerShown records whether the one-line pointer has already fired for
	// this file this session (wire doc: "pointer once per session per file").
	PointerShown bool `json:"pointerShown"`
}

// offerState is the whole per-session record this hook persists, in the same
// directory and with the same load-modify-save shape as repeatNudgeState
// (repeatnudge.go) — one developer, one agent, no locking, a lost update
// costs at most one extra offer.
type offerState struct {
	Files map[string]fileOffer `json:"files,omitempty"`
	// FullOffers is session-wide, across every file, and is reported back as
	// sessionState.fullOffers so the proxy can enforce its own 12/session cap
	// without a second round trip.
	FullOffers int `json:"fullOffers"`
	// Symbols is the blast-radius equivalent of Files: the last time each
	// changed exported symbol name was asked about, so exported.go and the
	// for-symbol "edit" mode call can both honour the 10-minute skip.
	Symbols map[string]time.Time `json:"symbols,omitempty"`
}

// offerStatePath mirrors repeatNudgeStatePath: a SEPARATE file
// (`sessions/<id>.offers`) from both the first-search counter and the
// repeat-run state, because all three have unrelated lifecycles and shapes
// and sharing one file would make one feature's read-modify-write race
// another's.
func offerStatePath(id string) string {
	spool := spoolDir()
	if spool == "" {
		return ""
	}
	return filepath.Join(spool, "sessions", id+".offers")
}

func loadOfferState(id string) offerState {
	st := offerState{}
	path := offerStatePath(id)
	if path == "" {
		return st
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return st
	}
	_ = json.Unmarshal(data, &st)
	return st
}

// saveOfferState is best-effort: a failed write costs at most one extra
// network round trip later, never a wrong answer now.
func saveOfferState(id string, st offerState) {
	path := offerStatePath(id)
	if path == "" {
		return
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return
	}
	data, err := json.Marshal(st)
	if err != nil {
		return
	}
	_ = os.WriteFile(path, data, 0o644)
}

// debounced reports whether rel was offered less than fileOfferDebounce ago
// this session — the 60s client debounce the wire doc requires: "skip the
// network entirely when the same file was offered <60s ago this session".
func (st offerState) debounced(rel string) bool {
	f, ok := st.Files[rel]
	if !ok {
		return false
	}
	return !f.LastOfferedAt.IsZero() && time.Since(f.LastOfferedAt) < fileOfferDebounce
}

// sessionStateFor renders the wire-shape sessionState object for rel, sent
// verbatim on the for-file request.
func (st offerState) sessionStateFor(rel string) sessionStateWire {
	f := st.Files[rel]
	var w sessionStateWire
	if !f.LastOfferedAt.IsZero() {
		w.LastOfferedAt = f.LastOfferedAt.UTC().Format(time.RFC3339)
	}
	if !f.NewestStamp.IsZero() {
		w.NewestStamp = f.NewestStamp.UTC().Format(time.RFC3339)
	}
	w.FullOffers = st.FullOffers
	w.PointerShown = f.PointerShown
	return w
}

// noteFileOffer records that rel was just offered, and how, so the next call
// this session reports the right sessionState.
func (st *offerState) noteFileOffer(rel string, resp forFileResponse, now time.Time) {
	if st.Files == nil {
		st.Files = map[string]fileOffer{}
	}
	f := st.Files[rel]
	f.LastOfferedAt = now

	mode := resp.Mode
	if mode == "" {
		mode = "full"
	}
	switch mode {
	case "full":
		st.FullOffers++
		if ts := newestNoteStamp(resp.Notes); !ts.IsZero() && ts.After(f.NewestStamp) {
			f.NewestStamp = ts
		}
	case "pointer":
		f.PointerShown = true
		if resp.Pointer != nil {
			if ts, err := time.Parse(time.RFC3339, resp.Pointer.Newest); err == nil && ts.After(f.NewestStamp) {
				f.NewestStamp = ts
			}
		}
	}
	st.Files[rel] = f
}

// newestNoteStamp is max(createdAt, verifiedAt) across notes actually shown —
// exactly what the wire doc defines sessionState.newestStamp to mean.
func newestNoteStamp(notes []surfaceNote) time.Time {
	var newest time.Time
	for _, n := range notes {
		for _, raw := range [2]string{n.CreatedAt, n.VerifiedAt} {
			if raw == "" {
				continue
			}
			if t, err := time.Parse(time.RFC3339, raw); err == nil && t.After(newest) {
				newest = t
			}
		}
	}
	return newest
}

// symbolRecentlyAsked reports whether name was asked about within
// symbolAskWindow this session — the blast-radius nudge's own debounce.
func (st offerState) symbolRecentlyAsked(name string) bool {
	t, ok := st.Symbols[name]
	return ok && time.Since(t) < symbolAskWindow
}

// markSymbolAsked records that name was just asked about.
func (st *offerState) markSymbolAsked(name string) {
	if st.Symbols == nil {
		st.Symbols = map[string]time.Time{}
	}
	st.Symbols[name] = time.Now()
}
