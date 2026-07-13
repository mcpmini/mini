package server

import (
	"encoding/json"
	"fmt"
	"log/slog"

	"github.com/mcpmini/mini/internal/response"
	"github.com/mcpmini/mini/internal/toon"
)

// EncodeToon renders env as TOON, the exact same information as JSON mode
// marshals via env's own MarshalJSON. FromAny/Encode cannot fail for a
// response.Envelope (its wire shape is a closed set of JSON-marshalable
// fields), but the fallback keeps a malformed envelope from ever producing a
// broken response, mirroring mustJSON's defensive fmt.Sprintf floor in serve.go.
func EncodeToon(logger *slog.Logger, env *response.Envelope) string {
	text, err := encodeToonValue(env)
	if err == nil {
		return text
	}
	logger.Warn("toon encode failed, falling back to JSON", "err", err)
	b, jsonErr := json.Marshal(env)
	if jsonErr != nil {
		logger.Error("toon fallback JSON marshal also failed", "err", jsonErr)
		return fmt.Sprintf("%v", env)
	}
	return string(b)
}

func encodeToonValue(env *response.Envelope) (string, error) {
	v, err := toon.FromAny(env)
	if err != nil {
		return "", err
	}
	return toon.Encode(v)
}
