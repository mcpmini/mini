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

type callbackHandlerParams struct {
	state                string
	issuer               string
	responseIssSupported bool
	results              chan<- loginCallbackResult
}

func callbackHandler(p callbackHandlerParams) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		if q.Get("state") != p.state {
			http.Error(w, "state mismatch", http.StatusBadRequest)
			return
		}
		if callbackIssuerRejected(q, p) {
			result := loginCallbackResult{err: errors.New("oauth authorization response issuer rejected")}
			writeLoginResponse(w, "Authorization response rejected. Start the login again, then close this tab.")
			select {
			case p.results <- result:
			default:
			}
			return
		}
		completeLoginCallback(w, q, p.results)
	})
}

func callbackIssuerRejected(q url.Values, p callbackHandlerParams) bool {
	if p.issuer == "" {
		return false
	}
	if q.Has("iss") {
		return q.Get("iss") != p.issuer
	}
	return p.responseIssSupported
}

func completeLoginCallback(w http.ResponseWriter, q url.Values, results chan<- loginCallbackResult) {
	result := loginCallbackResult{code: q.Get("code")}
	message := "Authorized. You can close this tab."
	if q.Has("error") {
		result.err = providerLoginError(q)
		message = result.err.Error() + ". You can close this tab."
	} else if result.code == "" {
		http.Error(w, "missing code", http.StatusBadRequest)
		return
	}
	writeLoginResponse(w, message)
	select {
	case results <- result:
	default:
	}
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
