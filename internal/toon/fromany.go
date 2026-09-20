package toon

import "encoding/json"

// FromAny converts an arbitrary Go value to a Value via json.Marshal then
// FromJSON, inheriting FromJSON's ordering and canonicalization.
// Non-serializable values (chan, func, NaN, Infinity, unsupported map keys)
// are returned as errors.
func FromAny(v any) (Value, error) {
	raw, err := json.Marshal(v)
	if err != nil {
		return Value{}, err
	}
	return FromJSON(raw)
}
