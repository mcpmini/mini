package main

import (
	"context"
	"fmt"
	"os"
	"time"

	"golang.org/x/oauth2"

	"github.com/spf13/cobra"

	"github.com/mcpmini/mini/internal/auth"
	"github.com/mcpmini/mini/internal/config"
)

func newAuthCmd(opts *rootOptions) *cobra.Command {
	return &cobra.Command{
		Use:   "auth NAME",
		Short: "Authorize a server via OAuth2 (PKCE flow)",
		Args:  usageArgs(cobra.ExactArgs(1)),
		RunE: func(cmd *cobra.Command, args []string) error {
			runAuth(opts.configDir, args[0])
			return nil
		},
	}
}

func runAuth(configDir, serverName string) {
	cfg, sc, err := loadOAuthServerAndConfig(configDir, serverName)
	if err != nil {
		fatalf("%v", err)
	}
	runPKCEFlow(pkceFlowParams{
		configDir:  configDir,
		serverName: serverName,
		opener:     authOpener(sc.Auth.BrowserCmd, cfg.BrowserCommand, cfg.DisableAuthBrowserOpen),
		sc:         sc,
	})
}

func loadOAuthServerAndConfig(configDir, serverName string) (*config.Config, *config.ServerConfig, error) {
	cfg, servers, err := config.Load(configDir)
	if err != nil {
		return nil, nil, fmt.Errorf("load config: %w", err)
	}
	sc := config.FindServer(servers, serverName)
	if sc == nil {
		return nil, nil, fmt.Errorf("server not found: %s", serverName)
	}
	if err := auth.ValidateOAuthServer(serverName, *sc); err != nil {
		return nil, nil, err
	}
	return cfg, sc, nil
}

type pkceFlowParams struct {
	configDir  string
	serverName string
	opener     func(string) error
	sc         *config.ServerConfig
}

func runPKCEFlow(p pkceFlowParams) {
	token, err := doPKCEFlow(p)
	if err != nil {
		fatalf("%v", err)
	}
	printAuthResult(p.serverName, token.Expiry)
}

func doPKCEFlow(p pkceFlowParams) (*oauth2.Token, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	fmt.Printf("Authorizing %s...\n", p.serverName)
	if err := auth.ResolveEndpoints(ctx, p.configDir, p.serverName, p.sc); err != nil {
		return nil, fmt.Errorf("resolve oauth config: %w", err)
	}
	token, err := auth.PKCEFlow(ctx, p.sc.Auth, p.opener)
	if err != nil {
		return nil, fmt.Errorf("auth flow: %w", err)
	}
	if err := auth.Save(p.configDir, p.serverName, token); err != nil {
		return nil, fmt.Errorf("save token: %w", err)
	}
	return token, nil
}

func authOpener(perServerCmd, globalCmd string, disabled bool) func(string) error {
	if disabled {
		return func(string) error { return nil }
	}
	cmd := resolveOpenerCmd(perServerCmd, globalCmd)
	if cmd != "" {
		return func(url string) error { return auth.OpenBrowser(cmd, url) }
	}
	return openBrowser
}

func resolveOpenerCmd(perServerCmd, globalCmd string) string {
	if perServerCmd != "" {
		return perServerCmd
	}
	return globalCmd
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
		if sc.Auth == nil || sc.Auth.Type != config.AuthTypeOAuth2 {
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
	auth.ApplyBearerToken(sc, t.AccessToken)
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

var openBrowser = func(url string) error { return auth.OpenBrowser("", url) }
