package spool

import (
	"bytes"
	"context"
	"encoding/json/jsontext"
	json "encoding/json/v2"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// writeRawSpool writes n JSONL lines directly to dir's pending file, all in
// the same org, bypassing Emit's per-line file open for speed on large
// fixtures.
func writeRawSpool(t *testing.T, dir string, n int) {
	t.Helper()
	var buf bytes.Buffer
	for i := 0; i < n; i++ {
		ev := eventForRepo(fmt.Sprintf("id-%d", i), "acme/repo")
		line, err := json.Marshal(ev)
		if err != nil {
			t.Fatalf("marshal event %d: %v", i, err)
		}
		buf.Write(line)
		buf.WriteByte('\n')
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, pendingFile), buf.Bytes(), 0o644); err != nil {
		t.Fatal(err)
	}
}

func countEventsInBody(t *testing.T, r *http.Request) int {
	t.Helper()
	body, _ := io.ReadAll(r.Body)
	var payload struct {
		Events []jsontext.Value `json:"events"`
	}
	if err := json.Unmarshal(body, &payload); err != nil {
		t.Fatalf("bad payload: %v", err)
	}
	return len(payload.Events)
}

// TestFlushChunksALargeBacklogInsteadOfOneHugeBatch proves the opportunistic
// (single-call) Flush no longer posts an entire large backlog as one
// request: it sends at most chunkMaxEvents per request and leaves the rest
// in the spool for a later attempt.
func TestFlushChunksALargeBacklogInsteadOfOneHugeBatch(t *testing.T) {
	var chunkSizes []int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		chunkSizes = append(chunkSizes, countEventsInBody(t, r))
		w.WriteHeader(http.StatusAccepted)
	}))
	defer srv.Close()

	dir := t.TempDir()
	writeRawSpool(t, dir, chunkMaxEvents+50)
	e := NewEmitter(dir, srv.URL, okResolver("tok"), "1.2.3", "test-client")

	if err := e.Flush(context.Background()); err != nil {
		t.Fatalf("Flush: %v", err)
	}
	if len(chunkSizes) != 1 {
		t.Fatalf("requests = %d, want exactly 1 (one opportunistic chunk)", len(chunkSizes))
	}
	if chunkSizes[0] != chunkMaxEvents {
		t.Errorf("chunk size = %d, want %d", chunkSizes[0], chunkMaxEvents)
	}
	if pending := CountPending(dir); pending != 50 {
		t.Errorf("pending = %d, want 50 (the remainder never sent)", pending)
	}
}

// TestFlushWithTimeoutDrainsALargeSpoolInChunks proves the looping drain
// (`sctx flush`/`sctx init`) fully empties a large backlog across several
// bounded requests, removing exactly what was acknowledged each time.
func TestFlushWithTimeoutDrainsALargeSpoolInChunks(t *testing.T) {
	const total = 5017
	var requests, events int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests++
		events += countEventsInBody(t, r)
		w.WriteHeader(http.StatusAccepted)
	}))
	defer srv.Close()

	dir := t.TempDir()
	writeRawSpool(t, dir, total)
	e := NewEmitter(dir, srv.URL, okResolver("tok"), "1.2.3", "test-client")

	res, err := e.FlushWithTimeout(context.Background(), 5*time.Second)
	if err != nil {
		t.Fatalf("FlushWithTimeout: %v", err)
	}
	wantRequests := (total + chunkMaxEvents - 1) / chunkMaxEvents
	if res.Requests != wantRequests {
		t.Errorf("Requests = %d, want %d", res.Requests, wantRequests)
	}
	if res.Sent != total {
		t.Errorf("Sent = %d, want %d", res.Sent, total)
	}
	if res.Pending != 0 {
		t.Errorf("Pending = %d, want 0", res.Pending)
	}
	if events != total {
		t.Errorf("server received %d events total, want %d", events, total)
	}
	if _, err := os.Stat(filepath.Join(dir, pendingFile)); !os.IsNotExist(err) {
		t.Error("spool file should be removed once fully drained")
	}
}

// TestFlushWithTimeoutStopsOnFailureAndKeepsTheRemainder proves that once a
// chunk's request fails outright (a 5xx here), the loop stops immediately:
// earlier successfully-acknowledged chunks stay removed (never re-sent) and
// everything from the failing chunk onward is left intact in the spool.
func TestFlushWithTimeoutStopsOnFailureAndKeepsTheRemainder(t *testing.T) {
	const total = 3 * chunkMaxEvents
	var requests int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests++
		if requests == 2 {
			http.Error(w, "boom", http.StatusInternalServerError)
			return
		}
		w.WriteHeader(http.StatusAccepted)
	}))
	defer srv.Close()

	dir := t.TempDir()
	writeRawSpool(t, dir, total)
	e := NewEmitter(dir, srv.URL, okResolver("tok"), "1.2.3", "test-client")

	res, err := e.FlushWithTimeout(context.Background(), 5*time.Second)
	if err == nil {
		t.Fatal("expected the failing chunk's error to surface")
	}
	if res.Sent != chunkMaxEvents {
		t.Errorf("Sent = %d, want %d (only the first chunk acknowledged before the failure)", res.Sent, chunkMaxEvents)
	}
	if res.Requests != 2 {
		t.Errorf("Requests = %d, want 2 (stopped at the failing one)", res.Requests)
	}
	if res.Pending != total-chunkMaxEvents {
		t.Errorf("Pending = %d, want %d (the failed chunk plus everything after it)", res.Pending, total-chunkMaxEvents)
	}

	// A second, successful drain must never re-request the already-acked
	// first chunk's worth of events — only the remainder.
	requests = 0
	var secondEvents int
	srv2 := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		secondEvents += countEventsInBody(t, r)
		w.WriteHeader(http.StatusAccepted)
	}))
	defer srv2.Close()
	e2 := NewEmitter(dir, srv2.URL, okResolver("tok"), "1.2.3", "test-client")
	res2, err := e2.FlushWithTimeout(context.Background(), 5*time.Second)
	if err != nil {
		t.Fatalf("second FlushWithTimeout: %v", err)
	}
	if secondEvents != total-chunkMaxEvents {
		t.Errorf("second drain sent %d events, want %d (the remainder only)", secondEvents, total-chunkMaxEvents)
	}
	if res2.Pending != 0 {
		t.Errorf("Pending after second drain = %d, want 0", res2.Pending)
	}
}

// TestFlushQuarantinesAChunkAfterThreeConsecutive4xx proves a permanently
// rejected chunk (e.g. malformed lines from an ancient sctx version) does
// not wedge the spool forever: after 3 consecutive 4xx responses for the
// same head chunk, it is moved to rejected.jsonl and draining continues.
func TestFlushQuarantinesAChunkAfterThreeConsecutive4xx(t *testing.T) {
	var requests int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests++
		http.Error(w, "bad request", http.StatusBadRequest)
	}))
	defer srv.Close()

	dir := t.TempDir()
	writeRawSpool(t, dir, 3)
	e := NewEmitter(dir, srv.URL, okResolver("tok"), "1.2.3", "test-client")

	// First two attempts: rejected but not yet quarantined, spool retained.
	for i := 0; i < 2; i++ {
		if err := e.Flush(context.Background()); err == nil {
			t.Fatalf("attempt %d: expected the 4xx to surface as an error", i)
		}
		if pending := CountPending(dir); pending != 3 {
			t.Fatalf("attempt %d: pending = %d, want 3 (not yet quarantined)", i, pending)
		}
	}

	// Third consecutive 4xx: quarantined, spool cleared, no error blocking
	// forever.
	if err := e.Flush(context.Background()); err != nil {
		t.Fatalf("quarantining attempt: %v", err)
	}
	if pending := CountPending(dir); pending != 0 {
		t.Errorf("pending after quarantine = %d, want 0", pending)
	}
	if requests != 3 {
		t.Errorf("requests = %d, want 3", requests)
	}

	rejected, err := os.ReadFile(filepath.Join(dir, rejectedFile))
	if err != nil {
		t.Fatalf("reading rejected.jsonl: %v", err)
	}
	if got := strings.Count(strings.TrimSpace(string(rejected)), "\n") + 1; got != 3 {
		t.Errorf("rejected.jsonl has %d lines, want 3", got)
	}

	// The reject-attempts counter must not linger once quarantined.
	if _, err := os.Stat(filepath.Join(dir, rejectAttemptsFile)); !os.IsNotExist(err) {
		t.Error("reject-attempts state should be cleared once quarantined")
	}
}

// TestHeadChunkIndicesAlwaysMakesProgress proves a single line larger than
// chunkMaxBytes is still sent alone rather than being wedged forever behind
// its own size cap.
func TestHeadChunkIndicesAlwaysMakesProgress(t *testing.T) {
	huge := make([]byte, chunkMaxBytes+1024)
	for i := range huge {
		huge[i] = 'a'
	}
	lines := [][]byte{huge, []byte("small")}
	got := headChunkIndices(lines, []int{0, 1})
	if len(got) != 1 || got[0] != 0 {
		t.Fatalf("headChunkIndices = %v, want [0] (the oversized line alone)", got)
	}
}
