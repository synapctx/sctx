package main

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/synapctx/sctx/internal/domain/telemetry"
	"github.com/synapctx/sctx/internal/platform/config"
)

const telemetryUsage = `usage: sctx telemetry [--preview] [--enable] [--disable]

  (no flags)  what is collected, and whether it is being sent
  --preview   the events queued on THIS machine right now, verbatim
  --enable    record consent and start sending
  --disable   record a refusal and stop sending`

// runTelemetry is the customer's view of their own telemetry.
//
// --preview prints the ACTUAL queued events rather than an example, and that is
// the point of it. A disclosure is a promise; the spool is the evidence. Anyone
// who suspects we send more than we admit can check in one command, which is a
// far stronger claim than any wording in a privacy policy — and it costs us
// nothing, because the payload really is program keys and counts.
func runTelemetry(cfg config.Config, args []string) int {
	preview, enable, disable := false, false, false
	for _, a := range args {
		switch a {
		case "--preview":
			preview = true
		case "--enable":
			enable = true
		case "--disable":
			disable = true
		default:
			fmt.Fprintf(os.Stderr, "sctx: telemetry: unknown flag %q\n", a)
			fmt.Fprintln(os.Stderr, telemetryUsage)
			return 2
		}
	}
	if enable && disable {
		fmt.Fprintln(os.Stderr, "sctx: telemetry: --enable and --disable are contradictory")
		return 2
	}

	if enable || disable {
		decision := config.ConsentDeclined
		if enable {
			decision = config.ConsentGranted
		}
		if err := recordConsent(cfg, decision); err != nil {
			fmt.Fprintf(os.Stderr, "sctx: telemetry: %v\n", err)
			return 1
		}
		if enable {
			fmt.Println("Telemetry ON. Thank you — this is what tells us which ecosystems to support.")
		} else {
			// The claim has to be TRUE. A spool left on disk after a refusal is
			// the refused data still sitting there, one `sctx flush` from being
			// sent — and a promise to discard that quietly does not is worse
			// than not making it.
			n, err := discardSpool(cfg)
			switch {
			case err != nil:
				fmt.Printf("Telemetry OFF. Nothing further will be sent, but the queue at %s could not be cleared: %v\n", cfg.SpoolDir, err)
			case n > 0:
				fmt.Printf("Telemetry OFF. %d queued event(s) discarded; nothing further will be sent.\n", n)
			default:
				fmt.Println("Telemetry OFF. Nothing was queued; nothing further will be sent.")
			}
		}
		if cfg.TelemetryExplicit {
			// Recording the decision is still right — it is the customer's
			// answer — but it would not take effect, and silently doing nothing
			// is how someone concludes the switch is broken.
			fmt.Println("\nNote: an explicit telemetry_enabled setting is currently overriding this.")
			fmt.Printf("      Remove it from %s (or unset SCT__TELEMETRY_ENABLED) for this to apply.\n", cfg.ConfigFilePath)
		}
		return 0
	}

	if preview {
		return previewSpool(cfg)
	}

	fmt.Println(config.ConsentDisclosure)
	fmt.Println()
	fmt.Println(telemetryStatusLine(cfg))
	return 0
}

// telemetryStatusLine reports BOTH purposes, because after the split a single
// "OFF" is a lie: a customer who declined still has their own savings report
// flowing, and telling them otherwise is how they conclude the dashboard is
// broken.
//
// The reason is stated too — a refusal, an unanswered prompt, a stale disclosure
// and a central policy override are four situations with four different fixes,
// and a bare status sends the reader to the wrong one.
func telemetryStatusLine(cfg config.Config) string {
	var b strings.Builder
	fmt.Fprintf(&b, "Your savings report: %s", onOff(cfg.ServiceTelemetryEnabled))
	if cfg.ServiceTelemetryEnabled {
		b.WriteString(" — needs an API key; run 'sctx init' if your console is empty")
	} else {
		b.WriteString(" — switched off explicitly, so console.synapctx.com will show nothing")
	}
	fmt.Fprintf(&b, "\nCommands we fail to cover: %s", onOff(cfg.ImprovementTelemetryEnabled))

	switch {
	case cfg.TelemetryExplicit:
		b.WriteString(" — set explicitly by telemetry_enabled, not by a prompt")
	case cfg.Consent.Stale():
		b.WriteString("\n  You answered before, but what we collect has changed since.\n" +
			"  'sctx telemetry --enable' to allow the list above, or --disable to keep it off.")
	case !cfg.Consent.Answered():
		b.WriteString("\n  Nobody has been asked yet — 'sctx telemetry --enable' to help.")
	case cfg.Consent.Grants():
		fmt.Fprintf(&b, " — you allowed this on %s", cfg.Consent.At)
	default:
		fmt.Fprintf(&b, " — you declined on %s", cfg.Consent.At)
	}
	return b.String()
}

func onOff(b bool) string {
	if b {
		return "ON"
	}
	return "OFF"
}

// previewSpool prints every queued event. Deliberately the raw JSON: a rendered
// table is a summary, and a summary is exactly what a suspicious reader cannot
// verify.
func previewSpool(cfg config.Config) int {
	path := filepath.Join(cfg.SpoolDir, "pending.jsonl")
	data, err := os.ReadFile(path)
	if os.IsNotExist(err) || len(strings.TrimSpace(string(data))) == 0 {
		fmt.Println("Nothing queued.")
		fmt.Println()
		fmt.Println(telemetryStatusLine(cfg))
		return 0
	}
	if err != nil {
		fmt.Fprintf(os.Stderr, "sctx: telemetry: reading %s: %v\n", path, err)
		return 1
	}

	kinds := map[string]int{}
	programs := map[string]int{}
	blocked := 0
	lines := strings.Split(strings.TrimSpace(string(data)), "\n")
	fmt.Printf("%d event(s) queued in %s\n\n", len(lines), path)
	for _, line := range lines {
		if strings.TrimSpace(line) == "" {
			continue
		}
		var ev telemetry.Event
		mark := "  "
		if json.Unmarshal([]byte(line), &ev) == nil {
			kinds[ev.Kind]++
			if ev.Program != "" {
				programs[ev.Program]++
			}
			// Marked per line, because "queued" and "will be sent" are different
			// facts and the preview is worthless if it blurs them.
			if !cfg.PermitsPurpose(telemetry.PurposeOf(ev.Kind)) {
				mark = "x "
				blocked++
			}
		}
		fmt.Println(mark + line)
	}

	fmt.Printf("\nBy kind: %s\n", counted(kinds))
	if len(programs) > 0 {
		fmt.Printf("Programs: %s\n", counted(programs))
	}
	fmt.Printf("\n  yours, already on (%s): sent — this is your savings report\n", telemetry.PurposeService)
	fmt.Printf("  what we learn from (%s): %s\n", telemetry.PurposeImprovement,
		map[bool]string{true: "sent — thank you", false: "NOT sent"}[cfg.ImprovementTelemetryEnabled])
	if blocked > 0 {
		fmt.Printf("\n%d line(s) marked 'x' will be DISCARDED on the next flush, not sent.\n", blocked)
	}
	fmt.Println()
	fmt.Println(telemetryStatusLine(cfg))
	return 0
}

func counted(m map[string]int) string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Slice(keys, func(i, j int) bool {
		if m[keys[i]] != m[keys[j]] {
			return m[keys[i]] > m[keys[j]]
		}
		return keys[i] < keys[j]
	})
	parts := make([]string, 0, len(keys))
	for _, k := range keys {
		parts = append(parts, fmt.Sprintf("%s=%d", k, m[k]))
	}
	return strings.Join(parts, " ")
}

// recordConsent rewrites the config file preserving everything else in it.
//
// It reuses writeConfigFile rather than editing in place because that function is
// already the single writer of this file — and it rewrites wholesale, so a
// consent record that were not threaded through it would be silently erased by
// the next `sctx init`.
func recordConsent(cfg config.Config, decision string) error {
	return writeConfigFile(cfg.ConfigFilePath, cfg.TelemetryEndpoint, configuredWorkspaceProxy(cfg), cfg.DefaultOrg,
		cfg.OrgTokens, config.NewConsent(decision, time.Now()))
}

// shouldAskConsent decides whether the customer is asked during `sctx setup`.
//
// Extracted so the guarantee is testable rather than asserted: a test process has
// no TTY, so an inline terminal check would pass without existing. The same
// reasoning as shouldNudge, and the same failure if it is skipped — the hook path
// has no terminal and no human, and a prompt there would block every Bash command
// the agent runs, forever, waiting for an answer nobody can give.
func shouldAskConsent(cfg config.Config, stdinIsTerminal bool) bool {
	if !stdinIsTerminal {
		return false
	}
	// Already decided by configuration: asking would imply the answer matters
	// when it would be overridden either way.
	if cfg.TelemetryExplicit {
		return false
	}
	return !cfg.Consent.Answered()
}

// askConsent puts the question during `sctx setup`, once, on a terminal.
//
// It defaults to NO on an empty answer. Someone pressing return to get through an
// install has not agreed to anything, and a prompt whose default is yes is a dark
// pattern wearing a consent prompt's clothes.
func askConsent(cfg config.Config, in io.Reader, out io.Writer) {
	fmt.Fprintln(out)
	fmt.Fprintln(out, config.ConsentDisclosure)
	if cfg.Consent.Stale() {
		fmt.Fprintln(out, "\nYou answered this before, but the list above has changed since.")
	}
	fmt.Fprint(out, "\nSend this? [y/N]: ")

	reader := bufio.NewReader(in)
	line, err := reader.ReadString('\n')
	if err != nil && line == "" {
		return // no answer: record nothing, ask again next time
	}
	decision := config.ConsentDeclined
	if a := strings.ToLower(strings.TrimSpace(line)); a == "y" || a == "yes" {
		decision = config.ConsentGranted
	}
	if err := recordConsent(cfg, decision); err != nil {
		fmt.Fprintf(out, "could not record your answer: %v\n", err)
		return
	}
	if decision == config.ConsentGranted {
		fmt.Fprintln(out, "Thank you — 'sctx telemetry --preview' shows exactly what is queued.")
	} else {
		fmt.Fprintln(out, "Nothing will be sent. 'sctx telemetry --enable' if you change your mind.")
	}
}

// discardSpool deletes the pending queue and reports how many events went with
// it, so `--disable` can say what it actually did rather than what it intends.
func discardSpool(cfg config.Config) (int, error) {
	path := filepath.Join(cfg.SpoolDir, "pending.jsonl")
	data, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return 0, nil
	}
	if err != nil {
		return 0, err
	}
	n := 0
	for line := range strings.SplitSeq(strings.TrimSpace(string(data)), "\n") {
		if strings.TrimSpace(line) != "" {
			n++
		}
	}
	if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
		return 0, err
	}
	return n, nil
}
