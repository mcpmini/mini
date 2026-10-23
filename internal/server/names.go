package server

import (
	"fmt"

	"github.com/mcpmini/mini/internal/config"
)

func toolFullName(server, tool string) string { return server + "." + tool }

func validateServerName(name string) error {
	if !config.ValidServerName.MatchString(name) {
		return fmt.Errorf("invalid server name: %q", name)
	}
	return nil
}
