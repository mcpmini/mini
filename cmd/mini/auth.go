package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"runtime"
	"strings"
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
	runPKCEFlow(configDir, serverName, sc)
}

func runPKCEFlow(configDir, serverName string, sc *config.ServerConfig) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	fmt.Printf("Authorizing %s...\n", serverName)
	if err := resolveOAuthEndpoints(ctx, configDir, serverName, sc); err != nil {
		fatalf("resolve oauth config: %v", err)
	}
	opener := openBrowser
	if sc.Auth.BrowserCmd != "" {
		opener = openBrowserCmd(sc.Auth.BrowserCmd)
	}
	token, err := auth.PKCEFlow(ctx, sc.Auth, opener)
	if err != nil {
		fatalf("auth flow: %v", err)
	}
	if err := auth.Save(configDir, serverName, token); err != nil {
		fatalf("save token: %v", err)
	}
	printAuthResult(serverName, token.Expiry)
}

func resolveOAuthEndpoints(ctx context.Context, configDir, serverName string, sc *config.ServerConfig) error {
	a := sc.Auth
	if a.AuthURL != "" && a.TokenURL != "" && a.ClientID != "" {
		return nil
	}
	regURL, err := discoverMissingEndpoints(ctx, sc.URL, a)
	if err != nil {
		return err
	}
	if a.ClientID == "" {
		return resolveClientID(ctx, configDir, serverName, a, regURL)
	}
	return nil
}

func discoverMissingEndpoints(ctx context.Context, url string, a *config.AuthConfig) (string, error) {
	if a.AuthURL != "" && a.TokenURL != "" {
		return "", nil
	}
	meta, err := auth.Discover(ctx, url)
	if err != nil {
		return "", fmt.Errorf("discover oauth endpoints: %w", err)
	}
	if a.AuthURL == "" {
		a.AuthURL = meta.AuthURL
	}
	if a.TokenURL == "" {
		a.TokenURL = meta.TokenURL
	}
	return meta.RegistrationURL, nil
}

func resolveClientID(ctx context.Context, configDir, serverName string, a *config.AuthConfig, regURL string) error {
	reg, err := auth.LoadRegistration(configDir, serverName)
	if err == nil {
		a.ClientID = reg.ClientID
		return nil
	}
	if !auth.IsNotFound(err) {
		return err
	}
	if regURL == "" {
		return fmt.Errorf("no client_id configured and server provides no registration endpoint")
	}
	clientID, err := auth.Register(ctx, regURL)
	if err != nil {
		return err
	}
	a.ClientID = clientID
	return auth.SaveRegistration(configDir, serverName, &auth.Registration{ClientID: clientID})
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

func openBrowserCmd(browserCmd string) func(string) error {
	return func(url string) error {
		return exec.Command("sh", "-c", browserCmd+" "+shellQuote(url)).Start()
	}
}

func shellQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", "'\\''") + "'"
}
