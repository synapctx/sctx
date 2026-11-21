package gradle

import (
	"fmt"
	"regexp"
	"strings"

	"github.com/synapctx/sctx/internal/domain/format"
)

var (
	buildRE    = regexp.MustCompile(`^BUILD (SUCCESSFUL|FAILED) in .*$`)
	progressRE = regexp.MustCompile(`^[.0-9%]*\.{3,}[.0-9%]*$`)
	taskRE     = regexp.MustCompile(`^> Task (\S+)(?: ([A-Z-]+))?$`)
	diagRE     = regexp.MustCompile(`^\S.*?:\d+: (error|warning): `)
	testFailRE = regexp.MustCompile(`^\S.* > .* (FAILED|SKIPPED)$`)
	testPassRE = regexp.MustCompile(`^\S.* > .* PASSED$`)
	atFrameRE  = regexp.MustCompile(`^\s+at (?:\w+//)?(\S+)`)
)

var noisePrefixes = []string{
	"Starting a Gradle Daemon", "Daemon will be stopped", "Welcome to Gradle", "Fetching distribution",
	"Downloading https://services.gradle.org", "Consider enabling configuration cache",
	"[Incubating] Problems report", "> Configure project", "> Transform", "> Starting",
	"FAILURE: Build failed with an exception", "You can use '--warning-mode all'",
	"For more on this, please refer to", "Download http", "Here are the highlights", " - ",
	"For more details see", "To honour the JVM settings", "Reusing configuration cache", "Calculating task graph",
	"Configuration cache entry", "Gradle Daemon", "Deprecated Gradle features were used",
}

type entry struct {
	text  string
	tmpl  string
	count int
}

type counters struct{ tasks, noise, frames, dup, help, passed int }

func (c counters) total() int { return c.tasks + c.noise + c.frames + c.dup + c.help + c.passed }

func (c counters) marker() string {
	var parts []string
	add := func(n int, what string) {
		if n > 0 {
			parts = append(parts, fmt.Sprintf("%d %s", n, what))
		}
	}
	add(c.tasks, "task lines")
	add(c.noise, "noise")
	add(c.frames, "framework frames")
	add(c.dup, "duplicate")
	add(c.help, "help")
	add(c.passed, "PASSED")
	return fmt.Sprintf("…+%d lines elided (%s)", c.total(), strings.Join(parts, ", "))
}

func (f *Formatter) aggressive(stdout, stderr []byte) (format.Rendered, error) {
	lines := splitLines(stdout)
	if len(lines) == 0 {
		return format.Rendered{}, format.ErrTierInapplicable
	}
	rel := f.relativizer()
	var (
		out       []*entry
		cnt       counters
		sawBuild  bool
		section   string
		inTest    bool
		skipCtx   bool
		ctxLines  int
		appFrames int
		failDrop  *entry
		seen      = map[string]bool{}
		warns     = map[string]*entry{}
	)
	add := func(e *entry) {
		out = append(out, e)
		if t := strings.TrimSpace(e.text); len(t) >= 3 {
			seen[t] = true
		}
	}
	for _, l := range lines {
		raw := rel(l)
		trimmed := strings.TrimSpace(raw)
		if buildRE.MatchString(trimmed) {
			sawBuild = true
			section, inTest, skipCtx = "", false, false
			add(&entry{text: trimmed})
			continue
		}
		if strings.HasPrefix(raw, "* ") {
			inTest, skipCtx = false, false
			switch {
			case strings.HasPrefix(raw, "* Try:"):
				section = "try"
				cnt.help++
			case strings.HasPrefix(raw, "* Where:"):
				section = "where"
				add(&entry{text: "Where:"})
			case strings.HasPrefix(raw, "* What went wrong:"):
				section = "wrong"
				add(&entry{text: "What went wrong:"})
			case strings.HasPrefix(raw, "* Exception is:"):
				section = "exc"
				add(&entry{text: "Exception:"})
			default: // "* Get more help at ..."
				section = "help"
				cnt.help++
			}
			continue
		}
		if trimmed == "" {
			inTest, skipCtx = false, false
			if section == "exc" || section == "help" {
				section = ""
			}
			continue
		}
		switch section {
		case "try", "help":
			cnt.help++
			continue
		case "wrong", "where":
			if isCaret(trimmed) || (len(trimmed) >= 3 && seen[trimmed]) {
				cnt.dup++
				continue
			}
			add(&entry{text: raw})
			continue
		case "exc":
			add(&entry{text: raw})
			continue
		}
		if m := taskRE.FindStringSubmatch(trimmed); m != nil {
			if m[2] == "FAILED" {
				add(&entry{text: "FAILED " + m[1]})
			} else {
				cnt.tasks++
			}
			continue
		}
		if isNoise(raw) || progressRE.MatchString(raw) {
			cnt.noise++
			continue
		}
		if testPassRE.MatchString(raw) {
			cnt.passed++
			continue
		}
		if testFailRE.MatchString(raw) {
			inTest, appFrames, failDrop = true, 0, nil
			add(&entry{text: raw})
			continue
		}
		if inTest && (strings.HasPrefix(raw, " ") || strings.HasPrefix(raw, "\t")) {
			if m := atFrameRE.FindStringSubmatch(raw); m != nil {
				if isFrameworkFrame(m[1]) || appFrames >= 8 {
					if failDrop == nil {
						failDrop = &entry{tmpl: "    …+%d framework frames"}
						out = append(out, failDrop)
					}
					failDrop.count++
					cnt.frames++
					continue
				}
				appFrames++
				failDrop = nil
			}
			add(&entry{text: raw})
			continue
		}
		inTest = false
		if ctxLines > 0 {
			ctxLines--
			if ctxLines == 1 || isCaret(trimmed) {
				cnt.noise++ // javac echoes the source line, then a caret line
				continue
			}
			ctxLines = 0
		}
		if diagRE.MatchString(raw) {
			skipCtx = false
			if strings.Contains(raw, ": warning: ") {
				if first, ok := warns[raw]; ok {
					first.count++
					cnt.dup++
					skipCtx, ctxLines = true, 2
					continue
				}
				e := &entry{text: raw}
				warns[raw] = e
				add(e)
				skipCtx = true
				ctxLines = 2
				continue
			}
			add(&entry{text: raw})
			continue
		}
		if skipCtx && (strings.HasPrefix(raw, " ") || strings.HasPrefix(raw, "\t")) {
			cnt.noise++
			continue
		}
		skipCtx = false
		add(&entry{text: raw})
	}
	if !sawBuild {
		return format.Rendered{}, format.ErrTierInapplicable
	}
	body := make([]string, 0, len(out)+2)
	for _, e := range out {
		switch {
		case e.tmpl != "":
			body = append(body, fmt.Sprintf(e.tmpl, e.count))
		case e.count > 0:
			body = append(body, e.text+fmt.Sprintf(" ×%d", e.count+1))
		default:
			body = append(body, e.text)
		}
	}
	if len(stderr) > 0 {
		body = append(body, strings.TrimRight(string(stderr), "\n"))
	}
	if cnt.total() > 0 {
		body = append(body, cnt.marker())
	}
	return format.Rendered{Body: []byte(strings.Join(body, "\n")), FoldStderr: len(stderr) > 0, Elided: cnt.total() > 0}, nil
}

func isCaret(t string) bool {
	return strings.Trim(t, "^~ ") == ""
}

func isNoise(raw string) bool {
	for _, p := range noisePrefixes {
		if strings.HasPrefix(raw, p) {
			return true
		}
	}
	return false
}

var frameworkPrefixes = []string{
	"org.junit.", "org.opentest4j.", "java.base/", "java.lang.", "java.util.", "jdk.", "sun.",
	"org.gradle.", "worker.org.gradle.", "junit.framework.", "org.testng.", "org.mockito.", "org.hamcrest.",
}

func isFrameworkFrame(sym string) bool {
	for _, p := range frameworkPrefixes {
		if strings.HasPrefix(sym, p) {
			return true
		}
	}
	return false
}
