package main

import (
	"fmt"
	"log/slog"
	"os"
	"path/filepath"

	"github.com/mcpmini/mini/internal/clock"
	"github.com/mcpmini/mini/internal/config"
	"github.com/mcpmini/mini/internal/response"
)

type callStoreParams struct {
	Config    *config.Config
	ConfigDir string
	Logger    *slog.Logger
	Clock     clock.Clock
}

func newCallStore(params callStoreParams) (*response.Store, error) {
	storeConfig := response.StoreConfigFrom(params.Config, params.ConfigDir)
	storeConfig.Clock = params.Clock
	store, err := response.NewStore(storeConfig)
	if err == nil {
		return store, nil
	}
	params.Logger.Warn("could not open response store, using temp dir", "err", err)
	storeConfig.Dir = filepath.Join(os.TempDir(), "mini-responses")
	store, err = response.NewStore(storeConfig)
	if err != nil {
		return nil, fmt.Errorf("open fallback response store: %w", err)
	}
	return store, nil
}
