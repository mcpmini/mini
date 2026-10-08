package testutil

import (
	"fmt"
	"os"
	"testing"
)

// Binary returns the path a test binary was built to, from the env var that scripts/test-bins.sh prints.
func Binary(t testing.TB, env string) string {
	t.Helper()
	bin, err := BinaryFromEnv(env)
	if err != nil {
		t.Fatal(err)
	}
	return bin
}

// BinaryFromEnv is Binary for TestMain, which has no testing.TB.
func BinaryFromEnv(env string) (string, error) {
	bin := os.Getenv(env)
	if bin == "" {
		return "", fmt.Errorf(
			"%s not set; run check.sh, or: scripts/test-bins.sh DIR and export the lines it prints",
			env,
		)
	}
	return bin, nil
}
