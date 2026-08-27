// Package spool implements offline-safe telemetry: Emit appends one JSONL
// line to a local spool file and returns immediately; Flush opportunistically
// POSTs the spooled batch to the developer-mcp-proxy under a strict deadline.
// The platform being down costs nothing but a stale spool.
package spool

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/synapctx/sctx/internal/domain/telemetry"
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
}

// NewEmitter builds an Emitter for endpoint, selecting connect/flush budgets
// by whether endpoint's host is loopback (fast, plain-HTTP local proxy) or
// remote (slower, allows time for a TLS handshake). resolver picks the API
// key (if any) that should deliver each event, keyed by the org slug of its
// repositoryName.
func NewEmitter(dir, endpoint string, resolver TokenResolver) *Emitter {
	connectTimeout, flushTimeout := loopbackConnectTimeout, loopbackFlushTimeout
	if !isLoopbackEndpoint(endpoint) {
		connectTimeout, flushTimeout = remoteConnectTimeout, remoteFlushTimeout
	}
	return &Emitter{
		dir:          dir,
		endpoint:     endpoint,
		resolver:     resolver,
		flushTimeout: flushTimeout,
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

// Flush drains the spool in one batch POST under the Emitter's default
// budget (loopback: 300ms; remote: 2s). On any failure the spool is left
// intact for a later attempt.
func (e *Emitter) Flush(ctx context.Context) error {
	return e.flush(ctx, e.flushTimeout)
}

// FlushWithTimeout drains the spool like Flush but overrides the per-flush
// deadline, so a large backlog (near maxSpoolBytes) has room to fully drain.
// Used by `sctx flush` and `sctx init`, never by the opportunistic
// post-command path.
func (e *Emitter) FlushWithTimeout(ctx context.Context, timeout time.Duration) error {
	return e.flush(ctx, timeout)
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

func (e *Emitter) flush(ctx context.Context, timeout time.Duration) error {
	path := filepath.Join(e.dir, pendingFile)
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return fmt.Errorf("reading spool: %w", err)
	}
	if len(bytes.TrimSpace(data)) == 0 {
		return nil
	}

	lines := make([][]byte, 0, 64)
	for line := range bytes.SplitSeq(data, []byte{'\n'}) {
		if len(bytes.TrimSpace(line)) == 0 {
			continue
		}
		lines = append(lines, line)
	}

	// Drop anything whose purpose is no longer authorised, BEFORE grouping.
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
	lines = permitted

	// Group lines by org, preserving first-seen group order so delivery is
	// deterministic across runs of the same spool contents.
	groups := make(map[string][][]byte)
	var groupOrder []string
	for _, line := range lines {
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
		groups[org] = append(groups[org], line)
	}

	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	retained := make([][]byte, 0)
	var firstErr error
	for _, org := range groupOrder {
		groupLines := groups[org]
		token, ok := e.resolver.TokenForOrg(org)
		if !ok {
			// No key configured for this org yet: keep for a later flush,
			// never deliver it under the wrong key.
			retained = append(retained, groupLines...)
			continue
		}

		events := make([]json.RawMessage, 0, len(groupLines))
		for _, line := range groupLines {
			events = append(events, json.RawMessage(line))
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
			if firstErr == nil {
				firstErr = fmt.Errorf("encoding telemetry batch: %w", err)
			}
			retained = append(retained, groupLines...)
			continue
		}

		req, err := http.NewRequestWithContext(ctx, http.MethodPost, e.endpoint, bytes.NewReader(payload))
		if err != nil {
			if firstErr == nil {
				firstErr = fmt.Errorf("building telemetry request: %w", err)
			}
			retained = append(retained, groupLines...)
			continue
		}
		req.Header.Set("Content-Type", "application/json")
		if token != "" {
			req.Header.Set("Authorization", "Bearer "+token)
		}

		resp, err := e.client.Do(req)
		if err != nil {
			if firstErr == nil {
				firstErr = fmt.Errorf("posting telemetry: %w", err)
			}
			retained = append(retained, groupLines...)
			continue
		}
		if resp.StatusCode < 200 || resp.StatusCode > 299 {
			if firstErr == nil {
				firstErr = fmt.Errorf("telemetry endpoint returned %s", resp.Status)
			}
			retained = append(retained, groupLines...)
		}
		resp.Body.Close()
	}

	if len(retained) == 0 {
		if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
			return err
		}
		return firstErr
	}

	tmp, err := os.CreateTemp(e.dir, ".pending-*.jsonl")
	if err != nil {
		if firstErr == nil {
			firstErr = fmt.Errorf("rewriting spool: %w", err)
		}
		return firstErr
	}
	for _, line := range retained {
		if _, err := tmp.Write(append(line, '\n')); err != nil {
			tmp.Close()
			os.Remove(tmp.Name())
			if firstErr == nil {
				firstErr = fmt.Errorf("rewriting spool: %w", err)
			}
			return firstErr
		}
	}
	if err := tmp.Close(); err != nil {
		os.Remove(tmp.Name())
		if firstErr == nil {
			firstErr = fmt.Errorf("rewriting spool: %w", err)
		}
		return firstErr
	}
	if err := os.Chmod(tmp.Name(), 0o644); err != nil {
		os.Remove(tmp.Name())
		if firstErr == nil {
			firstErr = fmt.Errorf("rewriting spool: %w", err)
		}
		return firstErr
	}
	if err := os.Rename(tmp.Name(), path); err != nil {
		os.Remove(tmp.Name())
		if firstErr == nil {
			firstErr = fmt.Errorf("rewriting spool: %w", err)
		}
		return firstErr
	}

	return firstErr
}
