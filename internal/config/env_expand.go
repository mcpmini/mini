package config

import (
	"fmt"
	"os"
	"strings"
)

type envExpansionMode uint8

const (
	strictEnvExpansion envExpansionMode = iota
	lenientEnvExpansion
)

func expandEnvValue(value string, mode envExpansionMode) (string, error) {
	var missing []string
	seen := make(map[string]bool)
	expanded := envVarRef.ReplaceAllStringFunc(value, func(ref string) string {
		name := ref[2 : len(ref)-1]
		if expanded, ok := os.LookupEnv(name); ok {
			return expanded
		}
		if mode == lenientEnvExpansion {
			return ref
		}
		if !seen[name] {
			missing = append(missing, name)
			seen[name] = true
		}
		return ref
	})
	if len(missing) > 0 {
		return "", fmt.Errorf("undefined environment variable(s): %s", strings.Join(missing, ", "))
	}
	return expanded, nil
}

func expandServerSecrets(sc *ServerConfig, mode envExpansionMode) error {
	for name, value := range sc.Headers {
		if err := expandServerField(mode, "headers."+name, &value); err != nil {
			return err
		}
		sc.Headers[name] = value
	}
	for i := range sc.Env {
		if err := expandServerField(mode, fmt.Sprintf("env[%d]", i), &sc.Env[i]); err != nil {
			return err
		}
	}
	if sc.Auth != nil {
		if err := expandServerField(mode, "auth.token", &sc.Auth.Token); err != nil {
			return err
		}
		if err := expandServerField(mode, "auth.client_secret", &sc.Auth.ClientSecret); err != nil {
			return err
		}
	}
	return nil
}

func expandServerField(mode envExpansionMode, field string, value *string) error {
	expanded, err := expandEnvValue(*value, mode)
	if err != nil {
		return fmt.Errorf("%s: %w", field, err)
	}
	*value = expanded
	return nil
}

func checkUnexpandedFields(sc ServerConfig) error {
	names := []string{"url", "command"}
	values := []string{sc.URL, sc.Command}
	for i, arg := range sc.Args {
		names = append(names, fmt.Sprintf("args[%d]", i))
		values = append(values, arg)
	}
	for i, value := range values {
		if ref := envVarRef.FindString(value); ref != "" {
			return fmt.Errorf("server %s: %s: %s isn't expanded; put secrets in headers, env, auth.token or auth.client_secret", sc.Name, names[i], ref)
		}
	}
	return nil
}
