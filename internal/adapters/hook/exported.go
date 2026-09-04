package hook

import (
	"regexp"
	"sort"
	"strings"
)

// Proactive guidance v2, blast radius (plan D4): after an Edit/Write, ask
// "did this change something another repository might call" ONLY when the
// answer could plausibly be yes — a changed EXPORTED Go declaration, never an
// unexported one, never another language yet. Go first because it is the one
// language this org's cross-repository call graph already indexes with any
// confidence; a false positive here costs a pointless round trip and an
// irrelevant line in an agent's context, so when in doubt this says nothing.
//
// declRE matches a top-level func, method or type declaration line. The
// receiver group is optional so one pattern covers both a free function
// (`func Foo(`) and a method (`func (r *Type) Foo(`) — the name that changed
// is always the one right before the parameter list opens.
var declRE = regexp.MustCompile(`^\s*func\s+(?:\([^)]*\)\s*)?([A-Za-z_][A-Za-z0-9_]*)\s*[\[(]`)

// typeRE matches a top-level type declaration line: `type Foo struct {`,
// `type Foo interface {`, `type Foo = Bar`, `type Foo int`.
var typeRE = regexp.MustCompile(`^\s*type\s+([A-Za-z_][A-Za-z0-9_]*)\b`)

// changedExportedSymbols diffs the declaration lines of an Edit/Write's
// old_string and new_string and returns the names of EXPORTED func/method/type
// declarations that are new or whose line text changed, capped at 3 and in a
// stable order. Only .go files are considered — every other language returns
// nil, on purpose, until this org's graph indexes their declarations with the
// same confidence.
func changedExportedSymbols(filePath, oldString, newString string) []string {
	if !strings.HasSuffix(filePath, ".go") {
		return nil
	}
	oldDecls := extractDecls(oldString)
	newDecls := extractDecls(newString)

	var names []string
	for name, line := range newDecls {
		if !isExportedName(name) {
			continue
		}
		if oldLine, ok := oldDecls[name]; ok && oldLine == line {
			continue // unchanged declaration line: nothing to say about it
		}
		names = append(names, name)
	}
	sort.Strings(names)
	if len(names) > 3 {
		names = names[:3]
	}
	return names
}

// extractDecls maps a declaration name to its (trimmed) source line, scanning
// line by line — a full parse is not needed to answer "did the signature
// line change", and a parse failure on a mid-edit fragment (old_string and
// new_string are rarely valid standalone Go) would silently lose every
// declaration in it.
func extractDecls(text string) map[string]string {
	decls := map[string]string{}
	if text == "" {
		return decls
	}
	for line := range strings.SplitSeq(text, "\n") {
		trimmed := strings.TrimRight(line, " \t\r")
		if m := declRE.FindStringSubmatch(trimmed); m != nil {
			decls[m[1]] = strings.TrimSpace(trimmed)
			continue
		}
		if m := typeRE.FindStringSubmatch(trimmed); m != nil {
			decls[m[1]] = strings.TrimSpace(trimmed)
		}
	}
	return decls
}

// isExportedName is Go's own export rule: the first rune is an uppercase
// letter.
func isExportedName(name string) bool {
	if name == "" {
		return false
	}
	r := name[0]
	return r >= 'A' && r <= 'Z'
}
