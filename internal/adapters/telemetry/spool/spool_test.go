package spool

import (
	"bytes"
	"context"
	"encoding/json/jsontext"
	json "encoding/json/v2"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/synapctx/sctx/internal/domain/telemetry"
)

func event(id string) telemetry.Event {
	return telemetry.Event{
		ID: id, Kind: telemetry.KindExecSavings, Tool: "sctx", Command: "go test", Tier: "aggressive", RawTokens: 100, OutTokens: 10,
		SavedTokens: 90, At: time.Now().UTC(),
	}
}

// eventForRepo is like event but sets RepositoryName, driving flush's
// per-org grouping.
func eventForRepo(id, repo string) telemetry.Event {
	ev := event(id)
	ev.RepositoryName = repo
	return ev
}

// okResolver is a TokenResolver that always allows delivery, returning the
// same token (possibly empty, e.g. an unauthenticated local topology) for
// every org — matching a single-key Emitter as used before multi-org
// telemetry.
type okResolver string

// Both test resolvers permit every purpose; purpose filtering has its own tests.
func (o okResolver) PermitsPurpose(string) bool { return true }

func (m mapResolver) PermitsPurpose(string) bool { return true }

func (o okResolver) TokenForOrg(string) (string, bool) {
	return string(o), true
}

// mapResolver is a TokenResolver keyed by org slug, for tests exercising
// per-org delivery and the "no key configured yet" retain path.
type mapResolver map[string]string

func (m mapResolver) TokenForOrg(org string) (string, bool) {
	t, ok := m[org]
	return t, ok
}

func TestEmitAndFlush(t *testing.T) {
	var received struct {
		TenantID string           `json:"tenantId"`
		Events   []jsontext.Value `json:"events"`
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		if err := json.Unmarshal(body, &received); err != nil {
			t.Errorf("bad payload: %v", err)
		}
		w.WriteHeader(http.StatusAccepted)
	}))
	defer srv.Close()

	dir := t.TempDir()
	e := NewEmitter(dir, srv.URL, okResolver(""))
	e.Emit(event("01A"))
	e.Emit(event("01B"))

	if err := e.Flush(context.Background()); err != nil {
		t.Fatalf("Flush: %v", err)
	}
	if len(received.Events) != 2 {
		t.Fatalf("received %d events, want 2", len(received.Events))
	}
	// The batch must carry NO tenantId. The server never read one - both ingest
	// handlers take the organization from the authenticated key, and the proxy's
	// batch DTO does not declare the field - while sctx sent an UPPERCASE ULID
	// the platform's lowercase-only `ulid` domain would reject. Sending a value
	// nothing consumes is how `sctx doctor` came to print a tenant this machine
	// does not have.
	if received.TenantID != "" {
		t.Errorf("batch carries tenantId=%q; it must be absent", received.TenantID)
	}
	if _, err := os.Stat(filepath.Join(dir, pendingFile)); !os.IsNotExist(err) {
		t.Fatal("spool file should be removed after a successful flush")
	}
}

func TestOfflineKeepsSpoolAndStaysFast(t *testing.T) {
	dir := t.TempDir()
	// Reserved TEST-NET-1 address: connect attempts hang until timeout.
	e := NewEmitter(dir, "http://192.0.2.1:9/v1/telemetry/exec", okResolver(""))
	e.Emit(event("01A"))

	start := time.Now()
	err := e.Flush(context.Background())
	elapsed := time.Since(start)

	if err == nil {
		t.Fatal("expected flush to fail offline")
	}
	if elapsed > time.Second {
		t.Fatalf("offline flush took %v, must stay within its deadline", elapsed)
	}
	data, readErr := os.ReadFile(filepath.Join(dir, pendingFile))
	if readErr != nil || len(data) == 0 {
		t.Fatal("spool must be retained after a failed flush")
	}
}

func TestFlushEmptySpoolIsNoop(t *testing.T) {
	e := NewEmitter(t.TempDir(), "http://192.0.2.1:9/", okResolver(""))
	if err := e.Flush(context.Background()); err != nil {
		t.Fatalf("empty spool flush should be a no-op, got %v", err)
	}
}

func TestFlushSetsAuthorizationHeaderOnlyWhenTokenSet(t *testing.T) {
	var gotAuth string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		w.WriteHeader(http.StatusAccepted)
	}))
	defer srv.Close()

	dir := t.TempDir()
	e := NewEmitter(dir, srv.URL, okResolver("sctx_live_secret"))
	e.Emit(event("01A"))
	if err := e.Flush(context.Background()); err != nil {
		t.Fatalf("Flush: %v", err)
	}
	if gotAuth != "Bearer sctx_live_secret" {
		t.Fatalf("Authorization header = %q, want Bearer sctx_live_secret", gotAuth)
	}

	dir2 := t.TempDir()
	e2 := NewEmitter(dir2, srv.URL, okResolver(""))
	e2.Emit(event("01B"))
	gotAuth = "unset"
	if err := e2.Flush(context.Background()); err != nil {
		t.Fatalf("Flush: %v", err)
	}
	if gotAuth != "" {
		t.Fatalf("Authorization header = %q, want empty when no token configured", gotAuth)
	}
}

func TestIsLoopbackEndpointSelectsBudget(t *testing.T) {
	cases := []struct {
		endpoint string
		loopback bool
	}{
		{"http://127.0.0.1:6220/v1/telemetry/exec", true},
		{"http://localhost:6220/v1/telemetry/exec", true},
		{"http://127.5.5.5:6220/v1/telemetry/exec", true},
		{"http://[::1]:6220/v1/telemetry/exec", true},
		{"https://sctx.synapctx.com/v1/telemetry/exec", false},
		{"http://10.0.0.5:6220/v1/telemetry/exec", false},
		{"not a url", false},
	}
	for _, tc := range cases {
		if got := isLoopbackEndpoint(tc.endpoint); got != tc.loopback {
			t.Errorf("isLoopbackEndpoint(%q) = %v, want %v", tc.endpoint, got, tc.loopback)
		}
	}

	loopback := NewEmitter(t.TempDir(), "http://127.0.0.1:6220/v1/telemetry/exec", okResolver(""))
	if loopback.flushTimeout != loopbackFlushTimeout {
		t.Fatalf("loopback flushTimeout = %v, want %v", loopback.flushTimeout, loopbackFlushTimeout)
	}
	remote := NewEmitter(t.TempDir(), "https://sctx.synapctx.com/v1/telemetry/exec", okResolver(""))
	if remote.flushTimeout != remoteFlushTimeout {
		t.Fatalf("remote flushTimeout = %v, want %v", remote.flushTimeout, remoteFlushTimeout)
	}
}

func TestAutoFlushThrottles(t *testing.T) {
	var requests int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests++
		w.WriteHeader(http.StatusAccepted)
	}))
	defer srv.Close()

	dir := t.TempDir()
	e := NewEmitter(dir, srv.URL, okResolver(""))

	e.Emit(event("01A"))
	if err := e.AutoFlush(context.Background()); err != nil {
		t.Fatalf("first AutoFlush: %v", err)
	}
	if requests != 1 {
		t.Fatalf("requests after first AutoFlush = %d, want 1", requests)
	}

	// Within the throttle window: a second call must not hit the network,
	// even though there is a fresh event spooled.
	e.Emit(event("01B"))
	if err := e.AutoFlush(context.Background()); err != nil {
		t.Fatalf("throttled AutoFlush: %v", err)
	}
	if requests != 1 {
		t.Fatalf("requests after throttled AutoFlush = %d, want still 1", requests)
	}

	// Backdate the marker past the throttle window: the next call must flush.
	marker := filepath.Join(dir, throttleMarkerFile)
	old := time.Now().Add(-throttleInterval - time.Second)
	if err := os.Chtimes(marker, old, old); err != nil {
		t.Fatalf("backdating marker: %v", err)
	}
	if err := e.AutoFlush(context.Background()); err != nil {
		t.Fatalf("post-window AutoFlush: %v", err)
	}
	if requests != 2 {
		t.Fatalf("requests after post-window AutoFlush = %d, want 2", requests)
	}
}

func TestServerErrorKeepsSpool(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "boom", http.StatusInternalServerError)
	}))
	defer srv.Close()

	dir := t.TempDir()
	e := NewEmitter(dir, srv.URL, okResolver(""))
	e.Emit(event("01A"))

	if err := e.Flush(context.Background()); err == nil {
		t.Fatal("expected flush to report the server error")
	}
	if _, err := os.Stat(filepath.Join(dir, pendingFile)); err != nil {
		t.Fatal("spool must survive a server-side failure")
	}
}

// TestFlushDeliversPerOrgUnderDistinctTokens proves that events for two
// different orgs are delivered in separate POSTs, each carrying that org's
// own bearer token.
func TestFlushDeliversPerOrgUnderDistinctTokens(t *testing.T) {
	authByOrg := make(map[string][]string) // bearer token -> repositoryNames seen under it
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		var payload struct {
			Events []struct {
				RepositoryName string `json:"repositoryName"`
			} `json:"events"`
		}
		if err := json.Unmarshal(body, &payload); err != nil {
			t.Errorf("bad payload: %v", err)
		}
		auth := r.Header.Get("Authorization")
		for _, ev := range payload.Events {
			authByOrg[auth] = append(authByOrg[auth], ev.RepositoryName)
		}
		w.WriteHeader(http.StatusAccepted)
	}))
	defer srv.Close()

	dir := t.TempDir()
	resolver := mapResolver{"parlitrack": "token-parlitrack", "synapctx": "token-synapctx"}
	e := NewEmitter(dir, srv.URL, resolver)
	e.Emit(eventForRepo("01A", "parlitrack/repo-a"))
	e.Emit(eventForRepo("01B", "synapctx/repo-b"))

	if err := e.Flush(context.Background()); err != nil {
		t.Fatalf("Flush: %v", err)
	}
	if got := authByOrg["Bearer token-parlitrack"]; len(got) != 1 || got[0] != "parlitrack/repo-a" {
		t.Errorf("parlitrack group = %v, want [parlitrack/repo-a] under its own token", got)
	}
	if got := authByOrg["Bearer token-synapctx"]; len(got) != 1 || got[0] != "synapctx/repo-b" {
		t.Errorf("synapctx group = %v, want [synapctx/repo-b] under its own token", got)
	}
	if _, err := os.Stat(filepath.Join(dir, pendingFile)); !os.IsNotExist(err) {
		t.Fatal("spool file should be removed once every group is delivered")
	}
}

// TestFlushRetainsOrgWithNoConfiguredKey proves an event whose org has no
// resolver entry is left in the spool (never delivered under a wrong key),
// while a sibling org with a key is delivered and removed.
func TestFlushRetainsOrgWithNoConfiguredKey(t *testing.T) {
	var delivered []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		var payload struct {
			Events []struct {
				RepositoryName string `json:"repositoryName"`
			} `json:"events"`
		}
		_ = json.Unmarshal(body, &payload)
		for _, ev := range payload.Events {
			delivered = append(delivered, ev.RepositoryName)
		}
		w.WriteHeader(http.StatusAccepted)
	}))
	defer srv.Close()

	dir := t.TempDir()
	resolver := mapResolver{"synapctx": "token-synapctx"} // no key for parlitrack
	e := NewEmitter(dir, srv.URL, resolver)
	e.Emit(eventForRepo("01A", "parlitrack/repo-a"))
	e.Emit(eventForRepo("01B", "synapctx/repo-b"))

	if err := e.Flush(context.Background()); err != nil {
		t.Fatalf("Flush: %v", err)
	}
	if len(delivered) != 1 || delivered[0] != "synapctx/repo-b" {
		t.Errorf("delivered = %v, want only synapctx/repo-b", delivered)
	}
	data, err := os.ReadFile(filepath.Join(dir, pendingFile))
	if err != nil {
		t.Fatalf("spool must be retained for the unconfigured org: %v", err)
	}
	if !bytes.Contains(data, []byte("parlitrack/repo-a")) {
		t.Errorf("retained spool = %q, want it to still contain the parlitrack event", data)
	}
	if bytes.Contains(data, []byte("synapctx/repo-b")) {
		t.Errorf("retained spool = %q, must not contain the delivered synapctx event", data)
	}
}

// TestFlushRetainsFailedOrgGroupOnly proves that when one org's POST fails
// and a sibling org's POST succeeds, only the failed group's events survive
// in the spool.
func TestFlushRetainsFailedOrgGroupOnly(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") == "Bearer token-fails" {
			http.Error(w, "boom", http.StatusInternalServerError)
			return
		}
		w.WriteHeader(http.StatusAccepted)
	}))
	defer srv.Close()

	dir := t.TempDir()
	resolver := mapResolver{"failing-org": "token-fails", "ok-org": "token-ok"}
	e := NewEmitter(dir, srv.URL, resolver)
	e.Emit(eventForRepo("01A", "failing-org/repo-a"))
	e.Emit(eventForRepo("01B", "ok-org/repo-b"))

	if err := e.Flush(context.Background()); err == nil {
		t.Fatal("expected Flush to report the failing group's error")
	}
	data, err := os.ReadFile(filepath.Join(dir, pendingFile))
	if err != nil {
		t.Fatalf("spool must survive with the failed group retained: %v", err)
	}
	if !bytes.Contains(data, []byte("failing-org/repo-a")) {
		t.Errorf("retained spool = %q, want the failed group's event", data)
	}
	if bytes.Contains(data, []byte("ok-org/repo-b")) {
		t.Errorf("retained spool = %q, must not contain the delivered event", data)
	}
}
