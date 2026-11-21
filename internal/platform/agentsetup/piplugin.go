package agentsetup

// Pi (pi-coding-agent) extension source.
//
// VERIFIED-FROM-THIRD-PARTY-SOURCE: a working third-party Pi extension and its
// README. Pi
// loads TypeScript extensions from `~/.pi/agent/extensions/`; one default-
// exports `async (pi: ExtensionAPI)` and subscribes with
// `pi.on("tool_call", handler)`. The handler narrows to the `bash` tool
// (`event.toolName === "bash"`) and mutates `event.input.command` IN PLACE;
// there is no return value for a rewrite. `pi.exec(cmd, args, {timeout,
// signal})` runs a process and resolves `{code, killed, stdout}`. that
// extension avoids importing the package's value exports (a ~250ms load cost)
// and so does this one — it imports nothing, so there is no type dependency to
// break. Oh My Pi loads the same file from `~/.omp/agent/extensions/`; this
// package manages only the Pi path.
//
// Like the Kilo/OpenCode plugin, every decision stays in the sctx binary
// (`sctx hook rewrite`), and every failure leaves the command untouched.

func piPluginSource(binary string) string {
	return pluginMarker + `
//
// Rewrites covered commands to ` + "`sctx <cmd>`" + ` before Pi's bash tool runs them,
// which is what produces the token savings. Every decision — which commands are
// covered, and when wrapping would change the conclusion — is made by the sctx
// binary, never here.
//
// FAIL-OPEN, ALWAYS: any error, timeout or unexpected output leaves the command
// exactly as the agent wrote it.

const SCTX_BINARY = ` + jsString(binary) + `
const REWRITE_TIMEOUT_MS = 5000

export default async function (pi: any) {
  pi.on("tool_call", async (event: any, ctx: any) => {
    try {
      if (!event || event.toolName !== "bash") return
      const cmd = event.input && event.input.command
      if (typeof cmd !== "string" || cmd.trim() === "") return
      const result = await pi.exec(SCTX_BINARY, ["hook", "rewrite", cmd], {
        timeout: REWRITE_TIMEOUT_MS,
        signal: ctx && ctx.signal,
      })
      if (!result || result.killed || result.code !== 0) return
      const rewritten = String(result.stdout || "").trim()
      if (rewritten && rewritten !== cmd) event.input.command = rewritten
    } catch {
      // Fail open: never block execution on an unexpected error.
    }
  })
}
`
}
