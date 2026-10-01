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

// GitHub Pages serves catalog/v1.json from main here, and every released binary reads it, so v1
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
func Fetch(ctx context.Context, client *http.Client, url string) ([]Entry, error) {
	if err := validateHTTPSURL(url); err != nil {
		return nil, err
	}
	data, err := download(ctx, client, url)
	if err != nil {
		return nil, err
	}
	doc, err := decode(data)
	if err != nil {
		return nil, err
	}
	doc.Entries = slices.DeleteFunc(doc.Entries, func(entry Entry) bool {
		return !slices.Contains(knownAuthValues, entry.Auth)
	})
	return validateEntries(doc.Entries)
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
