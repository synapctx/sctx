package docker

import "github.com/synapctx/sctx/internal/domain/format"

// Podman renders podman output with the docker formatter. Podman's CLI is
// docker-compatible and its table output is column-aligned the same way (two
// spaces between columns, which parseHeader already handles); only the
// program name differs. Podman-only global flags that the shared argv grammar
// does not know make the grammar decline, which degrades to the next tier.
type Podman struct{ *Formatter }

// NewPodman constructs the podman descriptor over the docker formatter.
func NewPodman(resolve ...Resolver) *Podman { return &Podman{New(resolve...)} }

// Descriptor claims every podman invocation.
func (p *Podman) Descriptor() format.Match { return format.Match{Command: "podman"} }
