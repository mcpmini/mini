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
	var out []LabeledRunStats
	for _, lr := range allRunStats(r) {
		if lr.Stats.Ran() {
			out = append(out, lr)
		}
	}
	return out
}

func allRunStats(r EvalResult) []LabeledRunStats {
	all := []LabeledRunStats{{"direct", r.Direct}}
	for i := range numFormats {
		all = append(all,
			LabeledRunStats{"mcp-" + fmtLabel[i], r.MCP[i]},
			LabeledRunStats{"cli-" + fmtLabel[i], r.CLI[i]},
		)
	}
	for i := range numFormats {
		all = append(all, LabeledRunStats{"proxy-" + fmtLabel[i], r.Proxy[i]})
	}
	return all
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
	pct := tokenDelta(rawAvg, r.Direct.Ran())
	fmt.Fprintf(w, "\n╔══ Token Report: %s ══╗\n", label)
	logEvalRows(w, r, pct)
	fmt.Fprintln(w, "╚══════════════════════════════════════╝")
	logEvalTexts(w, r)
}

func tokenDelta(rawAvg int, directRan bool) func(int) string {
	return func(avg int) string {
		if rawAvg == 0 || !directRan {
			return "    n/a"
		}
		return fmt.Sprintf("%+6.1f%%", float64(avg-rawAvg)/float64(rawAvg)*100)
	}
}

func logEvalRows(w io.Writer, r EvalResult, pct func(int) string) {
	logRow(w, "direct (no mini)", r.Direct, pct)
	for _, run := range EvalWithLabels(r) {
		if run.Label == "direct" {
			continue
		}
		logRow(w, run.Label, run.Stats, pct)
	}
}

func logEvalTexts(w io.Writer, r EvalResult) {
	for _, tc := range EvalWithLabels(r) {
		if text := firstRunText(tc.Stats); text != "" {
			fmt.Fprintf(w, "[%s] %s\n", tc.Label, text)
		}
	}
}

func firstRunText(stats RunStats) string {
	if len(stats.Runs) == 0 {
		return ""
	}
	return stats.Runs[0].Text
}

func logRow(w io.Writer, mode string, s RunStats, pct func(int) string) {
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
