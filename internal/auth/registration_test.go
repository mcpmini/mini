//go:build test

package auth_test

import (
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

func TestSaveRegistration_createsDir(t *testing.T) {
	dir := t.TempDir()
	// registrations/ subdir doesn't exist yet — Save should create it
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
