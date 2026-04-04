package response

import "encoding/json"

// EstimateTokens gives a rough token count for a JSON-serializable value.
func EstimateTokens(v any) int {
	b, err := json.Marshal(v)
	if err != nil {
		return 0
	}
	return EstimateTokensRaw(b)
}

// EstimateTokensRaw estimates tokens from raw JSON bytes.
func EstimateTokensRaw(b []byte) int {
	return len(b) / 3
}

// EstimateTokensText estimates tokens from plain text.
func EstimateTokensText(s string) int {
	return len(s) / 4
}
