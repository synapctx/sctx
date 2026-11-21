package systemd

import (
	"fmt"
	"regexp"
	"strings"
)

var (
	unitRowRE = regexp.MustCompile(`^(?:[●○×↻◌⏺] |  )(\S+)\s+(loaded|not-found|masked|error|bad-setting|stub|merged)\s+(\S+)\s+(\S+)(?:\s+(.*))?$`)
	fileRowRE = regexp.MustCompile(`^(\S+)\s+(enabled|enabled-runtime|linked|linked-runtime|alias|masked|masked-runtime|static|indirect|disabled|generated|transient|bad)(?:\s+(\S+))?\s*$`)
	legendRE  = regexp.MustCompile(`^(Legend:|        (LOAD|ACTIVE|SUB)\s+→)`)
	hintRE    = regexp.MustCompile(`^To show all installed unit files use `)
)

type groupKey struct{ active, sub, typ string }

// renderListUnits groups healthy units (loaded + active, or loaded + inactive)
// by (state, type) and keeps every other row in full.
func renderListUnits(lines []string, aggressive bool) result {
	var (
		verbatim []string
		order    []groupKey
		groups   = map[groupKey][]string{}
		seen     int
		tail     []string
		descs    int
		chrome   int
	)
	for _, l := range lines {
		if strings.TrimSpace(l) == "" {
			continue
		}
		trim := strings.TrimSpace(l)
		if strings.HasPrefix(trim, "UNIT ") {
			continue // header; the grouped lines below carry their own labels
		}
		if legendRE.MatchString(l) || hintRE.MatchString(l) {
			chrome++
			continue
		}
		m := unitRowRE.FindStringSubmatch(l)
		if m == nil {
			tail = append(tail, l)
			continue
		}
		seen++
		unit, load, active, sub := m[1], m[2], m[3], m[4]
		healthy := strings.HasPrefix(l, "  ") && load == "loaded" && (active == "active" || active == "inactive")
		if !aggressive || !healthy {
			verbatim = append(verbatim, strings.Join(strings.Fields(l), " "))
			continue
		}
		name, typ := splitUnit(unit)
		if name == "-" {
			name = unit // -.mount and -.slice: keep the suffix or the name reads as a bullet
		}
		k := groupKey{active, sub, typ}
		if _, ok := groups[k]; !ok {
			order = append(order, k)
		}
		groups[k] = append(groups[k], name)
		descs++
	}
	if seen == 0 {
		return result{}
	}
	out := append([]string{}, verbatim...)
	for _, k := range order {
		names := groups[k]
		out = append(out, fmt.Sprintf("%s/%s %s ×%d: %s", k.active, k.sub, k.typ, len(names), strings.Join(names, " ")))
	}
	out = append(out, tail...)
	if descs+chrome > 0 {
		out = append(out, fmt.Sprintf("…+%d omitted: %d descriptions of healthy units, %d legend/hint lines", descs+chrome, descs, chrome))
	}
	return result{out: out, elided: descs + chrome, ok: true}
}

func splitUnit(u string) (name, typ string) {
	i := strings.LastIndexByte(u, '.')
	if i <= 0 {
		return u, "unit"
	}
	return u[:i], u[i+1:]
}

// quietStates are unit-file states whose names are not worth listing: they are
// not something an operator enabled or disabled.
var quietStates = map[string]bool{"static": true, "indirect": true, "alias": true, "generated": true, "transient": true}

func renderListUnitFiles(lines []string, aggressive bool) result {
	var (
		order  []string
		groups = map[string][]string{}
		tail   []string
		seen   int
		elided int
	)
	for _, l := range lines {
		if strings.TrimSpace(l) == "" {
			continue
		}
		if strings.HasPrefix(l, "UNIT FILE") {
			continue
		}
		m := fileRowRE.FindStringSubmatch(l)
		if m == nil {
			tail = append(tail, l)
			continue
		}
		seen++
		unit, state, preset := m[1], m[2], m[3]
		if !aggressive {
			tail = append(tail, strings.Join(strings.Fields(l), " "))
			continue
		}
		if _, ok := groups[state]; !ok {
			order = append(order, state)
		}
		item := unit
		if preset != "" && preset != "-" && preset != state && !strings.HasPrefix(preset, state) {
			item += "(preset:" + preset + ")"
		}
		groups[state] = append(groups[state], item)
	}
	if seen == 0 {
		return result{}
	}
	var out []string
	for _, st := range order {
		names := groups[st]
		if quietStates[st] {
			out = append(out, fmt.Sprintf("%s ×%d (names omitted; list with --state=%s)", st, len(names), st))
			elided += len(names)
			continue
		}
		out = append(out, fmt.Sprintf("%s ×%d: %s", st, len(names), strings.Join(names, " ")))
	}
	out = append(out, tail...)
	return result{out: out, elided: elided, ok: true}
}
