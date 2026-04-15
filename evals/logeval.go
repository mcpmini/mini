//go:build evals

package evals

import (
	"fmt"
	"io"
	"os"
)

var output io.Writer = os.Stdout

func logWriter() io.Writer { return output }

// SetOutput redirects LogEval output. Call before running evals.
func SetOutput(w io.Writer) { output = w }

// LabeledRunStats pairs a mode label with its aggregated results.
type LabeledRunStats struct {
	Label string
	Stats RunStats
}

// EvalWithLabels returns only the modes that were actually run.
func EvalWithLabels(r EvalResult) []LabeledRunStats {
	all := []LabeledRunStats{{"direct", r.Direct}}
	for i := range numFormats {
		all = append(all,
			LabeledRunStats{"mcp-" + fmtLabel[i], r.MCP[i]},
			LabeledRunStats{"cli-" + fmtLabel[i], r.CLI[i]},
		)
	}
	var out []LabeledRunStats
	for _, lr := range all {
		if lr.Stats.Ran() {
			out = append(out, lr)
		}
	}
	return out
}

// RepLabel formats a label for a single rep within a mode.
// When there is only one rep, returns the mode label unchanged.
func RepLabel(mode string, rep, total int) string {
	if total == 1 {
		return mode
	}
	return fmt.Sprintf("%s[%d/%d]", mode, rep+1, total)
}

// LogEval writes a token report for all modes to w.
func LogEval(w io.Writer, label string, r EvalResult) {
	rawAvg := r.Direct.InputStats().avg
	pct := func(avg int) string {
		if rawAvg == 0 || !r.Direct.Ran() {
			return "    n/a"
		}
		return fmt.Sprintf("%+6.1f%%", float64(avg-rawAvg)/float64(rawAvg)*100)
	}
	row := func(mode string, s RunStats) {
		if !s.Ran() {
			return
		}
		tok := s.InputStats()
		cost := s.CostStats()
		if len(s.Runs) == 1 {
			fmt.Fprintf(w, "  %-22s %7d in   $%.4f  (%.0f turns)  %s\n",
				mode, tok.avg, cost.avg, s.AvgTurns(), pct(tok.avg))
		} else {
			fmt.Fprintf(w, "  %-22s avg %7d / p95 %7d / max %7d   $%.4f avg  (%.1f turns avg)  %s\n",
				mode, tok.avg, tok.p95, tok.max, cost.avg, s.AvgTurns(), pct(tok.avg))
		}
	}
	fmt.Fprintf(w, "\n╔══ Token Report: %s ══╗\n", label)
	row("direct (no mini)", r.Direct)
	for i := range numFormats {
		row("mcp-"+fmtLabel[i], r.MCP[i])
		row("cli-"+fmtLabel[i], r.CLI[i])
	}
	fmt.Fprintln(w, "╚══════════════════════════════════════╝")
	for _, tc := range EvalWithLabels(r) {
		if len(tc.Stats.Runs) > 0 && tc.Stats.Runs[0].Text != "" {
			fmt.Fprintf(w, "[%s] %s\n", tc.Label, tc.Stats.Runs[0].Text)
		}
	}
}
