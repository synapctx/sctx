package config

import "time"

// Telemetry consent, which applies to ONE of the two purposes.
//
// Until 2026-08-02 `telemetry_enabled` defaulted to TRUE and nothing ever asked:
// usage data left the machine because nobody had said no, which is not the same
// as somebody saying yes.
//
// The first fix over-corrected and gated EVERYTHING behind one prompt. That broke
// the product: a paying customer who declined opened their console and saw zero
// savings — we had withheld the customer's own report about the tool they bought,
// in order to protect them from it.
//
// So the split is by PURPOSE (see telemetry.PurposeOf):
//
//	service      the customer's own savings report, rendered on their own
//	             dashboards. Authorised by holding an API key, because an active
//	             key is a contract. Without a key nothing leaves the machine at
//	             all — the default endpoint is loopback.
//	improvement  which commands sctx fails to cover, and therefore which
//	             ecosystems our customers work in. Its value comes from
//	             AGGREGATING across customers, which nobody agreed to by buying a
//	             licence. Opt-in, and that is what this consent record governs.
//
// An EXPLICIT `telemetry_enabled` (file or environment) still overrides BOTH.
// Somebody who typed it meant it, and enterprise rollouts need a way to answer
// centrally without a prompt nobody will see.
const (
	ConsentGranted  = "granted"
	ConsentDeclined = "declined"
)

// CurrentDisclosure is the version of what we tell the customer we collect.
//
// It is stored WITH the decision, and a recorded decision older than this counts
// as no decision at all — so consent is re-asked. This is the whole reason the
// field exists: consent to "program names and token counts" is not consent to
// whatever we might decide to send in six months, and without a version the
// difference is invisible to everyone including us.
//
// BUMP THIS whenever the payload gains a field. See ConsentDisclosure for the
// text that must be kept in step with it.
// Bumped to 2 on 2026-08-02 when telemetry was split by purpose: the question
// narrowed from "may we collect all of this" to "may we collect the commands we
// fail to cover, and combine them with other customers'". A decision made
// against the older, broader text does not transfer — it was an answer to a
// different question.
// Bumped to 3 on 2026-08-15 when formatter selection, actual reduction and a
// fixed privacy-safe decline category became separate fields. No arguments,
// paths or output were added, but the payload still grew and prior consent does
// not silently extend to it.
const CurrentDisclosure = 3

// ConsentRecord is a customer's answer, as stored.
type ConsentRecord struct {
	Decision   string // ConsentGranted | ConsentDeclined | "" (never asked)
	At         string // RFC3339, informational
	Disclosure int    // the CurrentDisclosure they were shown
}

// Answered reports whether there is a usable decision on file: one that was
// made, and made about the payload we send today.
func (r ConsentRecord) Answered() bool {
	if r.Decision != ConsentGranted && r.Decision != ConsentDeclined {
		return false
	}
	return r.Disclosure >= CurrentDisclosure
}

// Grants reports whether telemetry may be delivered on the strength of this
// record alone.
func (r ConsentRecord) Grants() bool {
	return r.Answered() && r.Decision == ConsentGranted
}

// Stale reports the specific case worth telling the customer about: they DID
// decide, and then we changed what we collect. Distinguished from "never asked"
// because the honest prompt is different — one is "may we", the other is "this
// changed, may we still".
func (r ConsentRecord) Stale() bool {
	answered := r.Decision == ConsentGranted || r.Decision == ConsentDeclined
	return answered && r.Disclosure < CurrentDisclosure
}

// NewConsent stamps a decision at the current disclosure.
func NewConsent(decision string, now time.Time) ConsentRecord {
	return ConsentRecord{
		Decision:   decision,
		At:         now.UTC().Format(time.RFC3339),
		Disclosure: CurrentDisclosure,
	}
}

// ConsentDisclosure is what the customer is shown before deciding, and what
// `sctx telemetry` prints on demand.
//
// It states the payload FIELD BY FIELD rather than summarising it, because a
// summary is what lets a customer discover later that "usage data" included
// something they would have refused. Keep it true: every line here is checked
// against telemetry.Event by test, and CurrentDisclosure must be bumped when the
// payload changes.
const ConsentDisclosure = `Every record sctx sends carries exactly these fields:

  the program and subcommand   "go test", "cargo build", "pytest"
  the repository               "your-org/your-repo", from the git remote
  token counts                 bytes in, bytes out, tokens saved
  the exit code                whether the command succeeded
  how long it took             milliseconds
  which formatter matched      and which formatter path was selected
  whether output was reduced   and why output stayed native when it was not
  the sctx version             and when it happened

NEVER: command ARGUMENTS, file paths, file contents, environment variables,
output, branch names, or anything you typed. A command is recorded as
"git push", never "git push origin secret-branch".

That one record shape serves two purposes, and only the second is a question.

1. YOUR OWN USAGE — already on, because it is what you bought.
   Every wrapped command reports what it saved, so console.synapctx.com can show
   you what sctx is doing for you. This is the product reporting on itself;
   switching it off leaves your savings dashboard empty. It needs an API key —
   without one, nothing leaves your machine at all.

2. COVERAGE AND SAFETY POSTURE — this is what we are asking about.
   When sctx meets a command it has no formatter for, we would like to record it
   (the measurement fields are empty for these; there was nothing to measure).
   We also record a fixed reason when the hook deliberately leaves a known
   command untouched, such as streaming output or an unsafe shell pipeline.
   We may combine these with other customers' to rank what to build next and
   audit safety choices. THAT is why it is a question rather than a term in a
   contract.

   Coverage follows what people actually run. A command sctx has never seen is
   one nobody knows to build for — whatever language, toolchain, database or
   platform it belongs to — and these records are what decide which formatter
   is written next.

See exactly what is queued with 'sctx telemetry --preview', and change your mind
with 'sctx telemetry --enable' / '--disable'.`

// TelemetryPermitted answers "may we collect for this purpose right now" for
// callers that have no Config in hand — chiefly the Claude Code hook, which is dispatched BEFORE
// config load so that it stays fail-open when the environment cannot produce a
// valid Config.
//
// It FAILS CLOSED: any error, and the answer is no. That is the safe direction
// here and it costs nothing, because an unanswered decision already means no.
//
// It deliberately reuses Load rather than reading the keys itself. Precedence —
// explicit setting beats consent, consent beats the default — must exist in
// exactly one place; the progkey incident in this repo is what a duplicated
// predicate does over time.
func TelemetryPermitted(purpose string) bool {
	cfg, err := Load()
	if err != nil {
		return false
	}
	return cfg.PermitsPurpose(purpose)
}
