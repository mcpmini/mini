//go:build !test

package main

import "github.com/mcpmini/mini/internal/catalog"

func publishedCatalogURL() string {
	return catalog.PublishedURL
}
