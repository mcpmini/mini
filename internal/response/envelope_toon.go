package response

import "github.com/mcpmini/mini/internal/toon"

// Toon renders the complete envelope as TOON and returns encoder errors instead of substituting
// a different wire format.
func (e *Envelope) Toon() (string, error) {
	v, err := toon.FromAny(e.WireMap())
	if err != nil {
		return "", err
	}
	return toon.Encode(v)
}
