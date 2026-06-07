package config

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"gopkg.in/yaml.v3"
)

// LoadPipes reads all *.yaml files from dir/pipes/ and returns the parsed PipeConfigs.
// All errors are accumulated and returned together rather than failing fast.
func LoadPipes(dir string) ([]PipeConfig, error) {
	paths, err := filepath.Glob(filepath.Join(dir, "pipes", "*.yaml"))
	if err != nil {
		return nil, err
	}
	var (
		pipes []PipeConfig
		errs  []error
	)
	for _, p := range paths {
		pipe, err := loadPipeFile(p)
		if err != nil {
			errs = append(errs, err)
			continue
		}
		pipes = append(pipes, *pipe)
	}
	return pipes, errors.Join(errs...)
}

func loadPipeFile(path string) (*PipeConfig, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", path, err)
	}
	pipe, err := parsePipeConfig(path, data)
	if err != nil {
		return nil, err
	}
	return pipe, nil
}

func parsePipeConfig(path string, data []byte) (*PipeConfig, error) {
	var raw map[string]any
	if err := yaml.Unmarshal(data, &raw); err != nil {
		return nil, fmt.Errorf("parse %s: %w", path, err)
	}
	if err := rejectParallelKey(path, raw); err != nil {
		return nil, err
	}
	var pipe PipeConfig
	if err := yaml.Unmarshal(data, &pipe); err != nil {
		return nil, fmt.Errorf("parse %s: %w", path, err)
	}
	return &pipe, validatePipeConfig(path, pipe)
}

func rejectParallelKey(path string, raw map[string]any) error {
	steps, _ := raw["steps"].([]any)
	for _, s := range steps {
		step, ok := s.(map[string]any)
		if !ok {
			continue
		}
		if _, found := step["parallel"]; found {
			name, _ := raw["name"].(string)
			return fmt.Errorf("pipe %q: parallel steps are not supported in this version", name)
		}
	}
	return nil
}

func validatePipeConfig(path string, pipe PipeConfig) error {
	var errs []error
	stem := filepath.Base(path[:len(path)-5])
	if pipe.Name == "" {
		errs = append(errs, fmt.Errorf("pipe %s: name is required", path))
	} else if pipe.Name != stem {
		errs = append(errs, fmt.Errorf("pipe %s: name %q must match filename stem %q", path, pipe.Name, stem))
	}
	if len(pipe.Steps) == 0 {
		errs = append(errs, fmt.Errorf("pipe %q: steps must not be empty", pipe.Name))
	}
	errs = append(errs, validatePipeSteps(pipe)...)
	return errors.Join(errs...)
}

func validatePipeSteps(pipe PipeConfig) []error {
	var errs []error
	seen := make(map[string]bool)
	for _, step := range pipe.Steps {
		if err := validateStep(pipe.Name, step, seen); err != nil {
			errs = append(errs, err)
		}
		if step.ID != "" {
			seen[step.ID] = true
		}
	}
	return errs
}

func validateStep(pipeName string, step StepConfig, seen map[string]bool) error {
	var errs []error
	if step.ID == "" {
		errs = append(errs, fmt.Errorf("pipe %q: step is missing id", pipeName))
	} else if seen[step.ID] {
		errs = append(errs, fmt.Errorf("pipe %q: duplicate step id %q", pipeName, step.ID))
	}
	if len(step.Set) > 0 && (step.Server != "" || step.Tool != "") {
		errs = append(errs, fmt.Errorf("pipe %q step %q: set and server/tool are mutually exclusive", pipeName, step.ID))
	}
	if step.Server != "" && !ValidServerName.MatchString(step.Server) {
		errs = append(errs, fmt.Errorf("pipe %q step %q: invalid server name %q", pipeName, step.ID, step.Server))
	}
	return errors.Join(errs...)
}

// EnsurePipesDir creates the pipes directory under configDir if it does not exist.
func EnsurePipesDir(configDir string) error {
	dir := filepath.Join(configDir, "pipes")
	if err := os.MkdirAll(dir, 0700); err != nil {
		return fmt.Errorf("create pipes dir: %w", err)
	}
	return nil
}

