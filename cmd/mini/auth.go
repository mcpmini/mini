package main

import (
	"context"
	"fmt"
	"io"
	"os"
	"time"

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

func logIn(p logInParams) (*auth.Login, error) {
	token, err := doPKCEFlow(pkceFlowParamsFor(p.configDir, p.cfg, p.sc))
	if err != nil {
		return nil, err
	}
	printAuthResult(p.out, p.sc.Name, token.Token.Expiry)
	return token, nil
}

func pkceFlowParamsFor(configDir string, cfg *config.Config, sc *config.ServerConfig) pkceFlowParams {
	return pkceFlowParams{configDir: configDir, serverName: sc.Name, opener: authOpener(cfg, *sc), sc: sc}
}

const loginTimeout = 5 * time.Minute

func doPKCEFlow(p pkceFlowParams) (*auth.Login, error) {
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
	wait func() (*auth.Login, error)
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
	wait := func() (*auth.Login, error) {
		defer cancel()
		return finishBrowserLogin(ctx, login, p)
	}
	return browserLogin{url: login.AuthURL(), wait: wait}, nil
}

func finishBrowserLogin(ctx context.Context, login *auth.BrowserLogin, p pkceFlowParams) (*auth.Login, error) {
	defer login.Close() //nolint:errcheck // Close always returns nil
	token, err := login.Wait(ctx)
	if err != nil {
		return nil, fmt.Errorf("auth flow: %w", err)
	}
	if err := tokenLock(p.configDir, p.serverName).Save(ctx, token); err != nil {
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
	login, err := auth.LoadLogin(configDir, sc.Name)
	if auth.IsNotFound(err) {
		return
	}
	if err != nil {
		printNotice(os.Stderr, "mini: load token for %s: %v\n", sc.Name, err)
		return
	}
	if eligible, reason := auth.LoginEligible(login, *sc); !eligible {
		printNotice(
			os.Stderr,
			"mini: login for %s does not match current settings (%s) — run: mini auth %s\n",
			sc.Name,
			reason,
			sc.Name,
		)
		return
	}
	login, err = ensureValidToken(ctx, configDir, sc, login)
	if err != nil {
		return
	}
	auth.ApplyBearerToken(sc, login.Token.AccessToken)
}

func ensureValidToken(
	ctx context.Context,
	configDir string,
	sc *config.ServerConfig,
	login *auth.Login,
) (*auth.Login, error) {
	if login.Token.Valid() {
		return login, nil
	}
	if login.Token.RefreshToken == "" {
		printNotice(os.Stderr, "mini: token for %s is expired — run: mini auth %s\n", sc.Name, sc.Name)
		return nil, fmt.Errorf("expired")
	}
	return refreshAndSaveToken(ctx, configDir, sc, login)
}

func refreshAndSaveToken(
	ctx context.Context,
	configDir string,
	sc *config.ServerConfig,
	login *auth.Login,
) (*auth.Login, error) {
	var refreshed *auth.Login
	err := tokenLock(configDir, sc.Name).Do(ctx, func(f auth.LoginFile) error {
		var refreshErr error
		refreshed, refreshErr = refreshUnlessRefreshed(ctx, f, cliRefresh{sc: sc, loaded: login})
		return refreshErr
	})
	if err != nil {
		printNotice(os.Stderr, "mini: refresh token for %s failed — run: mini auth %s\n", sc.Name, sc.Name)
		return nil, err
	}
	return refreshed, nil
}

type cliRefresh struct {
	sc     *config.ServerConfig
	loaded *auth.Login
}

func refreshUnlessRefreshed(ctx context.Context, f auth.LoginFile, r cliRefresh) (*auth.Login, error) {
	login := r.loaded
	saved, err := f.Load()
	if err != nil {
		return nil, err
	}
	if eligible, reason := auth.LoginEligible(saved, *r.sc); !eligible {
		return nil, fmt.Errorf("login does not match current settings: %s", reason)
	}
	if saved.Token.Valid() {
		return saved, nil
	}
	if saved.Token.RefreshToken != "" {
		login = saved
	}
	ac := login.RefreshConfig(r.sc.Auth)
	refreshed, err := auth.Refresh(ctx, ac, login.Token)
	if err != nil {
		return nil, err
	}
	login.Token = refreshed
	if saveErr := f.Save(login); saveErr != nil {
		printNotice(os.Stderr, "mini: save refreshed token for %s: %v\n", r.sc.Name, saveErr)
	}
	return login, nil
}

func tokenLock(configDir, serverName string) auth.TokenLock {
	return auth.TokenLock{ConfigDir: configDir, ServerName: serverName, Clock: clock.System()}
}

var openBrowser = func(url string) error { return auth.OpenBrowser("", url) }
