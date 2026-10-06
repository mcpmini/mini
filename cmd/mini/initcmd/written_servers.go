package initcmd

import (
	"slices"

	"github.com/mcpmini/mini/internal/agents"
	"github.com/mcpmini/mini/internal/config"
)

// WrittenServers are mini's servers as their files hold them, before ${VAR} expansion, so they
// compare with agent entries, which hold references unexpanded too.
type WrittenServers []config.ServerConfig

func readWrittenServers(configDir string) (WrittenServers, error) {
	servers, err := config.LoadServers(configDir)
	if err != nil {
		return nil, err
	}
	var written WrittenServers
	for _, sc := range servers.Loaded {
		w, err := config.ReadUnexpandedServer(configDir, sc.Name)
		if err != nil {
			w = config.ServerConfig{Name: sc.Name}
		}
		written = append(written, w)
	}
	// A file that doesn't load still holds its name.
	for _, broken := range servers.Broken {
		written = append(written, config.ServerConfig{Name: broken.ServerName})
	}
	return written, nil
}

func (w WrittenServers) hasSame(sc config.ServerConfig) bool {
	return slices.ContainsFunc(w, func(written config.ServerConfig) bool { return agents.SameServer(written, sc) })
}
