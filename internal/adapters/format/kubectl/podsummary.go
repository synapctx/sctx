package kubectl

import (
	"fmt"
	"strings"
)

// podSummaryMinRows is the smallest healthy+completed population for which
// renderPodSummary replaces names with counts; below it the per-name list is
// already cheaper than the counting scaffolding and lets a reader confirm a
// specific pod exists.
const podSummaryMinRows = maxReadyNamesShown + 1

// podRestartLimit is the restart count at which a Running, fully ready pod
// whose last-restart age is unknown (older kubectl prints a bare count) is
// still listed in full.
const podRestartLimit = 5

// flapping reports whether a Running, ready pod with rc>0 restarts still needs
// a row. With kubectl's "N (AGE ago)" form the pod is listed only when the
// last restart was under a day ago (no d/w/y unit): older restarts are history,
// and are tallied in the header instead. A bare count carries no age, so it is
// listed from podRestartLimit upward.
func flapping(cell string, rc int) bool {
	open := strings.Index(cell, "(")
	if open < 0 {
		return rc >= podRestartLimit
	}
	return !strings.ContainsAny(cell[open:], "dwy")
}

// restartCount parses the leading integer of a RESTARTS cell.
func restartCount(cell string) int {
	n := 0
	for _, r := range cell {
		if r < '0' || r > '9' {
			break
		}
		n = n*10 + int(r-'0')
	}
	return n
}

// nsTally counts rows per namespace, remembering first-seen order.
type nsTally struct {
	order []string
	n     map[string]int
}

func newNSTally() *nsTally { return &nsTally{n: map[string]int{}} }

func (t *nsTally) add(ns string) {
	if _, ok := t.n[ns]; !ok {
		t.order = append(t.order, ns)
	}
	t.n[ns]++
}

func (t *nsTally) String() string {
	parts := make([]string, 0, len(t.order))
	for _, ns := range t.order {
		parts = append(parts, fmt.Sprintf("%s %d", ns, t.n[ns]))
	}
	return strings.Join(parts, ", ")
}

// renderPodSummary renders a pod table as counts plus full rows for pods that
// need attention. It applies only to tables with NAME, READY, STATUS and
// RESTARTS columns and enough healthy rows to be worth counting. Healthy
// Running pods and Completed/Succeeded pods are reduced to per-namespace
// counts; every other pod, and any Running pod that restarted recently (see
// flapping), keeps its full row (minus AGE).
// Running pods with older, fewer restarts stay in the running count and are
// tallied in the header so the elision is visible.
func renderPodSummary(names []string, rows [][]string, isHealthy func([]string) bool) ([]byte, bool) {
	nameIdx := colIndex(names, "NAME")
	statusIdx := colIndex(names, "STATUS")
	readyIdx := colIndex(names, "READY")
	restartIdx := colIndex(names, "RESTARTS")
	nsIdx := colIndex(names, "NAMESPACE")
	ageIdx := colIndex(names, "AGE")
	if nameIdx < 0 || statusIdx < 0 || readyIdx < 0 || restartIdx < 0 {
		return nil, false
	}

	running, completed := newNSTally(), newNSTally()
	var runningN, completedN, oldRestarts, maxOld int
	var attention [][]string

	for _, cols := range rows {
		ns := ""
		if nsIdx >= 0 {
			ns = cols[nsIdx]
		}
		status := cols[statusIdx]
		if status == "Completed" || status == "Succeeded" {
			completed.add(ns)
			completedN++
			continue
		}
		if status != "Running" || !isHealthy(cols) {
			attention = append(attention, cols)
			continue
		}
		rc := restartCount(cols[restartIdx])
		if rc > 0 && flapping(cols[restartIdx], rc) {
			attention = append(attention, cols)
			continue
		}
		if rc > 0 {
			oldRestarts++
			maxOld = max(maxOld, rc)
		}
		running.add(ns)
		runningN++
	}
	if runningN+completedN < podSummaryMinRows {
		return nil, false
	}

	var b strings.Builder
	fmt.Fprintf(&b, "%d pods: %d running", len(rows), runningN)
	if completedN > 0 {
		fmt.Fprintf(&b, ", %d completed", completedN)
	}
	if len(attention) > 0 {
		fmt.Fprintf(&b, ", %d unhealthy", len(attention))
	}
	if oldRestarts > 0 {
		fmt.Fprintf(&b, " (%d running had earlier restarts, max %d)", oldRestarts, maxOld)
	}
	if nsIdx >= 0 {
		if runningN > 0 {
			fmt.Fprintf(&b, "\nrunning: %s", running)
		}
		if completedN > 0 {
			fmt.Fprintf(&b, "\ncompleted: %s", completed)
		}
	}
	if len(attention) > 0 {
		var hdr []string
		for i, n := range names {
			if i != ageIdx {
				hdr = append(hdr, n)
			}
		}
		fmt.Fprintf(&b, "\nunhealthy: %s", strings.Join(hdr, " | "))
		for _, cols := range attention {
			var parts []string
			for i, v := range cols {
				if i != ageIdx {
					parts = append(parts, v)
				}
			}
			fmt.Fprintf(&b, "\n  %s", strings.Join(parts, " "))
		}
	}
	return []byte(b.String()), true
}
