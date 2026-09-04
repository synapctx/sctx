// Package httpclient builds the one User-Agent string every HTTP call sctx
// makes must send, so the server-side telemetry ingest and MCP proxy can tell
// sctx traffic apart from the generic Go-http-client default and from each
// other's client environment.
package httpclient

import (
	"fmt"
	"runtime"
)

// UserAgent returns "sctx/<version> (<GOOS>/<GOARCH>; client=<client>)".
// version and client are never validated here — version is whatever the
// binary was built with ("dev" included) and client is whatever
// internal/platform/agentenv resolved (falling back to "shell" or
// "unknown", never an arbitrary string, by agentenv's own contract) — this
// package only formats what it is given, defaulting an empty value so the
// header is never malformed.
func UserAgent(version, client string) string {
	if version == "" {
		version = "dev"
	}
	if client == "" {
		client = "unknown"
	}
	return fmt.Sprintf("sctx/%s (%s/%s; client=%s)", version, runtime.GOOS, runtime.GOARCH, client)
}
