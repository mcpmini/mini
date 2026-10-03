package main

import (
	"context"
	"fmt"
	"io"
	"os"
	"time"

	"golang.org/x/oauth2"

	"github.com/spf13/cobra"

	"github.com/mcpmini/mini/internal/auth"
	"github.com/mcpmini/mini/internal/clock"
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
	if _, err := logIn(logInParams{configDir: configDir, cfg: cfg, sc: sc, out: os.Stdout}); err != nil {
		fatalf("%v", err)
	}
}

func loadOAuthServerAndConfig(configDir, serverName string) (*config.Config, *config.ServerConfig, error) {
	cfg, sc, err := loadOneServer(configDir, serverName, os.Stderr)
	if err != nil {
		return nil, nil, err
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

type logInParams struct {
	configDir string
	cfg       *config.Config
	sc        *config.ServerConfig
	out       io.Writer
}

func logIn(p logInParams) (*oauth2.Token, error) {
	token, err := doPKCEFlow(pkceFlowParamsFor(p.configDir, p.cfg, p.sc))
	if err != nil {
		return nil, err
	}
	printAuthResult(p.out, p.sc.Name, token.Expiry)
	return token, nil
}

func pkceFlowParamsFor(configDir string, cfg *config.Config, sc *config.ServerConfig) pkceFlowParams {
	return pkceFlowParams{configDir: configDir, serverName: sc.Name, opener: authOpener(cfg, *sc), sc: sc}
}

func doPKCEFlow(p pkceFlowParams) (*oauth2.Token, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	fmt.Printf("Authorizing %s...\n", p.serverName)
	resolveParams := auth.ResolveEndpointsParams{ConfigDir: p.configDir, ServerName: p.serverName, Clock: clock.System()}
	if err := auth.ResolveEndpoints(ctx, p.sc, resolveParams); err != nil {
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

func authOpener(cfg *config.Config, sc config.ServerConfig) func(string) error {
	cmd, open := cfg.BrowserCommandFor(sc)
	if !open {
		return func(string) error { return nil }
	}
	if cmd != "" {
		return func(url string) error { return auth.OpenBrowser(cmd, url) }
	}
	return openBrowser
}

func printAuthResult(out io.Writer, name string, expiry time.Time) {
	if expiry.IsZero() {
		fmt.Fprintf(out, "authorized %s (no expiry)\n", name)
	} else {
		fmt.Fprintf(out, "authorized %s (expires %s)\n", name, expiry.Format(time.RFC3339))
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
	if sc.UnsetEnv != nil {
		return // dialing reports it; a refresh now would send a ${VAR} as the client secret
	}
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
	if err := auth.ApplyResourceURL(sc); err != nil {
		fmt.Fprintf(os.Stderr, "mini: resolve resource URL for %s: %v\n", sc.Name, err)
		return nil, err
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

var openBrowser = func(url string) error { return auth.OpenBrowser("", url) }
