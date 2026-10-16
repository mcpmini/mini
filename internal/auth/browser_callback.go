package auth

import (
	"errors"
	"fmt"
	"html"
	"net/http"
	"net/url"
	"strings"
	"unicode"
	"unicode/utf8"
)

type loginCallbackResult struct {
	code string
	err  error
}

// ExpectedIssuer is the authorization server a login was sent to, from its validated metadata. It is
// zero when the login used no metadata, leaving nothing to check the response against.
type ExpectedIssuer struct {
	Issuer string
	// AlwaysSent is the server's promise to put iss on every response, so a missing one is forged.
	AlwaysSent bool
}

// check guards against a mix-up attack: a response from another authorization server must not
// have its code exchanged at this server's token endpoint.
// https://github.com/modelcontextprotocol/modelcontextprotocol/blob/8e12bf3c/docs/specification/2026-07-28/basic/authorization/index.mdx?plain=1#L192-L211
func (e ExpectedIssuer) check(q url.Values) error {
	if !q.Has("iss") {
		if e.AlwaysSent {
			return errors.New("oauth authorization response is missing iss, which its server always sends")
		}
		return nil
	}
	if e.Issuer != "" && q.Get("iss") != e.Issuer {
		return fmt.Errorf(
			"oauth authorization response came from issuer %q, not %q",
			printableLoginText(q.Get("iss")),
			e.Issuer,
		)
	}
	return nil
}

type callbackExpectation struct {
	state  string
	issuer ExpectedIssuer
}

func callbackHandler(want callbackExpectation, results chan<- loginCallbackResult) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		if q.Get("state") != want.state {
			http.Error(w, "state mismatch", http.StatusBadRequest)
			return
		}
		completeLoginCallback(w, q, want.issuer, results)
	})
}

func completeLoginCallback(
	w http.ResponseWriter,
	q url.Values,
	issuer ExpectedIssuer,
	results chan<- loginCallbackResult,
) {
	result, complete := callbackResult(q, issuer)
	if !complete {
		http.Error(w, "missing code", http.StatusBadRequest)
		return
	}
	writeLoginResponse(w, loginResponseMessage(result.err))
	select {
	case results <- result:
	default:
	}
}

func callbackResult(q url.Values, issuer ExpectedIssuer) (result loginCallbackResult, complete bool) {
	if err := issuer.check(q); err != nil {
		return loginCallbackResult{err: err}, true
	}
	if q.Has("error") {
		return loginCallbackResult{err: providerLoginError(q)}, true
	}
	code := q.Get("code")
	return loginCallbackResult{code: code}, code != ""
}

func loginResponseMessage(err error) string {
	if err != nil {
		return err.Error() + ". You can close this tab."
	}
	return "Authorized. You can close this tab."
}

func providerLoginError(q url.Values) error {
	code := printableLoginText(q.Get("error"))
	description := printableLoginText(q.Get("error_description"))
	if description == "" {
		return fmt.Errorf("oauth authorization failed: %s", code)
	}
	return fmt.Errorf("oauth authorization failed: %s: %s", code, description)
}

func printableLoginText(value string) string {
	var out strings.Builder
	for _, r := range value {
		if !unicode.IsPrint(r) {
			continue
		}
		if out.Len()+utf8.RuneLen(r) > 256 {
			break
		}
		out.WriteRune(r)
	}
	return out.String()
}

func writeLoginResponse(w http.ResponseWriter, message string) {
	w.Header().Set("Content-Type", "text/html")
	//nolint:errcheck // callback handling owns the OAuth result; this page only informs the browser.
	fmt.Fprintf(w, "<html><body><p>%s</p></body></html>\n", html.EscapeString(message))
	if f, ok := w.(http.Flusher); ok {
		f.Flush()
	}
}
