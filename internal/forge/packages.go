package forge

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"time"
)

const maxPackages = 8

var packageSpecifierPattern = regexp.MustCompile(`^(npm|jsr):[@a-zA-Z0-9][a-zA-Z0-9@/._^~+-]*$`)

// withPackagesCacheDir points DENO_DIR at a cache keyed by the declared
// package set, so --cached-only can only ever see that set's dependency
// closure — packages is an import boundary, not just a download boundary.
func withPackagesCacheDir(packages, extraEnv []string) ([]string, error) {
	for _, kv := range extraEnv {
		if strings.HasPrefix(kv, "DENO_DIR=") {
			return extraEnv, nil
		}
	}
	dir, err := packagesCacheDir(packages)
	if err != nil {
		return nil, &Error{Kind: KindRunner, Message: err.Error()}
	}
	return append(extraEnv, "DENO_DIR="+dir), nil
}

func packagesCacheDir(packages []string) (string, error) {
	sorted := slices.Sorted(slices.Values(packages))
	sum := sha256.Sum256([]byte(strings.Join(sorted, "\n")))
	dir := filepath.Join(os.TempDir(), "forge-deno-"+hex.EncodeToString(sum[:8]))
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", err
	}
	// Refresh mtime so the startup sweep's age gate sees active caches as live;
	// real time because it must match the filesystem clock Chtimes writes to.
	now := time.Now() //nolint:clocklint
	_ = os.Chtimes(dir, now, now)
	return dir, nil
}

func validatePackages(packages []string) error {
	if len(packages) > maxPackages {
		return &Error{Kind: KindRunner, Message: fmt.Sprintf("too many packages: %d (max %d)", len(packages), maxPackages)}
	}
	for _, p := range packages {
		if !packageSpecifierPattern.MatchString(p) {
			return &Error{Kind: KindRunner, Message: fmt.Sprintf("invalid package specifier %q: only npm: and jsr: specifiers are allowed", p)}
		}
	}
	return nil
}
