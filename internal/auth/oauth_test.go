package auth_test

import (
	"context"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"golang.org/x/oauth2"

	"github.com/mcpmini/mini/internal/auth"
	"github.com/mcpmini/mini/internal/auth/authtest"
	"github.com/mcpmini/mini/internal/config"
	"github.com/mcpmini/mini/internal/testutil"
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
	return token.Token
}

type saveLoginParams struct {
	ConfigDir  string
	ServerName string
	Token      *oauth2.Token
}

func saveLogin(t *testing.T, p saveLoginParams) error {
	t.Helper()
	return auth.SaveLogin(
		p.ConfigDir,
		p.ServerName,
		authtest.LoginFor(t, config.ServerConfig{URL: "https://mcp.example.com/mcp"}, p.Token),
	)
}

func TestTokenSaveLoad(t *testing.T) {
	mock := authtest.NewTokenServer(t)
	dir := t.TempDir()
	token := pkceToken(t, mock.AuthConfig())
	if err := saveLogin(t, saveLoginParams{
		ConfigDir:  dir,
		ServerName: "myserver",
		Token:      token,
	}); err != nil {
		t.Fatalf("Save: %v", err)
	}
	loaded, err := auth.LoadLogin(dir, "myserver")
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if loaded.Token.AccessToken != token.AccessToken {
		t.Errorf("loaded token = %q, want %q", loaded.Token.AccessToken, token.AccessToken)
	}
	if !loaded.Token.Valid() {
		t.Error("loaded token should be valid")
	}
}

func TestIsNotFound(t *testing.T) {
	_, err := auth.LoadLogin(t.TempDir(), "nonexistent")
	if !auth.IsNotFound(err) {
		t.Errorf("expected IsNotFound=true for missing token, got false (err: %v)", err)
	}
}

func TestSave_tokenFilePermissions(t *testing.T) {
	mock := authtest.NewTokenServer(t)
	dir := t.TempDir()
	if err := saveLogin(t, saveLoginParams{
		ConfigDir:  dir,
		ServerName: "myserver",
		Token:      pkceToken(t, mock.AuthConfig()),
	}); err != nil {
		t.Fatalf("Save: %v", err)
	}
	assertTokenFilesPrivate(t, dir+"/internal")
}

func TestSaveTightensExistingTokenPermissions(t *testing.T) {
	dir := t.TempDir()
	path := dir + "/internal/myserver.token.json"
	if err := os.MkdirAll(dir+"/internal", 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(
		path,
		[]byte(`{}`),
		0o644,
	); err != nil { //fileiolint:allow loose permissions exercise credential hardening
		t.Fatal(err)
	}
	if err := saveLogin(t, saveLoginParams{
		ConfigDir:  dir,
		ServerName: "myserver",
		Token:      &oauth2.Token{AccessToken: "secret"},
	}); err != nil {
		t.Fatalf("Save: %v", err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if got := info.Mode().Perm(); got != 0o600 {
		t.Errorf("token permissions = %#o, want 0600", got)
	}
}

func TestSaveReplacesSymlinkInsteadOfFollowingIt(t *testing.T) {
	dir := t.TempDir()
	internal := dir + "/internal"
	if err := os.MkdirAll(internal, 0o700); err != nil {
		t.Fatal(err)
	}
	target := dir + "/target"
	testutil.WriteFile(t, target, "unchanged")
	path := internal + "/myserver.token.json"
	if err := os.Symlink(target, path); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	if err := saveLogin(t, saveLoginParams{
		ConfigDir:  dir,
		ServerName: "myserver",
		Token:      &oauth2.Token{AccessToken: "secret"},
	}); err != nil {
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
		if info.Mode().Perm()&0o077 != 0 {
			t.Errorf("token file %s has world/group-readable permissions: %v", e.Name(), info.Mode().Perm())
		}
	}
}

func TestSave_invalidServerName(t *testing.T) {
	token := &oauth2.Token{AccessToken: "tok"}
	err := saveLogin(t, saveLoginParams{
		ConfigDir:  t.TempDir(),
		ServerName: "bad name!",
		Token:      token,
	})
	if err == nil {
		t.Error("expected error for invalid server name")
	}
}

func TestLoad_invalidServerName(t *testing.T) {
	_, err := auth.LoadLogin(t.TempDir(), "bad name!")
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
		t.Errorf(
			"resource = %q, want %q — ExtraAuthParams must not override computed resource",
			got,
			"https://resource.example.com",
		)
	}
	if got := q.Get("prompt"); got != "consent" {
		t.Errorf("prompt = %q, want consent — ExtraAuthParams should pass through", got)
	}
}

func TestTokenValidAfterForcedExpiry(t *testing.T) {
	mock := authtest.NewTokenServer(t)
	dir := t.TempDir()
	if err := saveLogin(t, saveLoginParams{
		ConfigDir:  dir,
		ServerName: "srv",
		Token:      pkceToken(t, mock.AuthConfig()),
	}); err != nil {
		t.Fatal(err)
	}
	loaded, _ := auth.LoadLogin(dir, "srv")
	loaded.Token.Expiry = time.Date(2020, 1, 1, 0, 0, 0, 0, time.UTC)
	if err := saveLogin(t, saveLoginParams{
		ConfigDir:  dir,
		ServerName: "srv",
		Token:      loaded.Token,
	}); err != nil {
		t.Fatal(err)
	}
	reloaded, _ := auth.LoadLogin(dir, "srv")
	if reloaded.Token.Valid() {
		t.Error("token should be invalid after forced expiry")
	}
}

func TestLoadLogin_withoutIssuerRequiresLogin(t *testing.T) {
	dir := t.TempDir()
	testutil.WriteFile(
		t,
		filepath.Join(dir, "internal", "srv.token.json"),
		`{"resource":"https://resource.example/mcp","token_url":"https://shared.example/token","client":{"id":"old-client"},"token":{"access_token":"old-token","refresh_token":"old-refresh"}}`,
	)
	_, err := auth.LoadLogin(dir, "srv")
	if err == nil || !strings.Contains(err.Error(), "log in again") {
		t.Fatalf("unbound login load error = %v", err)
	}
}

func TestSavedLoginRetainsIssuerAndRejectsConfiguredIssuerChange(t *testing.T) {
	sc := config.ServerConfig{Name: "srv", URL: "https://resource.example/mcp", Auth: &config.AuthConfig{
		Type:     "oauth2",
		Issuer:   "https://issuer.example",
		ClientID: "configured",
		TokenURL: "https://shared.example/token",
	}}
	dir := t.TempDir()
	authtest.SaveToken(
		t,
		authtest.TokenFile{ConfigDir: dir, ServerName: "srv", Server: sc, Token: &oauth2.Token{AccessToken: "token"}},
	)
	login, err := auth.LoadLoginFor(dir, sc)
	if err != nil || login.Issuer != sc.Auth.Issuer {
		t.Fatalf("saved login issuer = %+v, %v", login, err)
	}
	if got := login.RefreshConfig(sc.Auth); got.Issuer != login.Issuer {
		t.Fatalf("refresh lost issuer: %+v", got)
	}
	sc.Auth.Issuer += "/"
	if _, err := auth.LoadLoginFor(dir, sc); err == nil || !strings.Contains(err.Error(), "issuer changed") {
		t.Fatalf("changed issuer load error = %v", err)
	}
}
