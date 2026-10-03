package authtest

import (
	"encoding/json"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	"github.com/mcpmini/mini/internal/auth"
	"github.com/mcpmini/mini/internal/testutil"
	"golang.org/x/oauth2"
)

func TestSaveTokenWritesSuppliedValuesAndReplacesExisting(t *testing.T) {
	dir := t.TempDir()
	SaveToken(t, TokenFile{
		ConfigDir:  dir,
		ServerName: "svc",
		Token: &oauth2.Token{
			AccessToken:  "old",
			RefreshToken: "old-refresh",
		},
	})
	want := &oauth2.Token{AccessToken: "new", TokenType: "Bearer", Expiry: time.Date(2099, 1, 1, 0, 0, 0, 0, time.UTC)}
	SaveToken(t, TokenFile{ConfigDir: dir, ServerName: "svc", Token: want})
	var got oauth2.Token
	if err := json.Unmarshal(testutil.ReadFile(t, filepath.Join(dir, "internal", "svc.token.json")), &got); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(&got, want) {
		t.Fatalf("saved token = %#v, want %#v", &got, want)
	}
}

func TestSaveRegistrationWritesSuppliedValuesAndReplacesExisting(t *testing.T) {
	dir := t.TempDir()
	SaveRegistration(t, RegistrationFile{
		ConfigDir:  dir,
		ServerName: "svc",
		Registration: &auth.Registration{
			ClientID:     "old",
			ClientSecret: "old-secret",
		},
	})
	want := &auth.Registration{ClientID: "new", TokenEndpointAuthMethod: "none"}
	SaveRegistration(t, RegistrationFile{ConfigDir: dir, ServerName: "svc", Registration: want})
	var got auth.Registration
	if err := json.Unmarshal(testutil.ReadFile(t, filepath.Join(dir, "internal", "svc.dcr.json")), &got); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(&got, want) {
		t.Fatalf("saved registration = %#v, want %#v", &got, want)
	}
}
