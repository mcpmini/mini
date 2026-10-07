package main

import (
	"context"
	"net/http"

	"github.com/mcpmini/mini/internal/catalog"
)

type catalogSource struct {
	client *http.Client
	url    string
}

func publishedCatalogSource() catalogSource {
	return catalogSource{client: catalog.NewFetchClient(), url: catalog.PublishedURL}
}

func (s catalogSource) entries() ([]catalog.Entry, error) {
	c, err := s.load()
	return c.Entries, err
}

// Any fetch failure, a document this build can't read included, quietly falls back to the built-in catalog.
func (s catalogSource) load() (catalog.Catalog, error) {
	if c, err := catalog.Fetch(context.Background(), s.client, s.url); err == nil {
		return c, nil
	}
	return catalog.Load()
}
