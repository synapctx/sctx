// Package spool implements offline-safe telemetry: Emit appends one JSONL
// line to a local spool file and returns immediately; Flush opportunistically
// POSTs the spooled batch to the developer-mcp-proxy under a strict deadline.
// The platform being down costs nothing but a stale spool.
package spool

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json/jsontext"
	json "encoding/json/v2"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/synapctx/sctx/internal/domain/telemetry"
	"github.com/synapctx/sctx/internal/platform/httpclient"
)

const (
	pendingFile = "pending.jsonl"
	// throttleMarkerFile records the last opportunistic (post-command) flush
	// attempt so it can be rate-limited independently of explicit flushes
	// (`sctx flush`, `sctx init`), which always run regardless of throttle.
	throttleMarkerFile = ".last-auto-flush"
	throttleInterval   = 60 * time.Second

	// Loopback budgets: today's numbers, tuned for a plain-HTTP local proxy.
	loopbackConnectTimeout = 100 * time.Millisecond
	loopbackFlushTimeout   = 300 * time.Millisecond
	// Remote budgets: generous enough for a TLS handshake to a real host.
	remoteConnectTimeout = 500 * time.Millisecond
	remoteFlushTimeout   = 2 * time.Second

	// maxSpoolBytes caps the spool so a permanently offline machine cannot
	// grow it unboundedly; the spool is reset when the cap is exceeded.
	maxSpoolBytes = 4 << 20

	// chunkMaxEvents and chunkMaxBytes bound a single POST's batch. Before
	// this, one flush POSTed an org's ENTIRE backlog as one request under a
	// short deadline: once the backlog grew large enough that sending it
	// exceeded the deadline, the client always timed out (net/http has
	// already written the request body to the wire by then), so the server
	// routinely ingested and accepted the batch while the client, having
	// seen only a context-deadline error, retained every line and resent it
	// next time — the spool never shrank and the server received the same
	// events again and again. Chunking bounds each request to a size that
	// reliably completes inside its own deadline, and a chunk is removed
	// from the spool ONLY after its own request is acknowledged 2xx, so a
	// slow or failing request can never lose or duplicate-drop events.
	chunkMaxEvents = 200
	chunkMaxBytes  = 256 << 10

	// maxConsecutiveChunkRejects bounds how many times the exact same head
	// chunk may be retried after a 4xx before it is quarantined. A 4xx is a
	// PERMANENT rejection (e.g. a malformed line written by an ancient sctx
	// version) — retrying it forever would wedge every event behind it.
	maxConsecutiveChunkRejects = 3

	// rejectedFile holds chunks quarantined after maxConsecutiveChunkRejects
	// consecutive 4xx responses: never resent, never silently discarded —
	// kept as JSONL for a human to inspect.
	rejectedFile = "rejected.jsonl"

	// rejectAttemptsFile persists the 4xx retry count per chunk fingerprint
	// (see chunkFingerprint) across separate flush calls, since a chunk's
	// position in the spool can survive several flush attempts before
	// either succeeding or being quarantined.
	rejectAttemptsFile = ".reject-attempts.json"
)

// TokenResolver maps an org slug to the API key that should deliver its
// events, and says which collection purposes are currently authorised.
// config.Config satisfies it.
type TokenResolver interface {
	TokenForOrg(org string) (token string, ok bool)
	// PermitsPurpose reports whether events collected for this purpose may be
	// delivered NOW — asked at flush, not at collection, because authorisation
	// changes underneath a spool: consent can be withdrawn, and a disclosure
	// bump invalidates a decision made about a smaller payload.
	PermitsPurpose(purpose string) bool
}

type Emitter struct {
	dir          string
	endpoint     string
	resolver     TokenResolver
	flushTimeout time.Duration
	client       *http.Client
	userAgent    string
}

// NewEmitter builds an Emitter for endpoint, selecting connect/flush budgets
// by whether endpoint's host is loopback (fast, plain-HTTP local proxy) or
// remote (slower, allows time for a TLS handshake). resolver picks the API
// key (if any) that should deliver each event, keyed by the org slug of its
// repositoryName. version and client identify the sctx binary and the coding
// agent driving it (internal/platform/agentenv) and are rendered into the
// User-Agent header sent with every batch POST.
func NewEmitter(dir, endpoint string, resolver TokenResolver, version, client string) *Emitter {
	connectTimeout, flushTimeout := loopbackConnectTimeout, loopbackFlushTimeout
	if !isLoopbackEndpoint(endpoint) {
		connectTimeout, flushTimeout = remoteConnectTimeout, remoteFlushTimeout
	}
	return &Emitter{
		dir:          dir,
		endpoint:     endpoint,
		resolver:     resolver,
		flushTimeout: flushTimeout,
		userAgent:    httpclient.UserAgent(version, client),
		// No client.Timeout: the deadline is enforced per-call via the
		// context passed into Flush/FlushWithTimeout, so FlushWithTimeout
		// can grant a longer budget than the default flushTimeout without
		// re-dialing a new client.
		client: &http.Client{
			Transport: &http.Transport{
				DialContext: (&net.Dialer{Timeout: connectTimeout}).DialContext,
			},
		},
	}
}

// isLoopbackEndpoint reports whether endpoint's host is 127.0.0.1/localhost
// (::1 included), i.e. the default local developer-mcp-proxy topology.
func isLoopbackEndpoint(endpoint string) bool {
	u, err := url.Parse(endpoint)
	if err != nil {
		return false
	}
	host := u.Hostname()
	return host == "localhost" || host == "127.0.0.1" || host == "::1" || strings.HasPrefix(host, "127.")
}

// Emit appends the event to the local spool. Errors are swallowed by design:
// telemetry can never affect a wrapped command.
func (e *Emitter) Emit(ev telemetry.Event) {
	_ = Append(e.dir, ev)
}

// Append appends ev as one JSONL line to dir's pending spool file, creating
// dir if it doesn't exist yet. It is exported so other fail-open telemetry
// paths (e.g. the Claude Code hook's fallback delegation) can reuse the
// exact same encoding instead of duplicating it.
func Append(dir string, ev telemetry.Event) error {
	line, err := json.Marshal(ev)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	path := filepath.Join(dir, pendingFile)
	if info, err := os.Stat(path); err == nil && info.Size() > maxSpoolBytes {
		_ = os.Remove(path)
	}
	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return err
	}
	defer f.Close()
	_, err = f.Write(append(line, '\n'))
	return err
}

// CountPending returns the number of undrained events currently sitting in
// dir's spool file. Reporting-only (e.g. `sctx init`'s backlog-drain
// message); any read error counts as zero rather than failing the caller.
func CountPending(dir string) int {
	data, err := os.ReadFile(filepath.Join(dir, pendingFile))
	if err != nil {
		return 0
	}
	n := 0
	for line := range bytes.SplitSeq(data, []byte{'\n'}) {
		if len(bytes.TrimSpace(line)) > 0 {
			n++
		}
	}
	return n
}

// FlushResult reports what one drain accomplished, for `sctx flush`'s
// "flushed N events in M requests; K pending" line.
type FlushResult struct {
	Sent        int // events successfully delivered and removed from the spool
	Requests    int // HTTP requests attempted
	Quarantined int // events moved to rejectedFile after repeated 4xx
	Pending     int // events remaining in the spool afterwards
}

// Flush drains AT MOST ONE CHUNK per org group, each under the Emitter's
// default per-request budget (loopback: 300ms; remote: 2s). It is the
// opportunistic post-command path (via AutoFlush): it must never block on a
// large backlog, so it never loops — a big spool drains gradually, one
// chunk per wrapped command, rather than in one blocking call. On any
// failure the unsent remainder is left intact for a later attempt.
func (e *Emitter) Flush(ctx context.Context) error {
	_, err := e.flushOnce(ctx, e.flushTimeout)
	return err
}

// FlushWithTimeout drains the ENTIRE spool, looping chunk by chunk — each
// chunk gets its own timeout deadline, not one deadline shared across the
// whole backlog — until the spool is empty or a chunk attempt fails outright
// (a network error or a non-2xx/non-4xx response). Used by `sctx flush` and
// `sctx init`, never by the opportunistic post-command path.
func (e *Emitter) FlushWithTimeout(ctx context.Context, timeout time.Duration) (FlushResult, error) {
	var total FlushResult
	for {
		res, err := e.flushOnce(ctx, timeout)
		total.Sent += res.Sent
		total.Requests += res.Requests
		total.Quarantined += res.Quarantined
		total.Pending = res.Pending
		if err != nil {
			return total, err
		}
		if res.Requests == 0 {
			// No group made any progress this round: either the spool is
			// empty, or everything left has no configured key yet. Looping
			// again would spin forever on the latter.
			return total, nil
		}
	}
}

// AutoFlush is the opportunistic post-command drain: it is throttled to at
// most once per throttleInterval via an mtime marker file in the spool dir,
// so a busy shell issuing many wrapped commands doesn't attempt a network
// round trip on every single one.
func (e *Emitter) AutoFlush(ctx context.Context) error {
	if err := os.MkdirAll(e.dir, 0o755); err != nil {
		return err
	}
	marker := filepath.Join(e.dir, throttleMarkerFile)
	if info, err := os.Stat(marker); err == nil && time.Since(info.ModTime()) < throttleInterval {
		return nil
	}
	now := time.Now()
	if err := os.Chtimes(marker, now, now); err != nil {
		if f, cerr := os.OpenFile(marker, os.O_CREATE|os.O_WRONLY, 0o644); cerr == nil {
			f.Close()
		}
	}
	return e.Flush(ctx)
}

// orgOf returns the org slug from a "org/repo" repositoryName: the substring
// before the first '/', or "" if there is no '/' or name is empty.
func orgOf(name string) string {
	if before, _, ok := strings.Cut(name, "/"); ok {
		return before
	}
	return ""
}

// flushOnce sends AT MOST ONE CHUNK per org group and reports what happened.
// It never shares one deadline across multiple requests: each chunk's POST
// gets its own fresh `timeout` window (via postChunk), so a slow or huge
// backlog in one org can never eat the budget another org's request needed.
// A chunk is removed from the spool ONLY once its own request is
// acknowledged 2xx (or quarantined after repeated 4xx) — a chunk that never
// got a request, or whose request failed, is left byte-for-byte in place.
func (e *Emitter) flushOnce(ctx context.Context, timeout time.Duration) (FlushResult, error) {
	path := filepath.Join(e.dir, pendingFile)
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return FlushResult{}, nil
		}
		return FlushResult{}, fmt.Errorf("reading spool: %w", err)
	}
	if len(bytes.TrimSpace(data)) == 0 {
		return FlushResult{}, nil
	}

	lines := make([][]byte, 0, 64)
	for line := range bytes.SplitSeq(data, []byte{'\n'}) {
		if len(bytes.TrimSpace(line)) == 0 {
			continue
		}
		lines = append(lines, line)
	}
	originalCount := len(lines)

	// Drop anything whose purpose is no longer authorised, BEFORE grouping,
	// and persist the drop immediately so it survives even if nothing below
	// ever sends anything.
	//
	// Dropped rather than retained: holding a customer's improvement data on the
	// chance they later say yes is keeping data they refused. The service half is
	// unaffected — it is authorised by the API key, which is what pays for the
	// dashboards it feeds.
	permitted := make([][]byte, 0, len(lines))
	for _, line := range lines {
		var meta struct {
			Kind string `json:"kind"`
		}
		// An undecodable line has no knowable purpose. PurposeOf defaults to
		// improvement, which is the conservative answer for an unclassifiable
		// event.
		_ = json.Unmarshal(line, &meta)
		if e.resolver.PermitsPurpose(telemetry.PurposeOf(meta.Kind)) {
			permitted = append(permitted, line)
		}
	}
	if len(permitted) != originalCount {
		if len(permitted) == 0 {
			if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
				return FlushResult{Pending: originalCount}, err
			}
			return FlushResult{}, nil
		}
		if err := writeSpool(e.dir, path, permitted); err != nil {
			return FlushResult{Pending: originalCount}, err
		}
	}
	lines = permitted
	if len(lines) == 0 {
		return FlushResult{}, nil
	}
	afterPurgeCount := len(lines)

	// Group lines by org, preserving first-seen group order so delivery is
	// deterministic across runs of the same spool contents. Indices, not
	// byte slices, so a chunk of one group can be identified and removed
	// exactly even though the file interleaves several orgs' lines.
	groups := make(map[string][]int)
	var groupOrder []string
	for i, line := range lines {
		var meta struct {
			RepositoryName string `json:"repositoryName"`
		}
		org := "" // unmarshal failure ⇒ treat as no repo, default-org group
		if json.Unmarshal(line, &meta) == nil {
			org = orgOf(meta.RepositoryName)
		}
		if _, ok := groups[org]; !ok {
			groupOrder = append(groupOrder, org)
		}
		groups[org] = append(groups[org], i)
	}

	result := FlushResult{}
	removed := make([]bool, len(lines))
	var firstErr error

	for _, org := range groupOrder {
		idxs := groups[org]
		token, ok := e.resolver.TokenForOrg(org)
		if !ok {
			// No key configured for this org yet: keep for a later flush,
			// never deliver it under the wrong key.
			continue
		}

		chunkIdx := headChunkIndices(lines, idxs)
		chunk := make([][]byte, len(chunkIdx))
		for j, i := range chunkIdx {
			chunk[j] = lines[i]
		}

		status, postErr := e.postChunk(ctx, timeout, token, chunk)
		result.Requests++

		switch {
		case postErr == nil && status >= 200 && status <= 299:
			for _, i := range chunkIdx {
				removed[i] = true
			}
			result.Sent += len(chunk)
			clearRejectCount(e.dir, chunkFingerprint(chunk))

		case postErr == nil && status >= 400 && status <= 499:
			// A 4xx is a PERMANENT rejection (malformed lines from an
			// ancient sctx version, most likely): retrying it forever would
			// wedge every event queued behind it. Quarantine it once the
			// SAME chunk has been rejected 3 times in a row.
			fp := chunkFingerprint(chunk)
			if n := incrementRejectCount(e.dir, fp); n >= maxConsecutiveChunkRejects {
				if qerr := appendQuarantine(e.dir, chunk); qerr != nil {
					if firstErr == nil {
						firstErr = qerr
					}
					continue
				}
				for _, i := range chunkIdx {
					removed[i] = true
				}
				result.Quarantined += len(chunk)
				clearRejectCount(e.dir, fp)
			} else if firstErr == nil {
				firstErr = fmt.Errorf("telemetry endpoint rejected chunk (%d/%d): status %d", n, maxConsecutiveChunkRejects, status)
			}

		default:
			if firstErr == nil {
				if postErr != nil {
					firstErr = postErr
				} else {
					firstErr = fmt.Errorf("telemetry endpoint returned status %d", status)
				}
			}
		}
	}

	remaining := make([][]byte, 0, len(lines))
	for i, line := range lines {
		if !removed[i] {
			remaining = append(remaining, line)
		}
	}
	result.Pending = len(remaining)

	if len(remaining) != afterPurgeCount {
		if len(remaining) == 0 {
			if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
				if firstErr == nil {
					firstErr = err
				}
			}
		} else if err := writeSpool(e.dir, path, remaining); err != nil {
			if firstErr == nil {
				firstErr = err
			}
		}
	}

	return result, firstErr
}

// headChunkIndices returns the longest PREFIX of idxs (indices into lines)
// bounded by chunkMaxEvents and chunkMaxBytes. It always returns at least
// one index when idxs is non-empty, even if that single line alone exceeds
// chunkMaxBytes — an oversized line must still eventually be sent (or
// rejected and quarantined), never wedged forever behind its own size cap.
func headChunkIndices(lines [][]byte, idxs []int) []int {
	chunk := make([]int, 0, min(len(idxs), chunkMaxEvents))
	size := 0
	for _, i := range idxs {
		lineLen := len(lines[i]) + 1 // +1 for the newline it is stored with
		if len(chunk) > 0 && (len(chunk) >= chunkMaxEvents || size+lineLen > chunkMaxBytes) {
			break
		}
		chunk = append(chunk, i)
		size += lineLen
	}
	return chunk
}

// postChunk POSTs exactly chunk as one telemetry batch, under its own fresh
// timeout deadline (never shared with any other chunk's request). It never
// removes anything from the spool itself — the caller decides what a given
// status code means for retention.
func (e *Emitter) postChunk(ctx context.Context, timeout time.Duration, token string, chunk [][]byte) (status int, err error) {
	events := make([]jsontext.Value, 0, len(chunk))
	for _, line := range chunk {
		events = append(events, jsontext.Value(line))
	}
	// No tenantId: the server never read one. Both ingest handlers take the
	// organization from the AUTHENTICATED KEY (or, on the local path, from
	// each event's repositoryName) — the proxy's batch DTO does not even
	// declare the field. Sending it invited exactly one confusion: `sctx
	// doctor` printed a tenant this machine does not have, defaulted to an
	// UPPERCASE ULID that the platform's lowercase-only `ulid` domain would
	// reject outright if anything ever trusted it.
	payload, err := json.Marshal(map[string]any{
		"events": events,
	})
	if err != nil {
		return 0, fmt.Errorf("encoding telemetry batch: %w", err)
	}

	reqCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	req, err := http.NewRequestWithContext(reqCtx, http.MethodPost, e.endpoint, bytes.NewReader(payload))
	if err != nil {
		return 0, fmt.Errorf("building telemetry request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("User-Agent", e.userAgent)
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}

	resp, err := e.client.Do(req)
	if err != nil {
		return 0, fmt.Errorf("posting telemetry: %w", err)
	}
	defer resp.Body.Close()
	return resp.StatusCode, nil
}

// chunkFingerprint identifies a chunk by the exact bytes it carries, so the
// 4xx reject counter recognises "the same chunk again" across separate
// flush calls even though its position in the spool can shift as sibling
// groups drain around it.
func chunkFingerprint(chunk [][]byte) string {
	h := sha256.New()
	for _, line := range chunk {
		h.Write(line)
		h.Write([]byte{'\n'})
	}
	return hex.EncodeToString(h.Sum(nil))
}

func rejectAttemptsPath(dir string) string {
	return filepath.Join(dir, rejectAttemptsFile)
}

func loadRejectCounts(dir string) map[string]int {
	data, err := os.ReadFile(rejectAttemptsPath(dir))
	if err != nil {
		return map[string]int{}
	}
	var m map[string]int
	if err := json.Unmarshal(data, &m); err != nil || m == nil {
		return map[string]int{}
	}
	return m
}

func saveRejectCounts(dir string, m map[string]int) error {
	if len(m) == 0 {
		err := os.Remove(rejectAttemptsPath(dir))
		if err != nil && !os.IsNotExist(err) {
			return err
		}
		return nil
	}
	data, err := json.Marshal(m)
	if err != nil {
		return err
	}
	return os.WriteFile(rejectAttemptsPath(dir), data, 0o600)
}

// incrementRejectCount records one more 4xx for fingerprint and returns the
// new consecutive count.
func incrementRejectCount(dir, fingerprint string) int {
	m := loadRejectCounts(dir)
	m[fingerprint]++
	n := m[fingerprint]
	_ = saveRejectCounts(dir, m)
	return n
}

// clearRejectCount drops fingerprint's counter — called once its chunk
// either succeeds or is quarantined, so an unrelated future chunk that
// happens to collide on content starts from zero.
func clearRejectCount(dir, fingerprint string) {
	m := loadRejectCounts(dir)
	if _, ok := m[fingerprint]; !ok {
		return
	}
	delete(m, fingerprint)
	_ = saveRejectCounts(dir, m)
}

// appendQuarantine appends chunk, verbatim, to dir's rejectedFile. Events
// that land here were rejected 4xx by the platform maxConsecutiveChunkRejects
// times in a row — never resent, never silently discarded, kept as JSONL for
// a human to inspect (e.g. a schema an ancient sctx version wrote wrong).
func appendQuarantine(dir string, chunk [][]byte) error {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	f, err := os.OpenFile(filepath.Join(dir, rejectedFile), os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return fmt.Errorf("quarantining rejected chunk: %w", err)
	}
	defer f.Close()
	for _, line := range chunk {
		if _, err := f.Write(append(append([]byte{}, line...), '\n')); err != nil {
			return fmt.Errorf("quarantining rejected chunk: %w", err)
		}
	}
	return nil
}

// writeSpool atomically rewrites dir's pending spool file (path) to contain
// exactly lines, in order — shared by the purpose filter and chunk removal,
// neither of which may ever leave the file partially written.
func writeSpool(dir, path string, lines [][]byte) error {
	tmp, err := os.CreateTemp(dir, ".pending-*.jsonl")
	if err != nil {
		return fmt.Errorf("rewriting spool: %w", err)
	}
	for _, line := range lines {
		if _, err := tmp.Write(append(line, '\n')); err != nil {
			tmp.Close()
			os.Remove(tmp.Name())
			return fmt.Errorf("rewriting spool: %w", err)
		}
	}
	if err := tmp.Close(); err != nil {
		os.Remove(tmp.Name())
		return fmt.Errorf("rewriting spool: %w", err)
	}
	if err := os.Chmod(tmp.Name(), 0o644); err != nil {
		os.Remove(tmp.Name())
		return fmt.Errorf("rewriting spool: %w", err)
	}
	if err := os.Rename(tmp.Name(), path); err != nil {
		os.Remove(tmp.Name())
		return fmt.Errorf("rewriting spool: %w", err)
	}
	return nil
}
