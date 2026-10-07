//go:build test

package main

import (
	"os"

	"github.com/mcpmini/mini/internal/catalog"
)

// Terminal tests run the real binary and must not reach the published catalog.
func publishedCatalogURL() string {
	if url := os.Getenv("MINI_TEST_CATALOG_URL"); url != "" {
		return url
	}
	return catalog.PublishedURL
}
