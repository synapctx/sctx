package main

import "fmt"

// The usage text lives apart from runWatch so a test can assert the privacy
// claim is actually made, rather than trusting that it is.

func watchUsageText() string {
	return `sctx watch — keep uncommitted code queryable by your agent

Usage:
  sctx watch [--root <dir>]...

Watches your working trees and streams the STRUCTURAL DIFF of uncommitted code
(symbol names, signatures, doc comments, content hashes — never bodies) to
SynapCTX, so retrieve_context answers about the code you are changing rather
than the last commit. Results from your working tree are marked UNCOMMITTED.

Requires a SynapCTX API key (` + "`sctx init`" + `). Without one, sctx is a
standalone token-optimizing wrapper and this command does nothing.

A --root CONTAINS organization directories (<root>/<org>/<repo>); it is not an
organization directory itself. Defaults to ~/git/github.com when it exists.

Runs in the foreground. Stop it and nothing further is sent.`
}

func printWatchUsage() { fmt.Println(watchUsageText()) }
