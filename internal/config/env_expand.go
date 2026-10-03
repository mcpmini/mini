package config

import (
	"fmt"
	"maps"
	"os"
	"slices"
	"strings"
)

// UnsetEnvError is a ${VAR} whose variable isn't set where mini runs.
type UnsetEnvError struct {
	Field string
	Names []string
}

func (e *UnsetEnvError) Error() string {
	verb := "isn't"
	if len(e.Names) > 1 {
		verb = "aren't"
	}
	return fmt.Sprintf("%s: %s %s set where mini runs", e.Field, strings.Join(e.Names, ", "), verb)
}

func expandEnvValue(field, value string) (string, error) {
	var missing []string
	expanded := envVarRef.ReplaceAllStringFunc(value, func(ref string) string {
		name := ref[2 : len(ref)-1]
		if expanded, ok := os.LookupEnv(name); ok {
			return expanded
		}
		if !slices.Contains(missing, name) {
			missing = append(missing, name)
		}
		return ref
	})
	if len(missing) > 0 {
		return "", &UnsetEnvError{Field: field, Names: missing}
	}
	return expanded, nil
}

// expandServerEnv runs only on a config just parsed from its file: a value merged in later, such
// as a client secret from the OAuth server, must never be expanded. A config with an unset
// variable stays as written, so code that never connects with it, like mini init in a shell
// without the variable, can still use it.
func expandServerEnv(sc *ServerConfig) {
	expanded := *sc
	expanded.Headers = maps.Clone(sc.Headers)
	expanded.Env = slices.Clone(sc.Env)
	if err := expandEnvFields(&expanded); err != nil {
		sc.UnsetEnv = fmt.Errorf("server %s: %w", sc.Name, err)
		return
	}
	*sc = expanded
}

func expandEnvFields(sc *ServerConfig) error {
	for name, value := range sc.Headers {
		if err := expandField("headers."+name, &value); err != nil {
			return err
		}
		sc.Headers[name] = value
	}
	for i := range sc.Env {
		if err := expandField(fmt.Sprintf("env[%d]", i), &sc.Env[i]); err != nil {
			return err
		}
	}
	if sc.Auth == nil {
		return nil
	}
	auth := *sc.Auth
	sc.Auth = &auth
	if err := expandField("auth.token", &auth.Token); err != nil {
		return err
	}
	return expandField("auth.client_secret", &auth.ClientSecret)
}

func expandField(field string, value *string) error {
	expanded, err := expandEnvValue(field, *value)
	if err != nil {
		return err
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
			return fmt.Errorf("server %s: %s: %s isn't expanded; ${VAR} is only expanded in headers, env, auth.token and auth.client_secret", sc.Name, names[i], ref)
		}
	}
	return nil
}
