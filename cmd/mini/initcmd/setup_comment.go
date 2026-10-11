package initcmd

import (
	"bytes"
	"fmt"
	"strings"

	"github.com/mcpmini/mini/internal/auth"
	"github.com/mcpmini/mini/internal/catalog"
)

// setupComment is the commented-out config a written catalog server needs from the user, so
// finishing it is an edit in its file: init can't get the token or app for them.
func (p Plan) setupComment(name string) string {
	return planAdds(p.Import.picked(), p.Add, p.written).setupComments[name]
}

// setupCommentIn reports whether a server file holds the setup comment init writes for entry.
func setupCommentIn(data []byte, entry catalog.Entry) bool {
	first, _, _ := strings.Cut(credentialComment(entry), "\n")
	return first != "" && bytes.Contains(data, []byte("# "+first))
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
