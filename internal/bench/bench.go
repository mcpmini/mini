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

	proj := applyAndMarshal(ApplyParams{Server: c.Server, Tool: c.Tool, Mode: "projected", Value: parsed, Cfg: c.ProjConfig, Defaults: defaults, Strip: false})
	stripped := applyAndMarshal(ApplyParams{Server: c.Server, Tool: c.Tool, Mode: "stripped", Value: parsed, Cfg: c.ProjConfig, Defaults: defaults, Strip: true})

	return []Result{raw, proj, stripped}
}

// ApplyParams holds the inputs needed to project a value and marshal the result.
type ApplyParams struct {
	Server, Tool, Mode string
	Value              any
	Cfg                *config.ProjectionConfig
	Defaults           *projection.Defaults
	Strip              bool
}

func applyAndMarshal(p ApplyParams) Result {
	effective := p.Cfg
	if p.Strip && p.Cfg != nil {
		copy := *p.Cfg
		copy.StripMarkup = true
		effective = &copy
	} else if p.Strip {
		effective = &config.ProjectionConfig{StripMarkup: true}
	}

	r := projection.Apply(p.Value, effective, p.Defaults)
	b, _ := json.Marshal(r.Summary)
	return Result{Server: p.Server, Tool: p.Tool, Mode: p.Mode, Tokens: response.EstimateTokensRaw(b), Bytes: len(b)}
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
