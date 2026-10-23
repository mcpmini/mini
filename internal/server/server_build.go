package server

import (
	"log/slog"
	"os"
	"path/filepath"

	"github.com/mcpmini/mini/internal/auth/provider"
	"github.com/mcpmini/mini/internal/clock"
	"github.com/mcpmini/mini/internal/config"
	"github.com/mcpmini/mini/internal/projection"
	"github.com/mcpmini/mini/internal/registry"
	"github.com/mcpmini/mini/internal/response"
)

// Params configures a new Server. Config, ConfigDir, and Logger are required.
type Params struct {
	Config          *config.Config
	ConfigDir       string
	Logger          *slog.Logger
	Clock           clock.Clock
	DaemonAuthToken string
}

func New(p Params) *Server {
	requireParams(p)
	servers, err := config.LoadServers(p.ConfigDir)
	if err != nil {
		p.Logger.Warn("starting without projections", "err", err)
	}
	s := newServer(serverInitParams{
		Config: p.Config, ConfigDir: p.ConfigDir,
		Projections: serverProjections(servers.Loaded), Logger: p.Logger,
	})
	applyParams(s, p)
	store := mustStore(storeInitParams{Config: p.Config, ConfigDir: p.ConfigDir, Logger: p.Logger, Clock: s.clock})
	s.store = store
	s.sessions = newSessionStore(s.clock)
	return s
}

func requireParams(p Params) {
	if p.Config == nil {
		panic("server.Params.Config must not be nil")
	}
	if p.ConfigDir == "" {
		panic("server.Params.ConfigDir must not be empty")
	}
	if p.Logger == nil {
		panic("server.Params.Logger must not be nil")
	}
}

func applyParams(s *Server, p Params) {
	if p.Clock != nil {
		s.clock = p.Clock
	}
	s.daemonAuthToken = p.DaemonAuthToken
}

type serverInitParams struct {
	Config      *config.Config
	ConfigDir   string
	Projections map[string]map[string]*config.ProjectionConfig
	Logger      *slog.Logger
}

func newServer(p serverInitParams) *Server {
	s := &Server{
		cfg:              p.Config,
		configDir:        p.ConfigDir,
		reg:              registry.New(),
		upstreams:        make(map[string]*upstreamServer),
		configServers:    make(map[string]configServer),
		startup:          newStartupTracker(),
		stopWaiting:      make(chan struct{}),
		removeGen:        make(map[string]uint64),
		projections:      p.Projections,
		projDefaults:     projection.DefaultsFrom(p.Config),
		authFlows:        make(map[string]*authFlowState),
		logger:           p.Logger,
		clock:            clock.System(),
		providerRegistry: provider.NewRegistry(),
	}
	s.connector = newUpstreamConnector(s.connectUntilRegistered)
	return s
}

type storeInitParams struct {
	Config    *config.Config
	ConfigDir string
	Logger    *slog.Logger
	Clock     clock.Clock
}

func mustStore(p storeInitParams) *response.Store {
	storeCfg := response.StoreConfigFrom(p.Config, p.ConfigDir)
	storeCfg.Clock = p.Clock
	store, err := response.NewStore(storeCfg)
	if err == nil {
		return store
	}
	p.Logger.Error("failed to create response store, falling back to tmp", "err", err)
	storeCfg.Dir = filepath.Join(os.TempDir(), "mini-responses")
	store, err = response.NewStore(storeCfg)
	if err != nil {
		panic("failed to create fallback response store: " + err.Error())
	}
	return store
}
