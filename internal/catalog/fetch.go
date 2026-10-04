package catalog

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"slices"
	"time"

	"github.com/mcpmini/mini/internal/transport"
)

// GitHub Pages serves catalog/v1.json from main here. No release reads it yet; once one does, v1
// changes must stay additive. An oauth2 entry that needs a bundled client registration must wait
// for the release that bundles it, or older binaries write it without one.
const PublishedURL = "https://mcpmini.github.io/mini/catalog/v1.json"

const (
	maxFetchBytes = 256 << 10
	fetchTimeout  = 3 * time.Second
)

func NewFetchClient() *http.Client {
	return transport.NewNoRedirectClient(transport.NoRedirectClientOptions{Timeout: fetchTimeout})
}

// Fetch skips entries whose auth this binary doesn't know, so newer auth kinds can be
// published; any other invalid entry rejects the whole document.
func Fetch(ctx context.Context, client *http.Client, url string) (Catalog, error) {
	if err := validateHTTPSURL(url); err != nil {
		return Catalog{}, err
	}
	data, err := download(ctx, client, url)
	if err != nil {
		return Catalog{}, err
	}
	doc, err := decode(data)
	if err != nil {
		return Catalog{}, err
	}
	return validated(withoutUnknownAuth(doc.Catalog))
}

func withoutUnknownAuth(c Catalog) Catalog {
	skipped := make(map[string]bool)
	var categories []Category
	for _, category := range c.Categories {
		listed := len(category.Servers)
		category.Servers = slices.DeleteFunc(slices.Clone(category.Servers), func(entry Entry) bool {
			unknown := !slices.Contains(knownAuthValues, entry.Auth)
			skipped[entry.Name] = skipped[entry.Name] || unknown
			return unknown
		})
		if len(category.Servers) > 0 || listed == 0 {
			categories = append(categories, category)
		}
	}
	popular := slices.DeleteFunc(slices.Clone(c.Popular), func(name string) bool { return skipped[name] })
	return Catalog{Popular: popular, Categories: categories}
}

func download(ctx context.Context, client *http.Client, url string) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	// Closing only releases the connection; whatever was read or returned is already decided.
	defer resp.Body.Close() //nolint:errcheck
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("fetch catalog: status %d", resp.StatusCode)
	}
	data, err := io.ReadAll(io.LimitReader(resp.Body, maxFetchBytes+1))
	if err != nil {
		return nil, fmt.Errorf("fetch catalog: %w", err)
	}
	if len(data) > maxFetchBytes {
		return nil, fmt.Errorf("fetch catalog: response exceeds %d bytes", maxFetchBytes)
	}
	return data, nil
}
