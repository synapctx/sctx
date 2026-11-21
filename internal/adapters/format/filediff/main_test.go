package filediff

import (
	"os"
	"testing"

	"github.com/synapctx/sctx/internal/platform/difffloor"
)

// TestMain disables the diff fidelity floor so the compaction tests can use
// small fixtures; the floor tests set it explicitly.
func TestMain(m *testing.M) {
	_ = os.Setenv(difffloor.EnvVar, "0")
	os.Exit(m.Run())
}
