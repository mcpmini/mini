package main

import (
	"fmt"
	"strings"

	"github.com/mcpmini/mini/internal/config"
)

const unknownTransport = "-"

func singleLine(err error) string {
	return strings.Join(strings.Fields(err.Error()), " ")
}

func projectionsError(sc config.ServerConfig) error {
	return fmt.Errorf("projections: %w", sc.ProjectionsErr.Err)
}
