package initcmd

import (
	"fmt"
	"slices"

	"github.com/mcpmini/mini/internal/auth"
	"github.com/mcpmini/mini/internal/catalog"
)

// setupComment is the commented-out config a catalog server needs from the user, written into its
// file so finishing it is an edit there: init can't get the token or app for them.
func (p Plan) setupComment(name string) string {
	adds := planAdds(p.Import.picked(), p.Add, p.written)
	if slices.Contains(adds.alreadyConfigured, name) || slices.Contains(adds.coveredByImport, name) {
		return ""
	}
	i := slices.IndexFunc(p.Add, func(e catalog.Entry) bool { return e.Name == name })
	if i < 0 {
		return ""
	}
	return credentialComment(p.Add[i])
}

func credentialComment(entry catalog.Entry) string {
	switch entry.Auth {
	case catalog.AuthToken:
		return fmt.Sprintf("Create a token at %s, then uncomment:\n"+
			"headers:\n"+
			"    Authorization: Bearer <your token>", entry.SetupURL)
	case catalog.AuthOAuth2App:
		return fmt.Sprintf("Register an OAuth app at %s with redirect URI %s, then uncomment:\n"+
			"auth:\n"+
			"    type: oauth2\n"+
			"    client_id: <your app's client ID>\n"+
			"    client_secret: <your app's client secret>", entry.SetupURL, auth.ResolvedCallbackURI(nil))
	}
	return ""
}
