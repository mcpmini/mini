package catalog

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"time"
)

// GitHub Pages serves the repo root under /mini/ (as for auth.ClientMetadataURL),
// so this is catalog/v1.json as merged to main.
const PublishedURL = "https://mcpmini.github.io/mini/catalog/v1.json"

const (
	maxFetchBytes = 256 << 10
	fetchTimeout  = 5 * time.Second
)

func NewFetchClient() *http.Client {
	return &http.Client{
		Timeout: fetchTimeout,
		// A redirect could hand the catalog to another host; refuse it like the upstream HTTP client does.
		CheckRedirect: func(*http.Request, []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}
}

// Fetch downloads the catalog at url and validates it the same way as the
// embedded one; any invalid entry rejects the whole document.
func Fetch(ctx context.Context, client *http.Client, url string) ([]Entry, error) {
	if err := validateHTTPSURL(url); err != nil {
		return nil, err
	}
	data, err := download(ctx, client, url)
	if err != nil {
		return nil, err
	}
	return parse(data)
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
