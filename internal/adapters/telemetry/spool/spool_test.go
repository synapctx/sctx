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
	"github.com/synapctx/sctx/internal/platform/httpclient"
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

// Neither test resolver models a default org unless wrapped by
// defaultOrgResolver below; the plain forms exercise the pre-existing
// "no key, no default" retain behaviour.
func (o okResolver) DefaultOrgSlug() (string, bool)  { return "", false }
func (m mapResolver) DefaultOrgSlug() (string, bool) { return "", false }

// mapResolver is a TokenResolver keyed by org slug, for tests exercising
// per-org delivery and the "no key configured yet" retain path.
type mapResolver map[string]string

func (m mapResolver) TokenForOrg(org string) (string, bool) {
	t, ok := m[org]
	return t, ok
}

// defaultOrgResolver wraps mapResolver with a configured default org, for
// tests exercising re-attribution of a keyless org's events to it.
type defaultOrgResolver struct {
	mapResolver
	defaultOrg string
}

func (d defaultOrgResolver) DefaultOrgSlug() (string, bool) {
	return d.defaultOrg, d.defaultOrg != ""
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
	e := NewEmitter(dir, srv.URL, okResolver(""), "1.2.3", "test-client")
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
	e := NewEmitter(dir, "http://192.0.2.1:9/v1/telemetry/exec", okResolver(""), "1.2.3", "test-client")
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
	e := NewEmitter(t.TempDir(), "http://192.0.2.1:9/", okResolver(""), "1.2.3", "test-client")
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
	e := NewEmitter(dir, srv.URL, okResolver("sctx_live_secret"), "1.2.3", "test-client")
	e.Emit(event("01A"))
	if err := e.Flush(context.Background()); err != nil {
		t.Fatalf("Flush: %v", err)
	}
	if gotAuth != "Bearer sctx_live_secret" {
		t.Fatalf("Authorization header = %q, want Bearer sctx_live_secret", gotAuth)
	}

	dir2 := t.TempDir()
	e2 := NewEmitter(dir2, srv.URL, okResolver(""), "1.2.3", "test-client")
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

	loopback := NewEmitter(t.TempDir(), "http://127.0.0.1:6220/v1/telemetry/exec", okResolver(""), "1.2.3", "test-client")
	if loopback.flushTimeout != loopbackFlushTimeout {
		t.Fatalf("loopback flushTimeout = %v, want %v", loopback.flushTimeout, loopbackFlushTimeout)
	}
	remote := NewEmitter(t.TempDir(), "https://sctx.synapctx.com/v1/telemetry/exec", okResolver(""), "1.2.3", "test-client")
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
	e := NewEmitter(dir, srv.URL, okResolver(""), "1.2.3", "test-client")

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
	e := NewEmitter(dir, srv.URL, okResolver(""), "1.2.3", "test-client")
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
	e := NewEmitter(dir, srv.URL, resolver, "1.2.3", "test-client")
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
	e := NewEmitter(dir, srv.URL, resolver, "1.2.3", "test-client")
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
	e := NewEmitter(dir, srv.URL, resolver, "1.2.3", "test-client")
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

// TestFlushSendsSctxUserAgent asserts the batch POST identifies itself as
// sctx traffic rather than the default Go-http-client — see
// httpclient.UserAgent.
func TestFlushSendsSctxUserAgent(t *testing.T) {
	var gotUA string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotUA = r.Header.Get("User-Agent")
		w.WriteHeader(http.StatusAccepted)
	}))
	defer srv.Close()

	dir := t.TempDir()
	e := NewEmitter(dir, srv.URL, okResolver(""), "9.9.9", "claude-code")
	e.Emit(event("01A"))
	if err := e.Flush(context.Background()); err != nil {
		t.Fatalf("Flush: %v", err)
	}
	want := httpclient.UserAgent("9.9.9", "claude-code")
	if gotUA != want {
		t.Errorf("User-Agent = %q, want %q", gotUA, want)
	}
}

// TestFlushReattributesKeylessOrgToDefault proves org-isolation rule 0009:
// a named org with no key of its own is delivered under the DEFAULT org's
// token, with repositoryName CLEARED — never under a sibling org's key with
// its real repository name attached — while a sibling org that DOES have its
// own key is sent untouched, under its own token, with its repositoryName
// intact.
func TestFlushReattributesKeylessOrgToDefault(t *testing.T) {
	type received struct {
		auth string
		repo string
	}
	var got []received
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		var payload struct {
			Events []struct {
				RepositoryName string `json:"repositoryName"`
			} `json:"events"`
		}
		_ = json.Unmarshal(body, &payload)
		auth := r.Header.Get("Authorization")
		for _, ev := range payload.Events {
			got = append(got, received{auth: auth, repo: ev.RepositoryName})
		}
		w.WriteHeader(http.StatusAccepted)
	}))
	defer srv.Close()

	dir := t.TempDir()
	resolver := defaultOrgResolver{
		mapResolver: mapResolver{"synapctx": "token-synapctx"}, // no key for podsteer
		defaultOrg:  "synapctx",
	}
	e := NewEmitter(dir, srv.URL, resolver, "1.2.3", "test-client")
	e.Emit(eventForRepo("01A", "podsteer/repo-a")) // no key of its own
	e.Emit(eventForRepo("01B", "synapctx/repo-b")) // has its own key

	res, err := e.FlushWithTimeout(context.Background(), 5*time.Second)
	if err != nil {
		t.Fatalf("FlushWithTimeout: %v", err)
	}
	if res.Reattributed != 1 || res.ReattributedTo != "synapctx" {
		t.Errorf("Reattributed = %d, ReattributedTo = %q, want 1, \"synapctx\"", res.Reattributed, res.ReattributedTo)
	}
	if len(res.NoKeyEvents) != 0 {
		t.Errorf("NoKeyEvents = %v, want none: the default org had a key", res.NoKeyEvents)
	}
	if len(got) != 2 {
		t.Fatalf("delivered %d events, want 2: %+v", len(got), got)
	}
	for _, r := range got {
		if r.auth != "Bearer token-synapctx" {
			t.Errorf("event repo=%q delivered under %q, want the synapctx token for BOTH (own key + reattribution)", r.repo, r.auth)
		}
		switch r.repo {
		case "":
			// The re-attributed podsteer event: repositoryName must be
			// cleared so synapctx's key never learns podsteer's repo name.
		case "synapctx/repo-b":
			// Untouched: synapctx delivering its own event under its own key.
		default:
			t.Errorf("unexpected repositoryName %q reached the server: podsteer's real name must never leave the machine", r.repo)
		}
	}
	if _, err := os.Stat(filepath.Join(dir, pendingFile)); !os.IsNotExist(err) {
		t.Fatal("spool file should be removed once every event is delivered (directly or reattributed)")
	}
}

// TestFlushWithNoDefaultOrgLeavesKeylessEventsPending proves that without a
// default org configured at all, a keyless org's events are neither
// delivered nor lost: they stay pending, and FlushResult.NoKeyEvents reports
// them (the source for `sctx flush`'s and `sctx doctor`'s message) rather
// than the flush looking like a silent, unexplained no-op.
func TestFlushWithNoDefaultOrgLeavesKeylessEventsPending(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Fatal("no request should ever be sent: there is no deliverable key")
	}))
	defer srv.Close()

	dir := t.TempDir()
	resolver := mapResolver{} // no keys, no default org (DefaultOrgSlug ⇒ false)
	e := NewEmitter(dir, srv.URL, resolver, "1.2.3", "test-client")
	e.Emit(eventForRepo("01A", "podsteer/repo-a"))
	e.Emit(eventForRepo("01B", "k8sense/repo-b"))

	res, err := e.FlushWithTimeout(context.Background(), 5*time.Second)
	if err != nil {
		t.Fatalf("FlushWithTimeout: %v", err)
	}
	if res.Sent != 0 || res.Reattributed != 0 {
		t.Errorf("Sent = %d, Reattributed = %d, want 0, 0: nothing is deliverable", res.Sent, res.Reattributed)
	}
	if res.NoKeyEvents["podsteer"] != 1 || res.NoKeyEvents["k8sense"] != 1 {
		t.Errorf("NoKeyEvents = %v, want podsteer:1, k8sense:1", res.NoKeyEvents)
	}
	data, err := os.ReadFile(filepath.Join(dir, pendingFile))
	if err != nil {
		t.Fatalf("spool must retain undeliverable events: %v", err)
	}
	if !bytes.Contains(data, []byte("podsteer/repo-a")) || !bytes.Contains(data, []byte("k8sense/repo-b")) {
		t.Errorf("retained spool = %q, want both events untouched (never delivered under a wrong key)", data)
	}
}

// TestFlushPurposeGateAppliesBeforeReattribution proves that an
// improvement-purpose event without consent is dropped, never reattributed
// and delivered to the default org — the purpose gate runs before grouping,
// so re-attribution never becomes a bypass for consent.
func TestFlushPurposeGateAppliesBeforeReattribution(t *testing.T) {
	var delivered int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		var payload struct {
			Events []jsontext.Value `json:"events"`
		}
		_ = json.Unmarshal(body, &payload)
		delivered += len(payload.Events)
		w.WriteHeader(http.StatusAccepted)
	}))
	defer srv.Close()

	dir := t.TempDir()
	lines := `{"id":"a","kind":"coverage_gap","program":"go build","repositoryName":"podsteer/repo-a"}
`
	if err := os.WriteFile(filepath.Join(dir, pendingFile), []byte(lines), 0o600); err != nil {
		t.Fatal(err)
	}
	// Permits nothing: matches a customer who declined improvement telemetry
	// (coverage_gap's purpose), on a machine that DOES have a default org.
	resolver := defaultOrgResolver{mapResolver: mapResolver{"synapctx": "tok"}, defaultOrg: "synapctx"}
	e := NewEmitter(dir, srv.URL, permitsNothing{resolver}, "1.2.3", "test-client")

	if _, err := e.FlushWithTimeout(context.Background(), 5*time.Second); err != nil {
		t.Fatalf("FlushWithTimeout: %v", err)
	}
	if delivered != 0 {
		t.Errorf("delivered %d events, want 0: an unconsented event must never be reattributed and sent", delivered)
	}
	if _, err := os.Stat(filepath.Join(dir, pendingFile)); !os.IsNotExist(err) {
		t.Error("an unauthorised-purpose event must be dropped, not retained, per the purpose gate's own contract")
	}
}

// permitsNothing wraps a TokenResolver and refuses every purpose, isolating
// the purpose gate from the token/reattribution logic under test.
type permitsNothing struct{ TokenResolver }

func (permitsNothing) PermitsPurpose(string) bool { return false }
