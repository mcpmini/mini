package server

import (
	"context"

	"github.com/mcpmini/mini/internal/jq"
)

func applyReadFilter(ctx context.Context, data []byte, filter string) (string, error) {
	return jq.Eval(ctx, data, filter)
}
