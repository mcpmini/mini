package auth

import (
	"errors"
	"fmt"
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

// authorizationResponse is the authorization server's redirect to mini's callback: RFC 6749
// section 4.1.2 and its error form, plus RFC 9207's iss.
type authorizationResponse struct {
	state string
	code  string
	// iss is nil when the response carries none, which the spec treats differently from an empty one.
	iss         *string
	providerErr *providerError
}

// providerError is the error form of the response, which the authorization server sends instead of a code.
type providerError struct {
	code        string
	description string
}

func (e *providerError) Error() string {
	code := printableLoginText(e.code)
	description := printableLoginText(e.description)
	if description == "" {
		return fmt.Sprintf("oauth authorization failed: %s", code)
	}
	return fmt.Sprintf("oauth authorization failed: %s: %s", code, description)
}

func parseAuthorizationResponse(q url.Values) authorizationResponse {
	resp := authorizationResponse{state: q.Get("state"), code: q.Get("code")}
	if q.Has("iss") {
		iss := q.Get("iss")
		resp.iss = &iss
	}
	if q.Has("error") {
		resp.providerErr = &providerError{code: q.Get("error"), description: q.Get("error_description")}
	}
	return resp
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
func (e ExpectedIssuer) check(resp authorizationResponse) error {
	if resp.iss == nil {
		if e.AlwaysSent {
			return errors.New("oauth authorization response is missing iss, which its server always sends")
		}
		return nil
	}
	if e.Issuer != "" && *resp.iss != e.Issuer {
		return fmt.Errorf(
			"oauth authorization response came from issuer %q, not %q",
			printableLoginText(*resp.iss),
			e.Issuer,
		)
	}
	return nil
}

type callbackExpectation struct {
	state  string
	issuer ExpectedIssuer
}

func callbackHandler(
	want callbackExpectation,
	target loginTarget,
	results chan<- loginCallbackResult,
) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		resp := parseAuthorizationResponse(r.URL.Query())
		if resp.state != want.state {
			writeLoginPage(w, http.StatusBadRequest, failedLoginPage(loginTarget{}, "state mismatch"))
			return
		}
		completeLoginCallback(loginCallbackParams{
			writer: w, response: resp, issuer: want.issuer, target: target, results: results,
		})
	})
}

type loginCallbackParams struct {
	writer   http.ResponseWriter
	response authorizationResponse
	issuer   ExpectedIssuer
	target   loginTarget
	results  chan<- loginCallbackResult
}

func completeLoginCallback(p loginCallbackParams) {
	result, complete := callbackResult(p.response, p.issuer)
	if !complete {
		writeLoginPage(p.writer, http.StatusBadRequest, failedLoginPage(p.target, "missing code"))
		return
	}
	writeLoginPage(p.writer, http.StatusOK, loginResultPage(p.target, result.err))
	select {
	case p.results <- result:
	default:
	}
}

func callbackResult(resp authorizationResponse, issuer ExpectedIssuer) (result loginCallbackResult, complete bool) {
	if err := issuer.check(resp); err != nil {
		return loginCallbackResult{err: err}, true
	}
	if resp.providerErr != nil {
		return loginCallbackResult{err: resp.providerErr}, true
	}
	return loginCallbackResult{code: resp.code}, resp.code != ""
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
