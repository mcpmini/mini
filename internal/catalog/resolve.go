package catalog

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"time"

	"github.com/mcpmini/mini/internal/clock"
)

const (
	cacheTTL      = 24 * time.Hour
	maxFetchBytes = 256 * 1024
	fetchTimeout  = 5 * time.Second
)

type ResolveParams struct {
	Clock      clock.Clock
	Client     *http.Client
	CatalogURL string
	ConfigDir  string
	Logger     *slog.Logger
}

type jsonDocument struct {
	SchemaVersion int     `json:"schema_version"`
	Entries       []Entry `json:"entries"`
}

func NewFetchClient() *http.Client {
	return &http.Client{
		Timeout: fetchTimeout,
		CheckRedirect: func(*http.Request, []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}
}

func Resolve(ctx context.Context, p ResolveParams) ([]Entry, error) {
	cachePath := filepath.Join(p.ConfigDir, "internal", "catalog.json")
	if entries, ok := loadFreshCache(cachePath, p.Clock); ok {
		return entries, nil
	}
	entries, fetchErr := fetchCatalog(ctx, p.Client, p.CatalogURL)
	if fetchErr == nil {
		_ = writeCacheAtomic(cachePath, entries)
		return entries, nil
	}
	if entries, ok := loadAnyCache(cachePath); ok {
		return entries, nil
	}
	embedded, loadErr := Load()
	p.Logger.Warn("catalog fetch failed, using embedded snapshot", "error", fetchErr)
	return embedded, loadErr
}

func loadFreshCache(path string, clk clock.Clock) ([]Entry, bool) {
	info, err := os.Stat(path)
	if err != nil || clk.Since(info.ModTime()) > cacheTTL {
		return nil, false
	}
	return loadAnyCache(path)
}

func loadAnyCache(path string) ([]Entry, bool) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, false
	}
	entries, err := parseJSON(data)
	return entries, err == nil
}

func fetchCatalog(ctx context.Context, client *http.Client, url string) ([]Entry, error) {
	if err := validateHTTPSURL(url); err != nil {
		return nil, err
	}
	ctx, cancel := context.WithTimeout(ctx, fetchTimeout)
	defer cancel()
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
		return nil, fmt.Errorf("catalog fetch: status %d", resp.StatusCode)
	}
	data, err := readLimited(resp.Body)
	if err != nil {
		return nil, err
	}
	return parseJSON(data)
}

func readLimited(body io.Reader) ([]byte, error) {
	data, err := io.ReadAll(io.LimitReader(body, maxFetchBytes+1))
	if err != nil {
		return nil, err
	}
	if len(data) > maxFetchBytes {
		return nil, fmt.Errorf("catalog fetch: response exceeds 256 KB")
	}
	return data, nil
}

func writeCacheAtomic(path string, entries []Entry) error {
	data, err := json.Marshal(jsonDocument{SchemaVersion: 1, Entries: entries})
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return err
	}
	return writeAtomicFile(path, data)
}

func writeAtomicFile(path string, data []byte) (err error) {
	tmp, err := os.CreateTemp(filepath.Dir(path), ".catalog-*")
	if err != nil {
		return err
	}
	defer func() {
		if err != nil {
			_ = os.Remove(tmp.Name())
		}
	}()
	if _, err = tmp.Write(data); err != nil {
		_ = tmp.Close()
		return err
	}
	if err = tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), path)
}

func parseJSON(data []byte) ([]Entry, error) {
	var doc jsonDocument
	if err := json.Unmarshal(data, &doc); err != nil {
		return nil, fmt.Errorf("parse catalog: %w", err)
	}
	if doc.SchemaVersion != 1 {
		return nil, fmt.Errorf("catalog schema_version must be 1")
	}
	return validateEntries(doc.Entries)
}
