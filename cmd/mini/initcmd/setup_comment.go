package initcmd

import (
	"bytes"
	"fmt"
	"strings"

	"github.com/mcpmini/mini/internal/auth"
	"github.com/mcpmini/mini/internal/catalog"
)

// Init can't get a token or OAuth app for the user, so the config they need goes in the file, commented out.
func (p Plan) setupComment(name string) string {
	return planAdds(p.Import.picked(), p.Add, p.written).setupComments[name]
}

// A user who deleted part of the block gets the summary's full step instead.
func setupCommentIn(data []byte, entry catalog.Entry) bool {
	comment := credentialComment(entry)
	if comment == "" {
		return false
	}
	for line := range strings.SplitSeq(comment, "\n") {
		if !bytes.Contains(data, []byte(strings.TrimRight("# "+line, " "))) {
			return false
		}
	}
	return true
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
