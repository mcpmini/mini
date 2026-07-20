package server

import (
	"encoding/json"
	"log/slog"

	"github.com/mcpmini/mini/internal/response"
	"github.com/mcpmini/mini/internal/toon"
)

// EncodeToon renders env as TOON. Encoding can fail for values that
// encoding/json itself cannot marshal (e.g. math.NaN); on failure the
// response degrades to JSON, and if even JSON fails, to a minimal JSON
// error object so the caller always receives parseable output.
func EncodeToon(logger *slog.Logger, env *response.Envelope) string {
	text, err := encodeToonValue(env)
	if err == nil {
		return text
	}
	logger.Warn("toon encode failed, falling back to JSON", "err", err)
	b, jsonErr := json.Marshal(env)
	if jsonErr != nil {
		logger.Error("toon fallback JSON marshal also failed", "err", jsonErr)
		errObj, _ := json.Marshal(map[string]string{"error": "response could not be encoded: " + jsonErr.Error()})
		return string(errObj)
	}
	return string(b)
}

func encodeToonValue(env *response.Envelope) (string, error) {
	v, err := toon.FromAny(env)
	if err != nil {
		// Retry with non-finite floats normalized to null (spec §3) only when
		// plain encoding failed: the normalizer rebuilds structs as generic maps,
		// losing omitempty/embedding nuances, so it must never touch finite data.
		// See https://github.com/toon-format/spec/blob/f55b93ac489f297ff597d95e4c19ae84675eaeb7/SPEC.md#3-encoding-normalization-reference-encoder
		v, err = toon.FromAny(normalizeEnvelopeNonFinite(env))
	}
	if err != nil {
		return "", err
	}
	return toon.Encode(v)
}
