// Package npx routes JavaScript tool output to the dedicated formatters.
//
// Two entry points share one decision table. The npx Formatter claims
// `npx <tool> ...` and dispatches on the NAMED tool (jest, vitest, tsc,
// eslint, playwright, prettier, prisma, turbo, nx, biome, oxlint) — the name is argv, so no guessing is involved. Sniff is for
// package-manager wrappers (`npm test`, `pnpm vitest`, `yarn jest`) whose
// argv does not say what the script runs: it offers the captured bytes to each
// tool formatter, every one of which declines unless the output has its exact
// summary shape.
package npx

import (
	"bytes"
	"context"
	"strings"

	"github.com/synapctx/sctx/internal/adapters/format/biome"
	"github.com/synapctx/sctx/internal/adapters/format/bun"
	"github.com/synapctx/sctx/internal/adapters/format/eslint"
	"github.com/synapctx/sctx/internal/adapters/format/jstest"
	"github.com/synapctx/sctx/internal/adapters/format/jsutil"
	"github.com/synapctx/sctx/internal/adapters/format/monorepo"
	"github.com/synapctx/sctx/internal/adapters/format/nextbuild"
	"github.com/synapctx/sctx/internal/adapters/format/playwright"
	"github.com/synapctx/sctx/internal/adapters/format/prettier"
	"github.com/synapctx/sctx/internal/adapters/format/prisma"
	"github.com/synapctx/sctx/internal/adapters/format/tsc"
	"github.com/synapctx/sctx/internal/domain/format"
)

// Formatter renders `npx <tool>` invocations of the supported tools.
type Formatter struct{}

// New constructs the npx Formatter.
func New() *Formatter { return &Formatter{} }

// Descriptor claims every npx invocation; Aggressive declines tools it does
// not know.
func (f *Formatter) Descriptor() format.Match { return format.Match{Command: "npx"} }

// Aggressive dispatches on the tool npx was asked to run.
func (f *Formatter) Aggressive(ctx context.Context, in format.Input) (format.Rendered, error) {
	switch Tool(in.Argv) {
	case "jest":
		return jstest.Render("jest", in)
	case "vitest":
		return jstest.Render("vitest", in)
	case "tsc":
		return tsc.Render(in)
	case "eslint":
		return eslint.Render(in)
	case "playwright":
		return playwright.Render(in)
	case "prettier":
		return prettier.Render(in)
	case "prisma":
		return prisma.Render(in)
	case "turbo":
		return monorepo.RenderTurbo(in)
	case "nx":
		return monorepo.RenderNx(in)
	case "biome":
		return biome.NewBiome().Aggressive(ctx, in)
	case "oxlint":
		return biome.NewOxlint().Aggressive(ctx, in)
	}
	return format.Rendered{}, format.ErrTierInapplicable
}

// Relaxed never applies: npx has no output of its own worth filtering.
func (f *Formatter) Relaxed(ctx context.Context, in format.Input) (format.Rendered, error) {
	return format.Rendered{}, format.ErrTierInapplicable
}

// Tool returns the first non-flag argument after argv[0].
func Tool(argv []string) string {
	for _, a := range argv[min(1, len(argv)):] {
		if !strings.HasPrefix(a, "-") {
			return a
		}
	}
	return ""
}

// Sniff offers a captured run to every JS tool formatter and returns the first
// render, or ErrTierInapplicable. in's streams are consumed.
func Sniff(in format.Input) (format.Rendered, error) {
	stdout, stderr := jsutil.Streams(in)
	return SniffBytes(in, stdout, stderr)
}

// SniffBytes is Sniff over already-read streams.
func SniffBytes(in format.Input, stdout, stderr []byte) (format.Rendered, error) {
	for _, try := range []func(format.Input) (format.Rendered, error){
		func(i format.Input) (format.Rendered, error) { return jstest.Render("", i) },
		tsc.Render,
		eslint.Render,
		playwright.Render,
		prettier.Render,
		prisma.Render,
		nextbuild.Render,
		bun.Render,
		monorepo.RenderTurbo,
		monorepo.RenderNx,
	} {
		c := in
		c.Stdout, c.Stderr = bytes.NewReader(stdout), bytes.NewReader(stderr)
		if r, err := try(c); err == nil {
			return r, nil
		}
	}
	return format.Rendered{}, format.ErrTierInapplicable
}
