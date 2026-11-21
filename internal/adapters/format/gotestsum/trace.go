package gotestsum

import (
	"fmt"
	"regexp"
	"strings"
)

var (
	goroutineRE = regexp.MustCompile(`^goroutine \d+ \[.*\]:$`)
	fileLineRE  = regexp.MustCompile(`^(\t| {4,})\S.*\.go:\d+`)
	// stdlibFuncRE names frames that belong to the Go toolchain rather than
	// the code under test.
	stdlibFuncRE = regexp.MustCompile(`^(created by )?(testing|runtime|time|sync|os|reflect|internal/[\w/]+|syscall|panic)[.(]`)
	stdlibFileRE = regexp.MustCompile(`/(go|libexec)/src/(testing|runtime|time|sync|os|internal|reflect|syscall)/|_testmain\.go:`)
)

func stdlibFrame(fn, file string) bool {
	return stdlibFuncRE.MatchString(strings.TrimSpace(fn)) || stdlibFileRE.MatchString(file)
}

// compactStack drops toolchain frames (func line plus file:line line) from
// panic, timeout and race reports, leaving the user's frames and every other
// line untouched. A run of dropped frames becomes one counted marker, and a
// goroutine whose frames were all toolchain collapses to its header line.
// It returns the lines and the number of frames dropped.
func compactStack(lines []string) ([]string, int) {
	var out []string
	total := 0
	i := 0
	for i < len(lines) {
		l := lines[i]
		if goroutineRE.MatchString(l) {
			j := i + 1
			for j < len(lines) && strings.TrimSpace(lines[j]) != "" && !goroutineRE.MatchString(lines[j]) {
				j++
			}
			body, dropped := compactFrames(lines[i+1 : j])
			total += dropped
			if dropped > 0 && len(body) == 1 && strings.Contains(body[0], "stdlib frames") {
				out = append(out, fmt.Sprintf("%s …+%d stdlib frames", l, dropped))
			} else {
				out = append(out, l)
				out = append(out, body...)
			}
			i = j
			continue
		}
		j := i
		for j < len(lines) && !goroutineRE.MatchString(lines[j]) {
			j++
		}
		body, dropped := compactFrames(lines[i:j])
		total += dropped
		out = append(out, body...)
		i = j
	}
	return out, total
}

func compactFrames(lines []string) ([]string, int) {
	var out []string
	run, total := 0, 0
	flush := func(indent string) {
		if run > 0 {
			out = append(out, fmt.Sprintf("%s…+%d stdlib frames", indent, run))
			run = 0
		}
	}
	for i := 0; i < len(lines); i++ {
		l := lines[i]
		if i+1 < len(lines) && !fileLineRE.MatchString(l) && fileLineRE.MatchString(lines[i+1]) {
			if stdlibFrame(l, lines[i+1]) {
				run++
				total++
				i++
				continue
			}
			flush("\t")
			out = append(out, l, lines[i+1])
			i++
			continue
		}
		flush("\t")
		out = append(out, l)
	}
	flush("\t")
	return out, total
}
