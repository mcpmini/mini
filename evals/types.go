//go:build evals

package evals

import (
	"math"
	"sort"
)

const (
	fmtPassthrough = iota
	fmtProjected
	fmtLines
	numFormats = 3
)

var fmtLabel = [numFormats]string{"passthrough", "projected", "lines"}

// ClaudeResult holds outcome and token usage for a single Claude run.
type ClaudeResult struct {
	Err              error
	Ran              bool
	Text             string
	InputTokens      int
	CacheReadTokens  int
	CacheWriteTokens int
	OutputTokens     int
	TotalCostUSD     float64
	Turns            int
	WorkDir          string
	CallLogDir       string
	RawOutputPath    string
}

func (r ClaudeResult) EffectiveInputTokens() int {
	return r.InputTokens + r.CacheReadTokens + r.CacheWriteTokens
}

// RunStats aggregates results across multiple repetitions of one mode.
type RunStats struct {
	Runs []ClaudeResult
}

func (s RunStats) Ran() bool { return len(s.Runs) > 0 }

type tokenStats struct{ min, max, avg, p95 int }
type costStats struct{ min, max, avg float64 }

func intStat(vals []int) tokenStats {
	if len(vals) == 0 {
		return tokenStats{}
	}
	cp := make([]int, len(vals))
	copy(cp, vals)
	sort.Ints(cp)
	sum := 0
	for _, v := range cp {
		sum += v
	}
	p95idx := int(math.Ceil(0.95*float64(len(cp)))) - 1
	return tokenStats{min: cp[0], max: cp[len(cp)-1], avg: sum / len(cp), p95: cp[p95idx]}
}

func floatStat(vals []float64) costStats {
	if len(vals) == 0 {
		return costStats{}
	}
	cp := make([]float64, len(vals))
	copy(cp, vals)
	sort.Float64s(cp)
	sum := 0.0
	for _, v := range cp {
		sum += v
	}
	return costStats{min: cp[0], max: cp[len(cp)-1], avg: sum / float64(len(cp))}
}

func (s RunStats) InputStats() tokenStats {
	vals := make([]int, len(s.Runs))
	for i, r := range s.Runs {
		vals[i] = r.EffectiveInputTokens()
	}
	return intStat(vals)
}

func (s RunStats) CostStats() costStats {
	vals := make([]float64, len(s.Runs))
	for i, r := range s.Runs {
		vals[i] = r.TotalCostUSD
	}
	return floatStat(vals)
}

func (s RunStats) AvgTurns() float64 {
	if len(s.Runs) == 0 {
		return 0
	}
	sum := 0
	for _, r := range s.Runs {
		sum += r.Turns
	}
	return float64(sum) / float64(len(s.Runs))
}

// EvalResult holds results for all 7 modes, each across one or more reps.
type EvalResult struct {
	Direct RunStats
	MCP [numFormats]RunStats
	CLI [numFormats]RunStats
}

// EvalParams specifies the eval-specific inputs. Mode and rep counts live on Runner.
type EvalParams struct {
	Servers      map[string]string
	AllowedTools string
	WorkSrcDir   string
}
