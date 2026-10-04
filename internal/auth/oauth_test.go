package auth_test

import (
	"context"
	"net/url"
	"os"
	"testing"
	"time"

	"github.com/mcpmini/mini/internal/auth"
	"github.com/mcpmini/mini/internal/auth/authtest"
	"github.com/mcpmini/mini/internal/config"
	"github.com/mcpmini/mini/internal/testutil"
	"golang.org/x/oauth2"
)

func pkceToken(t *testing.T, ac *config.AuthConfig) *oauth2.Token {
	t.Helper()
	login := authtest.StartLogin(t, ac)
	authtest.CompleteAuthorization(t, login.AuthURL(), "test-auth-code")
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	token, err := login.Wait(ctx)
	if err != nil {
		t.Fatalf("browser login: %v", err)
	}
	return token
}

func TestTokenSaveLoad(t *testing.T) {
	mock := authtest.NewTokenServer(t)
	dir := t.TempDir()
	token := pkceToken(t, mock.AuthConfig())
	if err := auth.Save(dir, "myserver", token); err != nil {
		t.Fatalf("Save: %v", err)
	}
	loaded, err := auth.Load(dir, "myserver")
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if loaded.AccessToken != token.AccessToken {
		t.Errorf("loaded token = %q, want %q", loaded.AccessToken, token.AccessToken)
	}
	if !loaded.Valid() {
		t.Error("loaded token should be valid")
	}
}

func TestIsNotFound(t *testing.T) {
	_, err := auth.Load(t.TempDir(), "nonexistent")
	if !auth.IsNotFound(err) {
		t.Errorf("expected IsNotFound=true for missing token, got false (err: %v)", err)
	}
}

func TestSave_tokenFilePermissions(t *testing.T) {
	mock := authtest.NewTokenServer(t)
	dir := t.TempDir()
	if err := auth.Save(dir, "myserver", pkceToken(t, mock.AuthConfig())); err != nil {
		t.Fatalf("Save: %v", err)
	}
	assertTokenFilesPrivate(t, dir+"/internal")
}

func TestSaveTightensExistingTokenPermissions(t *testing.T) {
	dir := t.TempDir()
	path := dir + "/internal/myserver.token.json"
	if err := os.MkdirAll(dir+"/internal", 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(`{}`), 0644); err != nil { //fileiolint:allow loose permissions exercise credential hardening
		t.Fatal(err)
	}
	if err := auth.Save(dir, "myserver", &oauth2.Token{AccessToken: "secret"}); err != nil {
		t.Fatalf("Save: %v", err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if got := info.Mode().Perm(); got != 0600 {
		t.Errorf("token permissions = %#o, want 0600", got)
	}
}

func TestSaveReplacesSymlinkInsteadOfFollowingIt(t *testing.T) {
	dir := t.TempDir()
	internal := dir + "/internal"
	if err := os.MkdirAll(internal, 0700); err != nil {
		t.Fatal(err)
	}
	target := dir + "/target"
	testutil.WriteFile(t, target, "unchanged")
	path := internal + "/myserver.token.json"
	if err := os.Symlink(target, path); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	if err := auth.Save(dir, "myserver", &oauth2.Token{AccessToken: "secret"}); err != nil {
		t.Fatalf("Save: %v", err)
	}
	got := testutil.ReadFile(t, target)
	if string(got) != "unchanged" {
		t.Errorf("symlink target was overwritten: %q", got)
	}
	info, err := os.Lstat(path)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode()&os.ModeSymlink != 0 {
		t.Error("token path remained a symlink")
	}
}

func assertTokenFilesPrivate(t *testing.T, tokensDir string) {
	t.Helper()
	entries, err := os.ReadDir(tokensDir)
	if err != nil {
		t.Fatalf("ReadDir tokens: %v", err)
	}
	for _, e := range entries {
		info, err := e.Info()
		if err != nil {
			t.Fatalf("Stat %s: %v", e.Name(), err)
		}
		if info.Mode().Perm()&0077 != 0 {
			t.Errorf("token file %s has world/group-readable permissions: %v", e.Name(), info.Mode().Perm())
		}
	}
}

func TestSave_invalidServerName(t *testing.T) {
	token := &oauth2.Token{AccessToken: "tok"}
	err := auth.Save(t.TempDir(), "bad name!", token)
	if err == nil {
		t.Error("expected error for invalid server name")
	}
}

func TestLoad_invalidServerName(t *testing.T) {
	_, err := auth.Load(t.TempDir(), "bad name!")
	if err == nil {
		t.Error("expected error for invalid server name")
	}
}

func TestBuildAuthURL_extraParamsDoNotOverrideResource(t *testing.T) {
	mock := authtest.NewTokenServer(t)
	ac := mock.AuthConfig()
	ac.ResourceURL = "https://resource.example.com"
	ac.ExtraAuthParams = map[string]string{
		"resource": "https://evil.example.com",
		"prompt":   "consent",
	}

	login := authtest.StartLogin(t, ac)

	parsed, err := url.Parse(login.AuthURL())
	if err != nil {
		t.Fatalf("parse auth URL: %v", err)
	}
	q := parsed.Query()
	if got := q.Get("resource"); got != "https://resource.example.com" {
		t.Errorf("resource = %q, want %q — ExtraAuthParams must not override computed resource", got, "https://resource.example.com")
	}
	if got := q.Get("prompt"); got != "consent" {
		t.Errorf("prompt = %q, want consent — ExtraAuthParams should pass through", got)
	}
}

func TestTokenValidAfterForcedExpiry(t *testing.T) {
	mock := authtest.NewTokenServer(t)
	dir := t.TempDir()
	authtest.SaveToken(t, authtest.TokenFile{ConfigDir: dir, ServerName: "srv", Token: pkceToken(t, mock.AuthConfig())})
	loaded, _ := auth.Load(dir, "srv")
	loaded.Expiry = time.Date(2020, 1, 1, 0, 0, 0, 0, time.UTC)
	authtest.SaveToken(t, authtest.TokenFile{ConfigDir: dir, ServerName: "srv", Token: loaded})
	reloaded, _ := auth.Load(dir, "srv")
	if reloaded.Valid() {
		t.Error("token should be invalid after forced expiry")
	}
}
