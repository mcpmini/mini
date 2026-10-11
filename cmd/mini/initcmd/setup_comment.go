package initcmd

import (
	"bytes"
	"fmt"
	"strings"

	"github.com/mcpmini/mini/internal/auth"
	"github.com/mcpmini/mini/internal/catalog"
	"github.com/mcpmini/mini/internal/config"
)

// Init can't get a token or OAuth app for the user, so the config they need goes in the file, commented out.
func (p Plan) setupComment(name string) string {
	return planAdds(p.Import.picked(), p.Add, p.written).setupComments[name]
}

// The placeholders the steps leave for the user; a value still holding one is no credential yet.
const (
	tokenPlaceholder        = "<your token>"
	clientIDPlaceholder     = "<your app's client ID>"
	clientSecretPlaceholder = "<your app's client secret>"
)

// A user who deleted part of the block gets the summary's full step instead; one who uncommented it
// with a placeholder left in still has the steps at hand.
func setupCommentIn(data []byte, entry catalog.Entry) bool {
	comment := credentialComment(entry)
	if comment == "" {
		return false
	}
	if placeholderIn(data) {
		return true
	}
	for line := range strings.SplitSeq(config.CommentLines(comment), "\n") {
		if !bytes.Contains(data, []byte(line)) {
			return false
		}
	}
	return true
}

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
				"    client_secret: " + clientSecretPlaceholder,
			}
	}
	return "", nil
}

func placeholderIn(data []byte) bool {
	for _, placeholder := range []string{tokenPlaceholder, clientIDPlaceholder, clientSecretPlaceholder} {
		for line := range strings.SplitSeq(string(data), "\n") {
			if strings.Contains(line, placeholder) && !strings.HasPrefix(strings.TrimSpace(line), "#") {
				return true
			}
		}
	}
	return false
}
