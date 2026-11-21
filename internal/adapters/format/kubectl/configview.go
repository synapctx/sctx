package kubectl

import (
	"fmt"
	"regexp"
	"strings"

	"github.com/synapctx/sctx/internal/domain/format"
)

// configSecretLine matches kubeconfig fields that carry key material or a
// credential. `kubectl config view` masks them as DATA+OMITTED/REDACTED, but
// `--raw` prints them in full, and the generic secret scanner (PEM, JWT, key=
// value with a known key name) does not recognise a base64 `client-key-data`
// blob, so the formatter elides them itself.
var configSecretLine = regexp.MustCompile(`^(\s*(?:-\s+)?(?:client-key-data|client-certificate-data|certificate-authority-data|token|password|id-token|refresh-token|access-token|client-secret)):\s+(\S+)\s*$`)

// aggressiveConfigView replaces every credential/certificate value in a
// kubeconfig with an explicit `…+N chars elided` marker. The names of
// clusters, contexts and users, and the server URLs, are kept verbatim.
func aggressiveConfigView(in format.Input, rest []string) (format.Rendered, error) {
	if len(rest) == 0 || rest[0] != "view" {
		return format.Rendered{}, format.ErrTierInapplicable
	}
	lines := splitLines(readAll(in.Stdout))
	changed := false
	for i, l := range lines {
		m := configSecretLine.FindStringSubmatch(l)
		if m == nil || m[2] == "REDACTED" || m[2] == "DATA+OMITTED" {
			continue
		}
		lines[i] = fmt.Sprintf("%s: …+%d chars elided (credential)", m[1], len(m[2]))
		changed = true
	}
	if !changed {
		return format.Rendered{}, format.ErrTierInapplicable
	}
	return format.Rendered{Body: []byte(strings.Join(lines, "\n"))}, nil
}
