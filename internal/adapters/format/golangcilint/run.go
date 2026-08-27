package golangcilint

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"

	"github.com/synapctx/sctx/internal/domain/format"
)

var (
	issueWithLinterRE = regexp.MustCompile(`^(.+\.go):(\d+):(\d+): (.+) \(([A-Za-z0-9_-]+)\)\s*$`)
	issueNoLinterRE   = regexp.MustCompile(`^(.+\.go):(\d+):(\d+): (.+?)\s*$`)
	summaryRE         = regexp.MustCompile(`^(\d+) issues?:\s*$`)
	statRE            = regexp.MustCompile(`^\* ([A-Za-z0-9_-]+): (\d+)\s*$`)
	caretRE           = regexp.MustCompile(`^[\t ]*[\^~]+[\t \^~]*$`)
	ansiRE            = regexp.MustCompile(`\x1b\[[0-?]*[ -/]*[@-~]`)
)

var machineOutputNames = []string{
	"json", "tab", "html", "checkstyle", "code-climate", "junit-xml", "teamcity", "sarif",
}

type issue struct {
	file, line, col, message, linter string
	contextLines                     int
}

type parsedRun struct {
	issues        []issue
	order         []string
	reportedTotal *int
	stats         map[string]int
	statOrder     []string
	additional    []string
}

func requestsMachineOutputOnCapturedStream(argv []string) bool {
	// v1 used one --out-format flag for both the format and optional output
	// destination. Its grammar differs materially from v2, so preserve every
	// explicit v1 output contract rather than guessing whether it is human text.
	if _, ok := optionValue(argv, "--out-format"); ok {
		return true
	}
	for _, name := range machineOutputNames {
		if value, ok := optionValue(argv, "--output."+name+".path"); ok {
			switch strings.ToLower(value) {
			case "stdout", "stderr":
				return true
			}
		}
	}
	return false
}

func optionValue(argv []string, name string) (string, bool) {
	for i := 1; i < len(argv); i++ {
		if argv[i] == name && i+1 < len(argv) {
			return argv[i+1], true
		}
		if after, ok := strings.CutPrefix(argv[i], name+"="); ok {
			return after, true
		}
	}
	return "", false
}

func optionIsFalse(argv []string, name string) bool {
	value, ok := optionValue(argv, name)
	return ok && strings.EqualFold(value, "false")
}

func cleanForParsing(line string) string {
	return ansiRE.ReplaceAllString(line, "")
}

func parseRunLines(lines []string, allowMissingLinter bool, parsed *parsedRun, seen map[string]bool) bool {
	for i := 0; i < len(lines); i++ {
		original := lines[i]
		line := cleanForParsing(original)
		var m []string
		if allowMissingLinter {
			m = issueNoLinterRE.FindStringSubmatch(line)
		} else {
			m = issueWithLinterRE.FindStringSubmatch(line)
		}
		if m != nil {
			is := issue{file: m[1], line: m[2], col: m[3], message: m[4]}
			if !allowMissingLinter {
				is.linter = m[5]
			}
			if i+2 < len(lines) && caretRE.MatchString(cleanForParsing(lines[i+2])) {
				is.contextLines = 2
				i += 2
			}
			parsed.issues = append(parsed.issues, is)
			if !seen[is.file] {
				seen[is.file] = true
				parsed.order = append(parsed.order, is.file)
			}
			continue
		}
		if m = summaryRE.FindStringSubmatch(line); m != nil {
			total, err := strconv.Atoi(m[1])
			if err != nil || parsed.reportedTotal != nil {
				return false
			}
			parsed.reportedTotal = &total
			continue
		}
		if m = statRE.FindStringSubmatch(line); m != nil {
			count, err := strconv.Atoi(m[2])
			if err != nil {
				return false
			}
			if _, exists := parsed.stats[m[1]]; exists {
				return false
			}
			parsed.stats[m[1]] = count
			parsed.statOrder = append(parsed.statOrder, m[1])
			continue
		}
		if strings.TrimSpace(line) != "" {
			parsed.additional = append(parsed.additional, original)
		}
	}
	return true
}

func parseRun(in format.Input) (parsedRun, bool, bool) {
	rawStdout := readAll(in.Stdout)
	rawStderr := readAll(in.Stderr)
	parsed := parsedRun{stats: make(map[string]int)}
	seen := make(map[string]bool)
	allowMissingLinter := optionIsFalse(in.Argv, "--output.text.print-linter-name")
	if !parseRunLines(splitLines(rawStdout), allowMissingLinter, &parsed, seen) ||
		!parseRunLines(splitLines(rawStderr), allowMissingLinter, &parsed, seen) || len(parsed.issues) == 0 {
		return parsedRun{}, false, false
	}
	// Type-check failures carry compiler framing and source excerpts that do not
	// follow the ordinary issue/context grammar. Until that framing has its own
	// parser, keep the complete native build diagnostic verbatim.
	for _, is := range parsed.issues {
		if is.linter == "typecheck" {
			return parsedRun{}, false, false
		}
	}
	if parsed.stats["typecheck"] > 0 {
		return parsedRun{}, false, false
	}
	if parsed.reportedTotal != nil && *parsed.reportedTotal != len(parsed.issues) {
		return parsedRun{}, false, false
	}
	if len(parsed.stats) > 0 {
		statTotal := 0
		for _, count := range parsed.stats {
			statTotal += count
		}
		if statTotal != len(parsed.issues) {
			return parsedRun{}, false, false
		}
		if !allowMissingLinter {
			actual := make(map[string]int)
			for _, is := range parsed.issues {
				actual[is.linter]++
			}
			if len(actual) != len(parsed.stats) {
				return parsedRun{}, false, false
			}
			for name, count := range actual {
				if parsed.stats[name] != count {
					return parsedRun{}, false, false
				}
			}
		}
	}
	return parsed, true, len(rawStderr) > 0
}

func aggressiveRun(in format.Input) (format.Rendered, error) {
	parsed, ok, foldStderr := parseRun(in)
	if !ok {
		return format.Rendered{}, format.ErrTierInapplicable
	}

	byFile := make(map[string][]issue)
	for _, is := range parsed.issues {
		byFile[is.file] = append(byFile[is.file], is)
	}

	var b strings.Builder
	for _, file := range parsed.order {
		renderFile(&b, file, byFile[file])
	}
	fmt.Fprintf(&b, "%d %s in %d %s", len(parsed.issues), plural(len(parsed.issues), "issue", "issues"), len(parsed.order), plural(len(parsed.order), "file", "files"))
	if len(parsed.statOrder) > 0 {
		b.WriteString("\nlinters:")
		for _, name := range parsed.statOrder {
			fmt.Fprintf(&b, " %s=%d", name, parsed.stats[name])
		}
	}
	if len(parsed.additional) > 0 {
		b.WriteString("\nadditional output:")
		for _, line := range parsed.additional {
			b.WriteByte('\n')
			b.WriteString(line)
		}
	}

	return format.Rendered{Body: []byte(b.String()), FoldStderr: foldStderr}, nil
}

func renderFile(b *strings.Builder, file string, issues []issue) {
	byLinter := make(map[string][]issue)
	var linterOrder []string
	for _, is := range issues {
		if _, exists := byLinter[is.linter]; !exists {
			linterOrder = append(linterOrder, is.linter)
		}
		byLinter[is.linter] = append(byLinter[is.linter], is)
	}

	if len(linterOrder) == 1 && linterOrder[0] != "" {
		fmt.Fprintf(b, "%s — %d %s (%s)\n", file, len(issues), plural(len(issues), "issue", "issues"), linterOrder[0])
		for _, is := range issues {
			renderIssue(b, is, "  ")
		}
		return
	}

	fmt.Fprintf(b, "%s — %d %s\n", file, len(issues), plural(len(issues), "issue", "issues"))
	if len(linterOrder) == 1 {
		for _, is := range issues {
			renderIssue(b, is, "  ")
		}
		return
	}
	for _, linter := range linterOrder {
		group := byLinter[linter]
		fmt.Fprintf(b, "  %s — %d\n", linter, len(group))
		for _, is := range group {
			renderIssue(b, is, "    ")
		}
	}
}

func renderIssue(b *strings.Builder, is issue, indent string) {
	fmt.Fprintf(b, "%sL%s:%s %s", indent, is.line, is.col, is.message)
	if is.contextLines > 0 {
		fmt.Fprintf(b, " …+%d context", is.contextLines)
	}
	b.WriteByte('\n')
}

func plural(n int, singular, plural string) string {
	if n == 1 {
		return singular
	}
	return plural
}
