package fs

import (
	"context"
	"fmt"
	"regexp"
	"strings"

	"github.com/synapctx/sctx/internal/domain/format"
)

type treeFormatter struct{}

func (f *treeFormatter) Descriptor() format.Match {
	return format.Match{Command: "tree"}
}

const (
	treeSmallLineThreshold = 20
	treeChildrenCap        = 12
	treeIndentUnit         = "  "
)

// treeSummaryRe matches tree's trailing "N directories, M files" summary
// line.
var treeSummaryRe = regexp.MustCompile(`^\d+\s+director(y|ies),\s+\d+\s+files?$`)

type treeNode struct {
	name     string
	children []*treeNode
}

func (f *treeFormatter) Aggressive(_ context.Context, in format.Input) (format.Rendered, error) {
	raw, err := readStdout(in)
	if err != nil {
		return format.Rendered{}, err
	}
	lines := splitLines(raw)
	if len(lines) <= treeSmallLineThreshold {
		return format.Rendered{}, format.ErrTierInapplicable
	}

	root := &treeNode{}
	stack := []*treeNode{root}
	summary := ""

	for _, l := range lines {
		trimmed := strings.TrimRight(l, " ")
		if strings.TrimSpace(trimmed) == "" {
			continue
		}
		if treeSummaryRe.MatchString(strings.TrimSpace(trimmed)) {
			summary = strings.TrimSpace(trimmed)
			continue
		}

		runes := []rune(trimmed)
		markerIdx := findTreeMarker(runes)
		if markerIdx == -1 {
			if root.name == "" {
				root.name = trimmed
				continue
			}
			// A second marker-less line is a shape this parser does not
			// understand; dropping it would silently lose entries.
			return format.Rendered{}, format.ErrTierInapplicable
		}

		depth := len(runes[:markerIdx]) / 4
		name := string(runes[markerIdx+4:])
		n := &treeNode{name: name}

		parentIdx := depth
		if parentIdx >= len(stack) {
			parentIdx = len(stack) - 1
		}
		stack = stack[:parentIdx+1]
		parent := stack[parentIdx]
		parent.children = append(parent.children, n)
		stack = append(stack, n)
	}

	var out []string
	baseDepth := 0
	if root.name != "" {
		out = append(out, root.name)
		baseDepth = 1
	}
	renderTreeNode(root, baseDepth, &out)

	if summary != "" {
		out = append(out, "", summary)
	}

	body := strings.Join(out, "\n")
	if !shrunk(raw, body) {
		return format.Rendered{}, format.ErrTierInapplicable
	}
	return format.Rendered{Body: []byte(body)}, nil
}

// findTreeMarker locates the "├── "/"└── " branch marker in runes (or the
// ASCII "|-- "/"`-- " tree prints when the locale is not UTF-8, the default in
// minimal Linux containers), returning its start index or -1 if none is
// present (e.g. the root "." line).
func findTreeMarker(runes []rune) int {
	for i := 0; i+4 <= len(runes); i++ {
		if (runes[i] == '├' || runes[i] == '└') && runes[i+1] == '─' && runes[i+2] == '─' && runes[i+3] == ' ' {
			return i
		}
		if (runes[i] == '|' || runes[i] == '`') && runes[i+1] == '-' && runes[i+2] == '-' && runes[i+3] == ' ' {
			return i
		}
	}
	return -1
}

// renderTreeNode writes n's children as two-space-indented lines, capping
// the number of children shown at treeChildrenCap.
func renderTreeNode(n *treeNode, depth int, out *[]string) {
	indent := strings.Repeat(treeIndentUnit, depth)
	for i, c := range n.children {
		if i >= treeChildrenCap {
			*out = append(*out, fmt.Sprintf("%s…+%d more", indent, len(n.children)-treeChildrenCap))
			return
		}
		if noiseDirs[c.name] && len(c.children) > 0 {
			*out = append(*out, fmt.Sprintf("%s%s …+%d entries (noise dir collapsed)", indent, c.name, countTreeDescendants(c)))
			continue
		}
		*out = append(*out, indent+c.name)
		renderTreeNode(c, depth+1, out)
	}
}

func countTreeDescendants(n *treeNode) int {
	total := len(n.children)
	for _, c := range n.children {
		total += countTreeDescendants(c)
	}
	return total
}

func (f *treeFormatter) Relaxed(ctx context.Context, in format.Input) (format.Rendered, error) {
	return relaxedRender(ctx, in)
}
