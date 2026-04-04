package bench

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"gopkg.in/yaml.v3"

	"github.com/mcpmini/mini/internal/config"
)

// LoadFixtures walks benchDir/fixtures/<server>/<tool>.json and pairs each
// fixture with the corresponding projection config from benchDir/projections/<server>.yaml.
func LoadFixtures(benchDir string) ([]Case, error) {
	fixturesDir := filepath.Join(benchDir, "fixtures")
	serverDirs, err := os.ReadDir(fixturesDir)
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read fixtures dir %s: %w", fixturesDir, err)
	}
	return collectServerCases(fixturesDir, filepath.Join(benchDir, "projections"), serverDirs)
}

func collectServerCases(fixturesDir, projectionsDir string, serverDirs []os.DirEntry) ([]Case, error) {
	var cases []Case
	for _, serverDir := range serverDirs {
		if !serverDir.IsDir() {
			continue
		}
		server := serverDir.Name()
		projections, _ := loadProjectionFile(filepath.Join(projectionsDir, server+".yaml"))
		serverCases, err := loadServerFixtures(fixturesDir, server, projections)
		if err != nil {
			return nil, err
		}
		cases = append(cases, serverCases...)
	}
	return cases, nil
}

func loadServerFixtures(fixturesDir, server string, projections map[string]*config.ProjectionConfig) ([]Case, error) {
	toolFiles, err := os.ReadDir(filepath.Join(fixturesDir, server))
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var cases []Case
	for _, tf := range toolFiles {
		if tf.IsDir() || !strings.HasSuffix(tf.Name(), ".json") {
			continue
		}
		tool := strings.TrimSuffix(tf.Name(), ".json")
		raw, err := os.ReadFile(filepath.Join(fixturesDir, server, tf.Name()))
		if err != nil {
			return nil, fmt.Errorf("read fixture %s/%s: %w", server, tf.Name(), err)
		}
		cases = append(cases, Case{Server: server, Tool: tool, Raw: raw, ProjConfig: resolveProjection(projections, tool)})
	}
	return cases, nil
}

func resolveProjection(projections map[string]*config.ProjectionConfig, tool string) *config.ProjectionConfig {
	if projections == nil {
		return nil
	}
	if c, ok := projections[tool]; ok {
		return c
	}
	return projections["*"]
}

func loadProjectionFile(path string) (map[string]*config.ProjectionConfig, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var m map[string]*config.ProjectionConfig
	if err := yaml.Unmarshal(data, &m); err != nil {
		return nil, fmt.Errorf("parse %s: %w", path, err)
	}
	return m, nil
}
