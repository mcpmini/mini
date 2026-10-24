package response

// EstimateTokensRaw estimates tokens from raw JSON bytes.
func EstimateTokensRaw(b []byte) int {
	return len(b) / 3
}
