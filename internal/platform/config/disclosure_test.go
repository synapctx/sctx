package config

import (
	"reflect"
	"strings"
	"testing"

	"github.com/synapctx/sctx/internal/domain/telemetry"
)

// disclosedFields maps every JSON field on telemetry.Event to how it is covered
// by ConsentDisclosure. A field absent from this map fails the test below, which
// is the entire mechanism: the payload cannot grow without a human deciding what
// the customer is told, and bumping CurrentDisclosure so they are re-asked.
//
// The value is the phrase the disclosure must contain for that field. Empty
// means "carried but not customer-facing data" — routing and identity of the
// event itself, not of the person — and each of those needs its reason here.
var disclosedFields = map[string]string{
	"program":          "the program and subcommand",
	"command":          "the program and subcommand", // same key, program-only by construction
	"repositoryName":   "the repository",
	"rawTokens":        "token counts",
	"outTokens":        "token counts",
	"savedTokens":      "token counts",
	"exitCode":         "the exit code",
	"durationMs":       "how long it took",
	"formatterMatched": "which formatter matched",
	"version":          "the sctx version",
	"at":               "and when it happened",
	"tier":             "which formatter matched", // which of aggressive/relaxed/verbatim ran
	"id":               "",                        // random ULID, for de-duplication on ingest
	"kind":             "",                        // exec_savings | coverage_gap, the event's own type
	"tool":             "",                        // constant "sctx"
}

// THE test that makes consent mean something over time. A customer agreed to a
// LIST, not to "telemetry"; if the payload gains a field and the disclosure does
// not, their agreement silently covers something they never saw.
//
// Reflective on purpose. A hand-written assertion checks the fields someone
// remembered — which is exactly the set already disclosed — and stays green for
// the one that gets added next.
func TestEveryTelemetryFieldIsDisclosedOrExplicitlyExempt(t *testing.T) {
	typ := reflect.TypeOf(telemetry.Event{})
	for i := 0; i < typ.NumField(); i++ {
		name := jsonName(typ.Field(i).Tag.Get("json"))
		if name == "" || name == "-" {
			continue
		}
		phrase, known := disclosedFields[name]
		if !known {
			t.Errorf("telemetry.Event.%s (json %q) is sent but not accounted for.\n"+
				"  Add it to ConsentDisclosure and to disclosedFields, then BUMP CurrentDisclosure\n"+
				"  so everyone who already consented is asked again about the larger payload.",
				typ.Field(i).Name, name)
			continue
		}
		if phrase != "" && !strings.Contains(ConsentDisclosure, phrase) {
			t.Errorf("field %q claims to be disclosed by %q, which is not in ConsentDisclosure", name, phrase)
		}
	}
}

// The promise that does the most work, stated in the negative. If this text ever
// softens, the preview stops being evidence and becomes marketing.
func TestTheDisclosureRulesOutArgumentsAndPaths(t *testing.T) {
	for _, promise := range []string{
		"NEVER: command ARGUMENTS",
		"file paths",
		"file contents",
		"environment",
	} {
		if !strings.Contains(ConsentDisclosure, promise) {
			t.Errorf("the disclosure no longer promises %q", promise)
		}
	}
}

func jsonName(tag string) string {
	if tag == "" {
		return ""
	}
	if i := strings.Index(tag, ","); i >= 0 {
		return tag[:i]
	}
	return tag
}
