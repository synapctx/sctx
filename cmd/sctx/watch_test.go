package main

import "testing"

// `sctx watch` sends CODE, which is more sensitive than the command telemetry
// the rest of sctx collects. These pin the properties that justify asking a
// developer to run it at all.

func TestAnUnconfiguredProxyIsRemoteAware(t *testing.T) {
	// The workspace routes live on the MCP host, a DIFFERENT host from telemetry
	// ingest, so a remote endpoint with a loopback workspace proxy means the
	// developer authenticated but never configured this. Pushing at localhost
	// forever would report nothing and look exactly like a quiet workspace.
	if !isRemote("https://sctx.synapctx.com/v1/telemetry/exec") {
		t.Fatal("a real https endpoint must read as remote")
	}
	for _, local := range []string{
		"http://127.0.0.1:6221/v1/telemetry/exec",
		"http://localhost:6220",
		"https://localhost:6220",
		"",
	} {
		if isRemote(local) {
			t.Fatalf("%q must not read as remote", local)
		}
	}
}

func TestWatchRootsAreTakenFromArguments(t *testing.T) {
	roots, help := parseWatchArgs([]string{"--root", "/a", "/b"})
	if help {
		t.Fatal("did not ask for help")
	}
	if len(roots) != 2 || roots[0] != "/a" || roots[1] != "/b" {
		t.Fatalf("roots %v, want /a and /b", roots)
	}
}

func TestWatchHelpIsRequestable(t *testing.T) {
	for _, arg := range []string{"-h", "--help", "help"} {
		if _, help := parseWatchArgs([]string{arg}); !help {
			t.Fatalf("%q must request help", arg)
		}
	}
}
