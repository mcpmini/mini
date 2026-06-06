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
	"github.com/mcpmini/mini/internal/transport"
)

func runAuth(configDir string, args []string) {
	fs := flag.NewFlagSet("auth", flag.ExitOnError)
	fs.Parse(args)
	serverName := requireAuthServer(fs)
	cfg, sc, err := loadOAuthServerAndConfig(configDir, serverName)
	if err != nil {
		fatalf("%v", err)
	}
	runPKCEFlow(configDir, serverName, cfg.BrowserCommand, sc)
}

func requireAuthServer(fs *flag.FlagSet) string {
	if fs.NArg() == 0 {
		fatalf("usage: mini auth <server-name>")
	}
	return fs.Arg(0)
}

func loadOAuthServerAndConfig(configDir, serverName string) (*config.Config, *config.ServerConfig, error) {
	cfg, servers, err := config.Load(configDir)
	if err != nil {
		return nil, nil, fmt.Errorf("load config: %w", err)
	}
	sc := findServer(servers, serverName)
	if sc == nil {
		return nil, nil, fmt.Errorf("server not found: %s", serverName)
	}
	if sc.Auth == nil || sc.Auth.Type != "oauth2" {
		return nil, nil, fmt.Errorf("server %q does not have oauth2 auth configured", serverName)
	}
	return cfg, sc, nil
}

func runPKCEFlow(configDir, serverName, globalBrowserCmd string, sc *config.ServerConfig) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	fmt.Printf("Authorizing %s...\n", serverName)
	if err := resolveOAuthEndpoints(ctx, configDir, serverName, sc); err != nil {
		fatalf("resolve oauth config: %v", err)
	}
	token, err := auth.PKCEFlow(ctx, sc.Auth, authOpener(sc.Auth.BrowserCmd, globalBrowserCmd))
	if err != nil {
		fatalf("auth flow: %v", err)
	}
	if err := auth.Save(configDir, serverName, token); err != nil {
		fatalf("save token: %v", err)
	}
	printAuthResult(serverName, token.Expiry)
}

func authOpener(perServerCmd, globalCmd string) func(string) error {
	cmd := resolveOpenerCmd(perServerCmd, globalCmd)
	if cmd != "" {
		return openBrowserCmd(cmd)
	}
	return openBrowser
}

func resolveOpenerCmd(perServerCmd, globalCmd string) string {
	if perServerCmd != "" {
		return perServerCmd
	}
	return globalCmd
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
	if err := applyDiscoveredEndpoints(a, meta); err != nil {
		return "", err
	}
	return meta.RegistrationURL, nil
}

func applyDiscoveredEndpoints(a *config.AuthConfig, meta *auth.ServerMeta) error {
	if a.AuthURL == "" {
		if err := validateOAuthEndpoint(meta.AuthURL, "authorization_endpoint"); err != nil {
			return err
		}
		a.AuthURL = meta.AuthURL
	}
	if a.TokenURL == "" {
		if err := validateOAuthEndpoint(meta.TokenURL, "token_endpoint"); err != nil {
			return err
		}
		a.TokenURL = meta.TokenURL
	}
	return validateOAuthEndpoint(meta.RegistrationURL, "registration_endpoint")
}

func validateOAuthEndpoint(endpoint, name string) error {
	if endpoint == "" {
		return nil
	}
	if err := transport.ValidateURL(endpoint); err != nil {
		return fmt.Errorf("oauth discovery: %s points to a disallowed host: %w", name, err)
	}
	return nil
}

func resolveClientID(ctx context.Context, configDir, serverName string, a *config.AuthConfig, regURL string) error {
	found, err := applyExistingRegistration(configDir, serverName, a)
	if err != nil || found {
		return err
	}
	if regURL == "" {
		return fmt.Errorf("no client_id configured and server provides no registration endpoint")
	}
	clientID, err := registerClient(ctx, regURL)
	if err != nil {
		return err
	}
	return storeNewClientID(configDir, serverName, clientID, a)
}

func applyExistingRegistration(configDir, serverName string, a *config.AuthConfig) (bool, error) {
	reg, err := auth.LoadRegistration(configDir, serverName)
	if err == nil {
		a.ClientID = reg.ClientID
		return true, nil
	}
	if !auth.IsNotFound(err) {
		return false, err
	}
	return false, nil
}

func storeNewClientID(configDir, serverName, clientID string, a *config.AuthConfig) error {
	a.ClientID = clientID
	return saveClientRegistration(configDir, serverName, clientID)
}

func registerClient(ctx context.Context, regURL string) (string, error) {
	return auth.Register(ctx, regURL)
}

func saveClientRegistration(configDir, serverName, clientID string) error {
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
	return refreshAndSaveToken(ctx, configDir, sc, t)
}

func refreshAndSaveToken(ctx context.Context, configDir string, sc *config.ServerConfig, t *oauth2.Token) (*oauth2.Token, error) {
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

var openBrowser = platformBrowserOpener

func platformBrowserOpener(url string) error {
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
	parts := strings.Fields(browserCmd)
	return func(url string) error {
		if len(parts) == 0 {
			return fmt.Errorf("empty browser_cmd")
		}
		return exec.Command(parts[0], append(parts[1:], url)...).Start()
	}
}
