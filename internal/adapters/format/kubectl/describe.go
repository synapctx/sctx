package kubectl

import (
	"fmt"
	"regexp"
	"strings"

	"github.com/synapctx/sctx/internal/adapters/format/collapse"
	"github.com/synapctx/sctx/internal/domain/format"
)

// Describe compaction. kubectl describe is the largest routine kubectl output
// and most of it is boilerplate that never explains a failure: container and
// image digests, default service-account volumes and tolerations, empty
// "<none>" fields, zeroed hugepages rows, node machine identifiers. Everything
// that carries diagnostic signal is kept verbatim: Status, State/Last State,
// Reason, Exit Code, Restart Count, Conditions, Images, Warning events.
//
// Every elision is marked: block-level elisions with an in-place `…+N` line,
// single-line drops with one trailing `…+N lines elided (<field names>)`.

const (
	describeMaxEnv          = 8 // env entries kept per container
	describeEventsKeepAll   = 8 // event tables up to this many rows are kept whole
	describeKeepNormal      = 3 // Normal events kept (newest) when a table is trimmed
	describeEventsWarnLimit = 5 // same, for a table that contains a Warning
	describeKeepNormalWarn  = 2
	describeMaxAnnotationCh = 160 // one annotation value is truncated past this
)

// droppedAlways are single-line fields that are identifiers or runtime noise.
var droppedAlways = map[string]bool{
	"Container ID": true, "Image ID": true,
	"Machine ID": true, "System UUID": true, "Boot ID": true,
}

// droppedWhenNone are fields elided only while they carry no information.
var droppedWhenNone = map[string]bool{
	"Port": true, "Host Port": true, "Node-Selectors": true, "Tolerations": true,
	"Volumes": true, "Mounts": true, "Environment": true, "Annotations": true,
	"Labels": true, "Command": true, "Args": true,
}

var zeroHugepages = regexp.MustCompile(`^\s*hugepages-\S+:?\s+(0(\s+\(0%\))?\s*)+$`)

func describeIndent(s string) int { return len(s) - len(strings.TrimLeft(s, " ")) }

func describeKey(line string) (key, val string, ok bool) {
	t := strings.TrimSpace(line)
	k, v, found := strings.Cut(t, ":")
	if !found || strings.ContainsAny(k, "\t") {
		return "", "", false
	}
	return k, strings.TrimSpace(v), true
}

// blockEnd returns the index just past the continuation lines of lines[i]:
// every following line indented deeper than it (blank lines end a block).
func blockEnd(lines []string, i int) int {
	ind := describeIndent(lines[i])
	j := i + 1
	for j < len(lines) && strings.TrimSpace(lines[j]) != "" && describeIndent(lines[j]) > ind {
		j++
	}
	return j
}

func truncAnnotation(s string) string {
	if len(s) <= describeMaxAnnotationCh {
		return s
	}
	return fmt.Sprintf("%s…+%d chars", s[:describeMaxAnnotationCh], len(s)-describeMaxAnnotationCh)
}

func compactDescribe(lines []string) []string {
	var out []string
	var dropped []string
	droppedLines := 0
	noteDrop := func(name string, n int) {
		droppedLines += n
		for _, d := range dropped {
			if d == name {
				return
			}
		}
		dropped = append(dropped, name)
	}

	var capacity []string
	for i := 0; i < len(lines); {
		line := lines[i]
		if zeroHugepages.MatchString(line) {
			noteDrop("hugepages", 1)
			i++
			continue
		}
		key, val, ok := describeKey(line)
		if !ok {
			out = append(out, line)
			i++
			continue
		}
		end := blockEnd(lines, i)
		ind := describeIndent(line)
		switch {
		case droppedAlways[key] && val != "":
			noteDrop(key, end-i)
			i = end
		case droppedWhenNone[key] && (val == "<none>" || (key == "Host Port" && val == "0/TCP")) && end == i+1:
			noteDrop(key, 1)
			i = end
		case key == "IPs" && val == "" && end-i == 2 && describeIsDupIP(out, lines[i+1]):
			noteDrop("IPs", 2)
			i = end
		case key == "Capacity" && val == "" && end > i+1:
			capacity = lines[i+1 : end]
			out = append(out, line)
			i++
		case key == "Allocatable" && val == "" && end > i+1 && capacity != nil && strings.Join(capacity, "\n") == strings.Join(lines[i+1:end], "\n"):
			out = append(out, line+" same as Capacity")
			noteDrop("Allocatable", end-i-1)
			i = end
		case key == "Tolerations" && val != "" && defaultTolerations(lines[i:end]):
			noteDrop("default Tolerations", end-i)
			i = end
		case (key == "Tolerations" || key == "Labels" || key == "Annotations") && val != "" && end > i+1:
			sep := "; "
			if key == "Labels" {
				sep = " "
			}
			parts := []string{val}
			for _, l := range lines[i+1 : end] {
				parts = append(parts, strings.TrimSpace(l))
			}
			if key == "Annotations" {
				for k := range parts {
					parts[k] = truncAnnotation(parts[k])
				}
			}
			out = append(out, fmt.Sprintf("%s%s: %s", strings.Repeat(" ", ind), key, strings.Join(parts, sep)))
			i = end
		case key == "Annotations" && val != "" && len(val) > describeMaxAnnotationCh:
			out = append(out, fmt.Sprintf("%s%s: %s", strings.Repeat(" ", ind), key, truncAnnotation(val)))
			i = end
		case key == "Volumes" && val == "" && end > i+1:
			out = append(out, line)
			out = append(out, summarizeVolumes(lines[i+1:end])...)
			i = end
		case key == "Mounts" && val == "" && end > i+1 && allServiceAccountMounts(lines[i+1:end]):
			noteDrop("Mounts(serviceaccount)", end-i)
			i = end
		case key == "Environment" && val == "" && end > i+1:
			out = append(out, line)
			out = append(out, capEnv(lines[i+1:end])...)
			i = end
		case key == "Events" && val == "" && end > i+1:
			out = append(out, line)
			out = append(out, trimEventRows(lines[i+1:end])...)
			i = end
		default:
			out = append(out, line)
			i++
		}
	}
	if droppedLines > 0 {
		out = append(out, fmt.Sprintf("…+%d lines elided (%s)", droppedLines, strings.Join(dropped, ", ")))
	}
	return out
}

// describeIsDupIP reports whether the single `IP: x` child of an IPs block
// repeats an `IP:` line already emitted.
func describeIsDupIP(out []string, child string) bool {
	_, v, ok := describeKey(child)
	if !ok {
		return false
	}
	for k := len(out) - 1; k >= 0 && k >= len(out)-12; k-- {
		if key, val, ok := describeKey(out[k]); ok && key == "IP" && val == v {
			return true
		}
	}
	return false
}

var defaultToleration = regexp.MustCompile(`^node\.kubernetes\.io/(not-ready|unreachable):NoExecute op=Exists for 300s$`)

// defaultTolerations reports whether a Tolerations block holds only the two
// tolerations the API server injects into every pod.
func defaultTolerations(block []string) bool {
	for n, l := range block {
		t := strings.TrimSpace(l)
		if n == 0 {
			_, t, _ = describeKey(l)
		}
		if !defaultToleration.MatchString(t) {
			return false
		}
	}
	return true
}

func allServiceAccountMounts(children []string) bool {
	for _, l := range children {
		if !strings.Contains(l, "/var/run/secrets/kubernetes.io/serviceaccount") {
			return false
		}
	}
	return true
}

// summarizeVolumes renders each volume as `name: Type` and marks the dropped
// detail lines.
func summarizeVolumes(children []string) []string {
	if len(children) == 0 {
		return nil
	}
	ind := describeIndent(children[0])
	var out []string
	dropped := 0
	for i := 0; i < len(children); {
		if describeIndent(children[i]) != ind {
			i++
			dropped++
			continue
		}
		name, _, _ := describeKey(children[i])
		j := i + 1
		typ := ""
		for j < len(children) && describeIndent(children[j]) > ind {
			if k, v, ok := describeKey(children[j]); ok && k == "Type" && typ == "" {
				typ = v
			}
			j++
		}
		if typ == "" {
			out = append(out, children[i:j]...)
		} else {
			if p := strings.Index(typ, " ("); p > 0 {
				typ = typ[:p]
			}
			out = append(out, fmt.Sprintf("%s%s: %s", strings.Repeat(" ", ind), name, typ))
			dropped += j - i - 1
		}
		i = j
	}
	if dropped > 0 {
		out = append(out, fmt.Sprintf("%s…+%d volume detail lines", strings.Repeat(" ", ind), dropped))
	}
	return out
}

func capEnv(children []string) []string {
	ind := describeIndent(children[0])
	entries, i := 0, 0
	for ; i < len(children); i++ {
		if describeIndent(children[i]) == ind {
			if entries == describeMaxEnv {
				break
			}
			entries++
		}
	}
	if i >= len(children) {
		return children
	}
	rest := 0
	for _, l := range children[i:] {
		if describeIndent(l) == ind {
			rest++
		}
	}
	out := append([]string(nil), children[:i]...)
	return append(out, fmt.Sprintf("%s…+%d more env vars", strings.Repeat(" ", ind), rest))
}

// trimEventRows keeps the header, every Warning and the newest few Normal
// events once a table is long, marking the Normal rows it dropped.
func trimEventRows(children []string) []string {
	var head, rows []string
	for _, l := range children {
		f := strings.Fields(l)
		if len(f) > 0 && (f[0] == "Type" || strings.HasPrefix(f[0], "---")) {
			head = append(head, l)
			continue
		}
		rows = append(rows, l)
	}
	normals, warnings := 0, 0
	for _, r := range rows {
		if f := strings.Fields(r); len(f) == 0 || f[0] != "Warning" {
			normals++
		} else {
			warnings++
		}
	}
	// A table that holds a Warning is diagnostic: the Normal rows around it
	// (Scheduled/Pulling/Created/Started) are lifecycle chatter.
	limit, keep := describeEventsKeepAll, describeKeepNormal
	if warnings > 0 {
		limit, keep = describeEventsWarnLimit, describeKeepNormalWarn
	}
	if len(rows) <= limit {
		return children
	}
	skip := normals - keep
	if skip <= 0 {
		return children
	}
	out := append([]string(nil), head...)
	seen := 0
	for _, r := range rows {
		if f := strings.Fields(r); len(f) == 0 || f[0] != "Warning" {
			seen++
			if seen <= skip {
				continue
			}
		}
		out = append(out, r)
	}
	return append(out, fmt.Sprintf("%s…+%d Normal events elided", strings.Repeat(" ", describeIndent(children[0])), skip))
}

// aggressiveDescribe compacts boilerplate (see the block comment above) and
// collapses identical consecutive lines. It declines when nothing shrank.
func aggressiveDescribe(in format.Input) (format.Rendered, error) {
	raw := readAll(in.Stdout)
	src := collapse.SplitLines(raw)
	compact := compactDescribe(src)
	lines, runChanged := collapse.Runs(compact)
	body := strings.Join(lines, "\n")
	if !runChanged && len(body) >= len(strings.TrimRight(string(raw), "\n")) {
		return format.Rendered{}, format.ErrTierInapplicable
	}
	return format.Rendered{Body: []byte(body)}, nil
}
