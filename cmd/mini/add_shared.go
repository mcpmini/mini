package main

import (
	"fmt"
	"os"
	"path/filepath"

	"gopkg.in/yaml.v3"

	"github.com/mcpmini/mini/internal/config"
)

type serverYAML struct {
	Name        string            `yaml:"name"`
	Transport   string            `yaml:"transport,omitempty"`
	URL         string            `yaml:"url,omitempty"`
	Command     string            `yaml:"command,omitempty"`
	Args        []string          `yaml:"args,omitempty"`
	Env         []string          `yaml:"env,omitempty"`
	Headers     map[string]string `yaml:"headers,omitempty"`
	Permissions *permissionsYAML  `yaml:"permissions,omitempty"`
}

type permissionsYAML struct {
	Protected []string `yaml:"protected,omitempty"`
	Hidden    []string `yaml:"hidden,omitempty"`
}

const maxImportConfigBytes = 10 << 20 // 10 MB

func readConfigFile(path string) []byte {
	info, err := os.Stat(path)
	if err != nil {
		fatalf("stat %s: %v", path, err)
	}
	if info.Size() > maxImportConfigBytes {
		fatalf("%s is too large (%d bytes)", path, info.Size())
	}
	data, err := os.ReadFile(path)
	if err != nil {
		fatalf("read %s: %v", path, err)
	}
	return data
}

func envList(env map[string]string) []string {
	out := make([]string, 0, len(env))
	for k, v := range env {
		out = append(out, k+"="+v)
	}
	return out
}

func writeServerYAML(configDir, name string, sc serverYAML) {
	if !config.ValidServerName.MatchString(name) {
		fatalf("invalid server name %q: must match ^[a-zA-Z0-9_-]+$", name)
	}
	dir := filepath.Join(configDir, "servers")
	if err := os.MkdirAll(dir, 0700); err != nil {
		fatalf("create servers dir: %v", err)
	}
	path := filepath.Join(dir, name+".yaml")
	data, _ := yaml.Marshal(sc)
	if err := os.WriteFile(path, data, 0600); err != nil {
		fatalf("write %s: %v", path, err)
	}
	fmt.Printf("added %s → %s\n", name, path)
	installBundledProjection(configDir, sc)
}
