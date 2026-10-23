package response_test

import (
	"testing"

	"github.com/mcpmini/mini/internal/response"
)

func TestEstimateTokensFromValue(t *testing.T) {
	tokens := response.EstimateTokens(map[string]any{"message": "hello"})
	if tokens == 0 {
		t.Fatal("expected non-zero token estimate")
	}
}

func TestEstimateTokensReturnsZeroOnMarshalError(t *testing.T) {
	tokens := response.EstimateTokens(map[chan int]any{make(chan int): "hello"})
	if tokens != 0 {
		t.Fatalf("EstimateTokens() = %d, want 0", tokens)
	}
}

func TestEstimateTokensText(t *testing.T) {
	text := "12345678"
	if got := response.EstimateTokensText(text); got != 2 {
		t.Fatalf("EstimateTokensText() = %d, want 2", got)
	}
}
