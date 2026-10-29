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
	"github.com/mcpmini/mini/internal/transport"
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
	cfg, sc, err := loadOAuthServerAndConfig(configDir, serverName, os.Stderr)
	if err != nil {
		fatalf("%v", err)
	}
	if _, err := logIn(logInParams{configDir: configDir, cfg: cfg, sc: sc, out: os.Stdout}); err != nil {
		fatalf("%v", err)
	}
}

func loadOAuthServerAndConfig(
	configDir, serverName string,
	warnings io.Writer,
) (*config.Config, *config.ServerConfig, error) {
	cfg, sc, err := loadOneServer(configDir, serverName, warnings)
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

const loginTimeout = 5 * time.Minute

func doPKCEFlow(p pkceFlowParams) (*oauth2.Token, error) {
	printNotice(os.Stdout, "Authorizing %s...\n", p.serverName)
	login, err := startBrowserLogin(context.Background(), p)
	if err != nil {
		return nil, err
	}
	printNotice(os.Stdout, "Open this URL in your browser:\n%s\n\n", login.url)
	return login.wait()
}

// browserLogin is a login waiting on the browser; wait ends it and saves the token.
type browserLogin struct {
	url  string
	wait func() (*oauth2.Token, error)
}

func startBrowserLogin(ctx context.Context, p pkceFlowParams) (browserLogin, error) {
	ctx, cancel := context.WithTimeout(ctx, loginTimeout)
	params := auth.BeginLoginParams{ConfigDir: p.configDir, ServerName: p.serverName, Clock: clock.System()}
	login, err := auth.BeginLogin(ctx, *p.sc, params)
	if err != nil {
		cancel()
		return browserLogin{}, err
	}
	p.opener(login.AuthURL()) //nolint:errcheck // the caller shows the URL, so a failed open only costs a click
	wait := func() (*oauth2.Token, error) {
		defer cancel()
		return finishBrowserLogin(ctx, login, p)
	}
	return browserLogin{url: login.AuthURL(), wait: wait}, nil
}

func finishBrowserLogin(ctx context.Context, login *auth.BrowserLogin, p pkceFlowParams) (*oauth2.Token, error) {
	defer login.Close() //nolint:errcheck // Close always returns nil
	saved, err := login.Wait(ctx)
	if err != nil {
		return nil, fmt.Errorf("auth flow: %w", err)
	}
	if err := auth.SaveLogin(p.configDir, p.serverName, saved); err != nil {
		return nil, fmt.Errorf("save login: %w", err)
	}
	return saved.Token, nil
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
		printNotice(out, "authorized %s (no expiry)\n", name)
	} else {
		printNotice(out, "authorized %s (expires %s)\n", name, expiry.Format(time.RFC3339))
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
	login, err := auth.LoadLoginFor(configDir, *sc)
	if auth.IsNotFound(err) {
		return
	}
	if err != nil {
		printNotice(os.Stderr, "mini: %v\n", transport.ReauthorizationError(sc.Name, err))
		return
	}
	token, err := ensureValidToken(loginRefreshParams{
		ctx: ctx, configDir: configDir, server: sc, login: login,
	})
	if err != nil {
		return
	}
	auth.ApplyBearerToken(sc, token.AccessToken)
}

type loginRefreshParams struct {
	ctx       context.Context
	configDir string
	server    *config.ServerConfig
	login     *auth.Login
}

func ensureValidToken(p loginRefreshParams) (*oauth2.Token, error) {
	if p.login.Token.Valid() {
		return p.login.Token, nil
	}
	if p.login.Token.RefreshToken == "" {
		printNotice(os.Stderr, "mini: token for %s is expired — run: mini auth %s\n", p.server.Name, p.server.Name)
		return nil, fmt.Errorf("expired")
	}
	return refreshAndSaveLogin(p)
}

func refreshAndSaveLogin(p loginRefreshParams) (*oauth2.Token, error) {
	refreshed, err := auth.Refresh(p.ctx, auth.RefreshParams{Server: *p.server, Login: p.login})
	if err != nil {
		printNotice(os.Stderr, "mini: refresh token for %s failed — run: mini auth %s\n", p.server.Name, p.server.Name)
		return nil, err
	}
	p.login.Token = refreshed
	if saveErr := auth.SaveLogin(p.configDir, p.server.Name, p.login); saveErr != nil {
		printNotice(os.Stderr, "mini: save refreshed login for %s: %v\n", p.server.Name, saveErr)
	}
	return refreshed, nil
}

var openBrowser = func(url string) error { return auth.OpenBrowser("", url) }
