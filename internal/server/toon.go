package server

import (
	"log/slog"

	"github.com/mcpmini/mini/internal/response"
	"github.com/mcpmini/mini/internal/toon"
)

// EncodeToon renders the complete envelope as TOON and returns encoder errors
// instead of substituting a different wire format.
func EncodeToon(logger *slog.Logger, env *response.Envelope) (string, error) {
	text, err := encodeToonValue(env)
	if err == nil {
		return text, nil
	}
	if logger != nil {
		logger.Warn("toon encode failed", "err", err)
	}
	return "", err
}

func encodeToonValue(env *response.Envelope) (string, error) {
	v, err := toon.FromAny(env.WireMap())
	if err != nil {
		return "", err
	}
	return toon.Encode(v)
}
