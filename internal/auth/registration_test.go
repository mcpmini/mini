//go:build test

package auth_test

import (
	"os"
	"testing"

	"github.com/mcpmini/mini/internal/auth"
)

func TestSaveLoadRegistration(t *testing.T) {
	dir := t.TempDir()
	reg := &auth.Registration{ClientID: "my-client-id"}

	if err := auth.SaveRegistration(dir, "myserver", reg); err != nil {
		t.Fatalf("save: %v", err)
	}
	loaded, err := auth.LoadRegistration(dir, "myserver")
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if loaded.ClientID != "my-client-id" {
		t.Errorf("got %q", loaded.ClientID)
	}
}

func TestLoadRegistration_notFound(t *testing.T) {
	_, err := auth.LoadRegistration(t.TempDir(), "missing")
	if !auth.IsNotFound(err) {
		t.Errorf("expected not-found error, got %v", err)
	}
}

func TestSaveRegistration_invalidName(t *testing.T) {
	if err := auth.SaveRegistration(t.TempDir(), "bad name!", &auth.Registration{ClientID: "x"}); err == nil {
		t.Error("expected error for invalid server name")
	}
}

func TestLoadRegistration_invalidName(t *testing.T) {
	if _, err := auth.LoadRegistration(t.TempDir(), "bad name!"); err == nil {
		t.Error("expected error for invalid server name")
	}
}

func TestSaveLoadRegistration_confidentialClientFields(t *testing.T) {
	dir := t.TempDir()
	reg := &auth.Registration{
		ClientID:                "confidential-id",
		ClientSecret:            "sk-test-usurp-roundtrip",
		TokenEndpointAuthMethod: "client_secret_basic",
		ClientSecretExpiresAt:   1234567890,
	}
	if err := auth.SaveRegistration(dir, "myserver", reg); err != nil {
		t.Fatalf("save: %v", err)
	}
	loaded, err := auth.LoadRegistration(dir, "myserver")
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if loaded.ClientSecret != reg.ClientSecret {
		t.Errorf("ClientSecret = %q, want %q", loaded.ClientSecret, reg.ClientSecret)
	}
	if loaded.TokenEndpointAuthMethod != reg.TokenEndpointAuthMethod {
		t.Errorf("TokenEndpointAuthMethod = %q, want %q", loaded.TokenEndpointAuthMethod, reg.TokenEndpointAuthMethod)
	}
	if loaded.ClientSecretExpiresAt != reg.ClientSecretExpiresAt {
		t.Errorf("ClientSecretExpiresAt = %d, want %d", loaded.ClientSecretExpiresAt, reg.ClientSecretExpiresAt)
	}
}

func TestSaveRegistration_filePermissions(t *testing.T) {
	dir := t.TempDir()
	if err := auth.SaveRegistration(dir, "myserver", &auth.Registration{ClientID: "id1", ClientSecret: "secret"}); err != nil {
		t.Fatalf("save: %v", err)
	}
	info, err := os.Stat(dir + "/internal/myserver.dcr.json")
	if err != nil {
		t.Fatal(err)
	}
	if got := info.Mode().Perm(); got != 0600 {
		t.Errorf("registration file permissions = %#o, want 0600", got)
	}
}

func TestSaveRegistration_overwritesExistingAtomically(t *testing.T) {
	dir := t.TempDir()
	if err := auth.SaveRegistration(dir, "myserver", &auth.Registration{ClientID: "old-id", ClientSecret: "old-secret"}); err != nil {
		t.Fatalf("first save: %v", err)
	}
	if err := auth.SaveRegistration(dir, "myserver", &auth.Registration{ClientID: "new-id", ClientSecret: "new-secret"}); err != nil {
		t.Fatalf("second save: %v", err)
	}
	loaded, err := auth.LoadRegistration(dir, "myserver")
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if loaded.ClientID != "new-id" || loaded.ClientSecret != "new-secret" {
		t.Errorf("expected overwrite to replace fields, got %+v", loaded)
	}
}

func TestSaveRegistration_createsDir(t *testing.T) {
	dir := t.TempDir()
	if err := auth.SaveRegistration(dir, "svc", &auth.Registration{ClientID: "id1"}); err != nil {
		t.Fatalf("save: %v", err)
	}
	reg, err := auth.LoadRegistration(dir, "svc")
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if reg.ClientID != "id1" {
		t.Errorf("got %q", reg.ClientID)
	}
}
