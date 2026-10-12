package initcmd

import (
	"fmt"
	"strings"

	"github.com/mcpmini/mini/internal/auth"
	"github.com/mcpmini/mini/internal/catalog"
	"github.com/mcpmini/mini/internal/config"
)

// The placeholders the steps leave for the user; a value still holding one is no credential yet.
const (
	tokenPlaceholder        = "<your token>"
	clientIDPlaceholder     = "<your app's client ID>"
	clientSecretPlaceholder = "<your app's client secret>"
)

// Init can't get a token or OAuth app for the user, so the config they need goes in the file,
// commented out.
func credentialComment(entry catalog.Entry) string {
	intro, block := credentialSteps(entry.Auth, entry.SetupURL)
	if intro == "" {
		return ""
	}
	return intro + ", then uncomment:\n" + strings.Join(block, "\n")
}

// credentialSteps is where to get a server's credentials and the config that holds them, shared by
// the file's comment and the summary so the two never disagree.
func credentialSteps(authKind, setupURL string) (intro string, block []string) {
	switch authKind {
	case catalog.AuthToken:
		return "Create a token at " + setupURL, []string{
			"headers:",
			"    Authorization: Bearer " + tokenPlaceholder,
		}
	case catalog.AuthOAuth2App:
		return fmt.Sprintf("Register an OAuth app at %s with redirect URI %s", setupURL, auth.ResolvedCallbackURI(nil)),
			[]string{
				"auth:",
				"    type: oauth2",
				"    client_id: " + clientIDPlaceholder,
				"    client_secret: " + clientSecretPlaceholder + "  # leave this line out if your app has none",
			}
	}
	return "", nil
}

// holdsPlaceholder reports whether the user uncommented the steps without finishing them.
func holdsPlaceholder(sc config.ServerConfig) bool {
	for _, value := range sc.Headers {
		if strings.Contains(value, tokenPlaceholder) {
			return true
		}
	}
	return sc.Auth != nil &&
		(sc.Auth.ClientID == clientIDPlaceholder || sc.Auth.ClientSecret == clientSecretPlaceholder)
}
