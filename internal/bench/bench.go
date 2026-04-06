package bench

import (
	"encoding/json"

	"github.com/mcpmini/mini/internal/config"
	"github.com/mcpmini/mini/internal/projection"
	"github.com/mcpmini/mini/internal/response"
)

// Result holds token and byte measurements for one (server, tool, mode) triple.
type Result struct {
	Server string
	Tool   string
	Mode   string
	Tokens int
	Bytes  int
}

// Case is a single benchmark: a fixture paired with its projection config.
type Case struct {
	Server     string
	Tool       string
	Raw        []byte
	ProjConfig *config.ProjectionConfig
}

// Measure runs the three projection modes against c and returns three Results.
func Measure(c Case, defaults *projection.Defaults) []Result {
	raw := result(c.Server, c.Tool, "raw", c.Raw)

	var parsed any
	if err := json.Unmarshal(c.Raw, &parsed); err != nil {
		return []Result{raw}
	}

	proj := applyAndMarshal(c.Server, c.Tool, "projected", parsed, c.ProjConfig, defaults, false)
	stripped := applyAndMarshal(c.Server, c.Tool, "stripped", parsed, c.ProjConfig, defaults, true)

	return []Result{raw, proj, stripped}
}

func applyAndMarshal(server, tool, mode string, value any, cfg *config.ProjectionConfig, defaults *projection.Defaults, strip bool) Result {
	effective := cfg
	if strip && cfg != nil {
		copy := *cfg
		copy.StripMarkup = true
		effective = &copy
	} else if strip {
		effective = &config.ProjectionConfig{StripMarkup: true}
	}

	r := projection.Apply(value, effective, defaults)
	b, _ := json.Marshal(r.Summary)
	return Result{Server: server, Tool: tool, Mode: mode, Tokens: response.EstimateTokensRaw(b), Bytes: len(b)}
}

func result(server, tool, mode string, raw []byte) Result {
	return Result{
		Server: server,
		Tool:   tool,
		Mode:   mode,
		Tokens: response.EstimateTokensRaw(raw),
		Bytes:  len(raw),
	}
}

// DefaultProjectionDefaults returns the defaults used in normal proxy operation.
func DefaultProjectionDefaults() *projection.Defaults {
	cfg := config.DefaultConfig()
	return &projection.Defaults{
		StringLimit:        cfg.DefaultStringLimit,
		DepthLimit:         cfg.DefaultDepthLimit,
		ContentFields:      cfg.ContentFields,
		AutoStripThreshold: cfg.AutoStripThreshold,
	}
}
