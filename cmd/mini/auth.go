package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"runtime"
	"time"

	"golang.org/x/oauth2"

	"github.com/mcpmini/mini/internal/auth"
	"github.com/mcpmini/mini/internal/config"
)

func runAuth(configDir string, args []string) {
	fs := flag.NewFlagSet("auth", flag.ExitOnError)
	fs.Parse(args)
	if fs.NArg() == 0 {
		fatalf("usage: mini auth <server-name>")
	}
	serverName := fs.Arg(0)
	_, servers, err := config.Load(configDir)
	if err != nil {
		fatalf("load config: %v", err)
	}
	sc := findServer(servers, serverName)
	if sc == nil {
		fatalf("server not found: %s", serverName)
	}
	if sc.Auth == nil || sc.Auth.Type != "oauth2" {
		fatalf("server %q does not have oauth2 auth configured", serverName)
	}
	runPKCEFlow(configDir, serverName, sc.Auth)
}

func runPKCEFlow(configDir, serverName string, authCfg *config.AuthConfig) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	fmt.Printf("Authorizing %s...\n", serverName)
	token, err := auth.PKCEFlow(ctx, authCfg, openBrowser)
	if err != nil {
		fatalf("auth flow: %v", err)
	}
	if err := auth.Save(configDir, serverName, token); err != nil {
		fatalf("save token: %v", err)
	}
	printAuthResult(serverName, token.Expiry)
}

func printAuthResult(name string, expiry time.Time) {
	if expiry.IsZero() {
		fmt.Printf("authorized %s (no expiry)\n", name)
	} else {
		fmt.Printf("authorized %s (expires %s)\n", name, expiry.Format(time.RFC3339))
	}
}

func injectOAuthTokens(ctx context.Context, configDir string, servers []config.ServerConfig) {
	for i := range servers {
		sc := &servers[i]
		if sc.Auth == nil || sc.Auth.Type != "oauth2" {
			continue
		}
		injectToken(ctx, configDir, sc)
	}
}

func injectToken(ctx context.Context, configDir string, sc *config.ServerConfig) {
	t, err := auth.Load(configDir, sc.Name)
	if auth.IsNotFound(err) {
		return
	}
	if err != nil {
		fmt.Fprintf(os.Stderr, "mini: load token for %s: %v\n", sc.Name, err)
		return
	}
	t, err = ensureValidToken(ctx, configDir, sc, t)
	if err != nil {
		return
	}
	setAuthHeader(sc, t.AccessToken)
}

func ensureValidToken(ctx context.Context, configDir string, sc *config.ServerConfig, t *oauth2.Token) (*oauth2.Token, error) {
	if t.Valid() {
		return t, nil
	}
	if t.RefreshToken == "" {
		fmt.Fprintf(os.Stderr, "mini: token for %s is expired — run: mini auth %s\n", sc.Name, sc.Name)
		return nil, fmt.Errorf("expired")
	}
	refreshed, err := auth.Refresh(ctx, sc.Auth, t)
	if err != nil {
		fmt.Fprintf(os.Stderr, "mini: refresh token for %s failed — run: mini auth %s\n", sc.Name, sc.Name)
		return nil, err
	}
	if saveErr := auth.Save(configDir, sc.Name, refreshed); saveErr != nil {
		fmt.Fprintf(os.Stderr, "mini: save refreshed token for %s: %v\n", sc.Name, saveErr)
	}
	return refreshed, nil
}

func setAuthHeader(sc *config.ServerConfig, accessToken string) {
	h := sc.Auth.Header
	if h == "" {
		h = "Authorization"
	}
	if sc.Headers == nil {
		sc.Headers = make(map[string]string)
	}
	sc.Headers[h] = "Bearer " + accessToken
}

func findServer(servers []config.ServerConfig, name string) *config.ServerConfig {
	for i := range servers {
		if servers[i].Name == name {
			return &servers[i]
		}
	}
	return nil
}

func openBrowser(url string) error {
	var cmd string
	switch runtime.GOOS {
	case "darwin":
		cmd = "open"
	case "linux":
		cmd = "xdg-open"
	case "windows":
		cmd = "start"
	default:
		return fmt.Errorf("unsupported platform")
	}
	return exec.Command(cmd, url).Start()
}
