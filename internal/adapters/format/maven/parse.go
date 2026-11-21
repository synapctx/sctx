package maven

import (
	"fmt"
	"regexp"
	"strings"

	"github.com/synapctx/sctx/internal/domain/format"
)

var (
	buildResultRE  = regexp.MustCompile(`^\[(?:INFO|ERROR)\] BUILD (SUCCESS|FAILURE)\s*$`)
	moduleBannerRE = regexp.MustCompile(`^\[INFO\] -+\[? ?<? ?[^\]]*>?-+\s*$|^\[INFO\] -{5,}< .+ >-{5,}$`)
	buildingRE     = regexp.MustCompile(`^\[INFO\] Building (\S.*?)\s+(\S+?)(?:\s+\[(\d+/\d+)\])?\s*$`)
	goalRE         = regexp.MustCompile(`^\[INFO\] --- ([^:\s]+):[^:\s]+:([^\s]+) \(.*\) @ .+ ---$|^\[INFO\] --- ([^:\s]+):([^:\s]+) \(.*\) @ .+ ---$`)
	reactorRowRE   = regexp.MustCompile(`^\[(?:INFO|ERROR|WARNING)\] (\S.*?) \.{2,} (SUCCESS|FAILURE|SKIPPED)(?: \[\s*([^\]]+?)\s*\])?\s*$`)
	orderRowRE     = regexp.MustCompile(`^\[INFO\] \S.*\s\[[a-z-]+\]\s*$`)
	passClassRE    = regexp.MustCompile(`^\[INFO\] Tests run: \d+, Failures: 0, Errors: 0, Skipped: \d+, Time elapsed: .* -- in (\S+)\s*$`)
	failClassRE    = regexp.MustCompile(`^\[ERROR\] Tests run: .*<<< (?:FAILURE|ERROR)! -- in \S+\s*$`)
	failHeaderRE   = regexp.MustCompile(`^\[ERROR\] (\S+?)\.([^\s.]+) -- Time elapsed: .* <<< (FAILURE|ERROR)!\s*$`)
	summaryEntryRE = regexp.MustCompile(`^\[ERROR\]   (\S+?)\.([\w$]+)[^\s:»]*(?::\d+| »).*$`)
	javacLocRE     = regexp.MustCompile(`^\[(?:ERROR|WARNING)\] \S+:\[\d+,\d+\] `)
	runningRE      = regexp.MustCompile(`^\[INFO\] Running (\S+)\s*$`)
	helpRE         = regexp.MustCompile(`^\[ERROR\] (?:-> \[Help \d+\]|\[Help \d+\] .*|To see the full stack trace.*|Re-run Maven using.*|For more information about the errors.*|After correcting the problems.*|See dump files.*)$`)
	atFrameRE      = regexp.MustCompile(`^\s+at (\S+)`)
	timeRE         = regexp.MustCompile(`^\[INFO\] Total time:\s+(.*)$`)
)

// noiseInfo are [INFO] lines that carry no signal a reader can act on.
var noiseInfo = []string{
	"Scanning for projects", "  from ", "Loaded ", "skip non existing resourceDirectory",
	"Using auto detected provider", " T E S T S", "Results:", "Nothing to compile",
	"Finished at:", "Reactor Build Order:", "Recompiling the module", "Changes detected",
	"Latest version of plugin", "Looking for compatible RELEASE", "Compiling ",
	"Copying ", "Using ", "No sources to compile", "Reactor Summary",
}

type entry struct {
	text  string
	tmpl  string // when set, rendered as fmt.Sprintf(tmpl, count)
	count int
	key   string // dedupe key for warning/error blocks
	kind  byte   // 'W', 'E' or 0
}

type counters struct {
	download, noise, frames, dup, help int
}

func (c counters) total() int { return c.download + c.noise + c.frames + c.dup + c.help }

func (c counters) marker() string {
	var parts []string
	add := func(n int, what string) {
		if n > 0 {
			parts = append(parts, fmt.Sprintf("%d %s", n, what))
		}
	}
	add(c.download, "download")
	add(c.noise, "noise")
	add(c.frames, "framework frames")
	add(c.dup, "duplicate")
	add(c.help, "help")
	return fmt.Sprintf("…+%d lines elided (%s)", c.total(), strings.Join(parts, ", "))
}

type modEntry struct {
	header string
	goals  []string
}

func (f *Formatter) aggressive(stdout, stderr []byte) (format.Rendered, error) {
	lines, _ := splitLog(stdout)
	if len(lines) == 0 {
		return format.Rendered{}, format.ErrTierInapplicable
	}
	rel := f.relativizer()
	var (
		out         []*entry
		mods        = map[*entry]*modEntry{}
		curMod      *modEntry
		cnt         counters
		sawBuild    bool
		afterBanner bool
		inOrder     bool
		inReactor   bool
		reactor     []string
		reactorAt   *entry
		inFail      bool
		failDrop    *entry
		appFrames   int
		pendingRun  string
		seenFail    = map[string]bool{}
		seenKeys    = map[string]*entry{}
		prev        *entry // last kept entry, for continuation lines
	)
	keep := func(e *entry) {
		if pendingRun != "" && e.kind == 0 && !strings.HasPrefix(e.text, "[") {
			out = append(out, &entry{text: "Running " + pendingRun})
		}
		out = append(out, e)
		prev = e
	}
	for _, ll := range lines {
		if ll.download {
			cnt.download++
			continue
		}
		raw := rel(ll.text)
		switch {
		case buildResultRE.MatchString(raw):
			sawBuild = true
			inFail, inReactor = false, false
			keep(&entry{text: strings.TrimPrefix(strings.TrimPrefix(raw, "[INFO] "), "[ERROR] ")})
			continue
		}
		// Surefire failure block: header, message lines, frames until blank.
		if m := failHeaderRE.FindStringSubmatch(raw); m != nil {
			inFail, failDrop, appFrames = true, nil, 0
			cls := m[1]
			seenFail[cls[strings.LastIndex(cls, ".")+1:]+"."+m[2]] = true
			label := "FAIL"
			if m[3] == "ERROR" {
				label = "ERROR"
			}
			keep(&entry{text: label + " " + cls + "." + m[2]})
			continue
		}
		if inFail {
			if strings.TrimSpace(raw) == "" || strings.HasPrefix(raw, "[") {
				inFail = false
			} else {
				if m := atFrameRE.FindStringSubmatch(raw); m != nil {
					if isFrameworkFrame(m[1]) || appFrames >= 8 {
						if failDrop == nil {
							failDrop = &entry{tmpl: "\t…+%d framework frames"}
							out = append(out, failDrop)
						}
						failDrop.count++
						cnt.frames++
						continue
					}
					appFrames++
					failDrop = nil
				}
				keep(&entry{text: raw})
				continue
			}
		}
		if m := runningRE.FindStringSubmatch(raw); m != nil {
			pendingRun = m[1]
			cnt.noise++
			continue
		}
		if passClassRE.MatchString(raw) {
			pendingRun = ""
			cnt.noise++
			continue
		}
		if failClassRE.MatchString(raw) {
			pendingRun = ""
			cnt.noise++
			continue
		}
		if m := moduleBannerRE.FindString(raw); m != "" && strings.Contains(raw, "<") {
			afterBanner = true
			cnt.noise++
			continue
		}
		if m := buildingRE.FindStringSubmatch(raw); m != nil && afterBanner {
			afterBanner = false
			me := &modEntry{header: m[1] + " " + m[2]}
			if m[3] != "" {
				me.header += " [" + m[3] + "]"
			}
			curMod = me
			e := &entry{}
			mods[e] = me
			out = append(out, e)
			prev = e
			cnt.noise++
			continue
		}
		afterBanner = false
		if m := goalRE.FindStringSubmatch(raw); m != nil {
			plugin, goal := m[1], m[2]
			if plugin == "" {
				plugin, goal = m[3], m[4]
			}
			g := plugin
			if goal != plugin {
				g = plugin + ":" + goal
			}
			if curMod != nil {
				curMod.goals = append(curMod.goals, g)
			}
			cnt.noise++
			continue
		}
		if strings.HasPrefix(raw, "[INFO] Reactor Build Order:") {
			inOrder = true
			cnt.noise++
			continue
		}
		if inOrder {
			if orderRowRE.MatchString(raw) || raw == "[INFO] " || raw == "[INFO]" {
				cnt.noise++
				continue
			}
			inOrder = false
		}
		if strings.HasPrefix(raw, "[INFO] Reactor Summary") {
			inReactor = true
			reactorAt = &entry{}
			out = append(out, reactorAt)
			cnt.noise++
			continue
		}
		if m := reactorRowRE.FindStringSubmatch(raw); m != nil && inReactor {
			s := m[1] + " " + m[2]
			if m[3] != "" {
				s += " " + strings.ReplaceAll(m[3], " ", "")
			}
			reactor = append(reactor, s)
			continue
		}
		if helpRE.MatchString(raw) {
			if strings.Contains(raw, "After correcting the problems") {
				cnt.help++
				continue
			}
			cnt.help++
			continue
		}
		if strings.HasPrefix(raw, "[ERROR] Failures:") || strings.HasPrefix(raw, "[ERROR] Errors:") || strings.HasPrefix(raw, "[ERROR] Skipped:") {
			cnt.noise++
			continue
		}
		if m := summaryEntryRE.FindStringSubmatch(raw); m != nil && seenFail[m[1]+"."+m[2]] {
			cnt.dup++
			continue
		}
		if trimmed := strings.TrimSpace(raw); trimmed == "[INFO]" || trimmed == "[WARNING]" || trimmed == "[ERROR]" || trimmed == "" {
			cnt.noise++
			continue
		}
		if strings.HasPrefix(raw, "[INFO] ") {
			if isNoiseInfo(raw[len("[INFO] "):]) || strings.HasPrefix(raw, "[INFO] ---") {
				cnt.noise++
				continue
			}
			if m := timeRE.FindStringSubmatch(raw); m != nil {
				keep(&entry{text: "Total time: " + m[1]})
				continue
			}
			keep(&entry{text: strings.TrimPrefix(raw, "[INFO] ")})
			continue
		}
		if strings.Contains(raw, "Some problems were encountered") ||
			strings.HasPrefix(raw, "[WARNING] It is highly recommended to fix these problems") ||
			strings.HasPrefix(raw, "[WARNING] For this reason, future Maven versions") {
			cnt.noise++
			continue
		}
		if strings.HasPrefix(raw, "[WARNING] ") || strings.HasPrefix(raw, "[ERROR] ") {
			// Compiler continuation lines ("  symbol: ...") ride on their
			// diagnostic; Maven repeats them inside "Failed to execute goal".
			if prev != nil && prev.key != "" && javacCont(raw) {
				prev.text += "\n" + strings.TrimPrefix(strings.TrimPrefix(raw, "[ERROR]"), "[WARNING]")
				prev.key += "\n" + strings.TrimSpace(raw[strings.Index(raw, "]")+1:])
				continue
			}
			kind := byte('W')
			if raw[1] == 'E' {
				kind = 'E'
			}
			e := &entry{text: raw, kind: kind}
			if kind == 'W' || javacLocRE.MatchString(raw) {
				e.key = raw
			}
			keep(e)
			continue
		}
		// Unprefixed text: test stdout, stack-trace tails, javac continuation.
		if prev != nil && prev.kind != 0 {
			prev.text += "\n" + raw
			if prev.key != "" {
				prev.key += "\n" + strings.TrimSpace(raw)
			}
			continue
		}
		keep(&entry{text: raw})
	}
	if !sawBuild {
		return format.Rendered{}, format.ErrTierInapplicable
	}
	// A diagnostic block's key is final only once its continuation lines
	// arrived, so duplicates (Maven repeats every compiler error inside
	// "Failed to execute goal") are removed after the scan.
	kept := out[:0]
	for _, e := range out {
		if e.key != "" {
			if first, ok := seenKeys[e.key]; ok {
				first.count++
				cnt.dup += 1 + strings.Count(e.text, "\n")
				continue
			}
			seenKeys[e.key] = e
		}
		kept = append(kept, e)
	}
	out = kept
	var body []string
	for _, e := range out {
		if me, ok := mods[e]; ok {
			h := me.header
			if len(me.goals) > 0 {
				h += " (" + strings.Join(me.goals, " ") + ")"
			}
			body = append(body, h)
			continue
		}
		if e == reactorAt {
			body = append(body, "Reactor: "+strings.Join(reactor, "; "))
			continue
		}
		switch {
		case e.tmpl != "":
			body = append(body, fmt.Sprintf(e.tmpl, e.count))
		case e.count > 0 && e.kind == 'W':
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
	return format.Rendered{Body: joinBody(body), FoldStderr: len(stderr) > 0, Elided: cnt.total() > 0}, nil
}

func javacCont(raw string) bool {
	t := strings.TrimSpace(raw[strings.Index(raw, "]")+1:])
	return strings.HasPrefix(t, "symbol:") || strings.HasPrefix(t, "location:")
}

func isNoiseInfo(s string) bool {
	for _, p := range noiseInfo {
		if strings.HasPrefix(s, p) {
			return true
		}
	}
	return false
}

var frameworkPrefixes = []string{
	"org.junit.", "org.opentest4j.", "java.base/", "java.lang.", "java.util.", "jdk.", "sun.",
	"org.apache.maven.surefire.", "org.testng.", "junit.framework.", "org.mockito.", "org.hamcrest.",
}

func isFrameworkFrame(sym string) bool {
	for _, p := range frameworkPrefixes {
		if strings.HasPrefix(sym, p) {
			return true
		}
	}
	return false
}
