//go:build test

package server

import (
	"errors"
	"testing"
)

func TestValidateStorePathWith_RejectsResolverFailures(t *testing.T) {
	for _, tc := range []struct {
		name      string
		failOn    string
		wantCalls []string
	}{
		{name: "store directory", failOn: "/store", wantCalls: []string{"/store"}},
		{name: "requested path", failOn: "/store/file.json", wantCalls: []string{"/store", "/store/file.json"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cause := errors.New("resolution failed")
			var calls []string
			err := validateStorePathWith(validateStorePathParams{
				StoreDir: "/store",
				Path:     "/store/file.json",
				Resolve: func(path string) (string, error) {
					calls = append(calls, path)
					if path == tc.failOn {
						return "", cause
					}
					return path, nil
				},
			})
			if !errors.Is(err, errInvalidParams) {
				t.Fatalf("error = %v, want errInvalidParams", err)
			}
			if !errors.Is(err, cause) {
				t.Fatalf("error = %v, want original resolver cause", err)
			}
			if len(calls) != len(tc.wantCalls) {
				t.Fatalf("resolver calls = %v, want %v", calls, tc.wantCalls)
			}
			for i, got := range calls {
				if got != tc.wantCalls[i] {
					t.Fatalf("resolver calls = %v, want %v", calls, tc.wantCalls)
				}
			}
		})
	}
}
