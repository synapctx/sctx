package telemetry

import (
	"reflect"
	"testing"
)

// Every event kind must be classified deliberately. An unclassified kind
// defaults to improvement (needs consent), which is the safe side — this test
// exists so the default is a fallback, not the actual policy for a real kind.
func TestEveryEventKindHasAPurpose(t *testing.T) {
	kinds := map[string]string{
		KindExecSavings:     PurposeService,
		KindCoverageGap:     PurposeImprovement,
		KindCoverageDecline: PurposeImprovement,
	}
	for kind, want := range kinds {
		if got := PurposeOf(kind); got != want {
			t.Errorf("PurposeOf(%q) = %q, want %q", kind, got, want)
		}
	}
	if PurposeOf("") != PurposeImprovement || PurposeOf("invented") != PurposeImprovement {
		t.Error("an unknown kind must default to the purpose that REQUIRES consent")
	}
	if reflect.DeepEqual(PurposeService, PurposeImprovement) {
		t.Error("the two purposes must be distinguishable")
	}
}
