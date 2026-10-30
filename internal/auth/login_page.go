package auth

import (
	_ "embed"
	"html/template"
	"net/http"
)

//go:embed login_page.html
var loginPageHTML string

var loginPageTemplate = template.Must(template.New("login").Parse(loginPageHTML))

const offlineLoginPageCSP = "default-src 'none'; style-src 'unsafe-inline'; img-src data:; base-uri 'none'; form-action 'none'"

type loginPage struct {
	OK      bool
	Heading string
	Hint    string
	Detail  string
}

func loginResultPage(err error) loginPage {
	if err != nil {
		return failedLoginPage(err.Error())
	}
	return loginPage{OK: true, Heading: "You're signed in", Hint: "You can close this tab and return to your terminal."}
}

func failedLoginPage(detail string) loginPage {
	return loginPage{Heading: "Sign-in failed", Hint: "Return to your terminal to try again.", Detail: detail}
}

func writeLoginPage(w http.ResponseWriter, status int, page loginPage) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Content-Security-Policy", offlineLoginPageCSP)
	w.WriteHeader(status)
	//nolint:errcheck // callback handling owns the OAuth result; this page only informs the browser.
	loginPageTemplate.Execute(w, page)
	if f, ok := w.(http.Flusher); ok {
		f.Flush()
	}
}
