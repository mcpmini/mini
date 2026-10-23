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
	logRow(logRowParams{Writer: w, Mode: "direct (no mini)", Stats: r.Direct, Percentile: pct})
	for _, run := range EvalWithLabels(r) {
		if run.Label == "direct" {
			continue
		}
		logRow(logRowParams{Writer: w, Mode: run.Label, Stats: run.Stats, Percentile: pct})
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

type logRowParams struct {
	Writer     io.Writer
	Mode       string
	Stats      RunStats
	Percentile func(int) string
}

func logRow(p logRowParams) {
	if !p.Stats.Ran() {
		return
	}
	tok := p.Stats.InputStats()
	cost := p.Stats.CostStats()
	if len(p.Stats.Runs) == 1 {
		fmt.Fprintf(p.Writer, "  %-22s %7d in   $%.4f  (%.0f turns)  %s\n",
			p.Mode, tok.avg, cost.avg, p.Stats.AvgTurns(), p.Percentile(tok.avg))
	} else {
		fmt.Fprintf(p.Writer, "  %-22s avg %7d / p95 %7d / max %7d   $%.4f avg  (%.1f turns avg)  %s\n",
			p.Mode, tok.avg, tok.p95, tok.max, cost.avg, p.Stats.AvgTurns(), p.Percentile(tok.avg))
	}
}
