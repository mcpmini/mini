package server

import "os"

func expandEnv(s string) string {
	return os.Expand(s, os.Getenv)
}
