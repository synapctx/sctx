package fw

import "strings"

func isAny(s string) bool {
	switch s {
	case "0.0.0.0/0", "::/0", "anywhere", "anywhere6":
		return true
	}
	return false
}

const legend = "columns: pkts bytes target prot [opt] [in=] [out=] [src=] [dst=] options  (wildcards omitted: opt --, in/out *, src/dst any)"

// renderIptables handles `-L` listings. Rule-spec output (-S, -C) and every
// mutating verb decline.
func renderIptables(args, lines []string, aggressive bool) result {
	list := false
	for _, a := range args {
		switch {
		case a == "-S" || a == "--list-rules" || a == "-C" || a == "--check" || a == "-A" || a == "-I" || a == "-D" || a == "-F" || a == "-X" || a == "-N" || a == "-P" || a == "-Z" || a == "-R":
			return result{}
		case a == "-L" || a == "--list" || (len(a) > 1 && a[0] == '-' && a[1] != '-' && strings.Contains(a[1:], "L")):
			list = true
		}
	}
	if !list {
		return result{}
	}

	var (
		out      []string
		cols     []string
		colsLine string
		chains   int
	)
	for i := 0; i < len(lines); i++ {
		l := lines[i]
		if strings.TrimSpace(l) == "" {
			continue
		}
		if strings.HasPrefix(l, "Chain ") {
			chains++
			// header line, if any, follows immediately
			j := i + 1
			if j < len(lines) && isColumnHeader(lines[j]) {
				h := strings.Fields(lines[j])
				if cols == nil {
					cols, colsLine = h, strings.Join(h, " ")
				} else if strings.Join(h, " ") != colsLine {
					return result{} // mixed layouts: do not guess
				}
				j++
			}
			k := j
			for k < len(lines) && strings.TrimSpace(lines[k]) != "" && !strings.HasPrefix(lines[k], "Chain ") {
				k++
			}
			if aggressive && k == j {
				out = append(out, l+": empty")
			} else {
				out = append(out, l)
			}
			for _, r := range lines[j:k] {
				out = append(out, formatRule(r, cols, aggressive))
			}
			i = k - 1
			continue
		}
		out = append(out, l) // anything else (warnings, table banners) stays
	}
	if chains == 0 || cols == nil {
		return result{}
	}
	hdr := "columns: " + colsLine
	if aggressive {
		hdr = legend + " | source columns: " + colsLine
	}
	// The wildcard legend only applies when columns are the full set.
	return result{out: append([]string{hdr}, out...), elided: chains - 1, ok: true}
}

func isColumnHeader(l string) bool {
	f := strings.Fields(l)
	has := map[string]bool{}
	for _, x := range f {
		has[x] = true
	}
	return has["target"] && has["prot"] && has["source"]
}

// formatRule emits one rule row. Relaxed only squeezes spaces; aggressive also
// omits wildcard columns.
func formatRule(row string, cols []string, aggressive bool) string {
	f := strings.Fields(row)
	if len(f) < len(cols) {
		return strings.Join(f, " ")
	}
	fixed, tail := f[:len(cols)], f[len(cols):]
	var b strings.Builder
	for i, c := range cols {
		v := fixed[i]
		if aggressive {
			switch c {
			case "opt":
				if v == "--" {
					continue
				}
			case "in", "out":
				if v == "*" {
					continue
				}
				v = c + "=" + v
			case "source":
				if isAny(v) {
					continue
				}
				v = "src=" + v
			case "destination":
				if isAny(v) {
					continue
				}
				v = "dst=" + v
			}
		}
		if b.Len() > 0 {
			b.WriteByte(' ')
		}
		b.WriteString(v)
	}
	if len(tail) > 0 {
		b.WriteString(" " + strings.Join(tail, " "))
	}
	return b.String()
}
