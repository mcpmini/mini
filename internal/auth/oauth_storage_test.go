package auth_test

import (
	"os"
	"testing"
	"time"

	"golang.org/x/oauth2"

	"github.com/mcpmini/mini/internal/auth"
)

func TestTokenSaveLoad(t *testing.T) {
	mock := newMockAuthServer(t)
	dir := t.TempDir()
	token := pkceToken(t, mock)
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
	mock := newMockAuthServer(t)
	dir := t.TempDir()
	if err := auth.Save(dir, "myserver", pkceToken(t, mock)); err != nil {
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
	if err := os.WriteFile(path, []byte(`{}`), 0644); err != nil {
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
	if err := os.WriteFile(target, []byte("unchanged"), 0600); err != nil {
		t.Fatal(err)
	}
	path := internal + "/myserver.token.json"
	if err := os.Symlink(target, path); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	if err := auth.Save(dir, "myserver", &oauth2.Token{AccessToken: "secret"}); err != nil {
		t.Fatalf("Save: %v", err)
	}
	got, err := os.ReadFile(target)
	if err != nil {
		t.Fatal(err)
	}
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

func TestTokenValidAfterForcedExpiry(t *testing.T) {
	mock := newMockAuthServer(t)
	dir := t.TempDir()
	if err := auth.Save(dir, "srv", pkceToken(t, mock)); err != nil {
		t.Fatalf("Save: %v", err)
	}
	loaded, _ := auth.Load(dir, "srv")
	loaded.Expiry = time.Date(2020, 1, 1, 0, 0, 0, 0, time.UTC)
	auth.Save(dir, "srv", loaded) //nolint:errcheck
	reloaded, _ := auth.Load(dir, "srv")
	if reloaded.Valid() {
		t.Error("token should be invalid after forced expiry")
	}
}
