package toon

import "encoding/json"

// FromAny converts an arbitrary Go value to a Value via json.Marshal then
// FromJSON, so it inherits FromJSON's ordering and canonicalization and
// encoding/json's own rules for unsupported values (chan, func, NaN/Inf
// floats, non-string map keys) and its guarantee of sorted map keys.
func FromAny(v any) (Value, error) {
	raw, err := json.Marshal(v)
	if err != nil {
		return Value{}, err
	}
	return FromJSON(raw)
}
