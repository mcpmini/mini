package auth

import (
	_ "embed"
	"encoding/base64"
	"html/template"
	"net/http"
	"net/url"
)

//go:embed login_page.html
var loginPageHTML string

//go:embed mini_icon.png
var miniIconPNG []byte

var miniIconURL = template.URL("data:image/png;base64," + base64.StdEncoding.EncodeToString(miniIconPNG))

var loginPageTemplate = template.Must(template.New("login").
	Funcs(template.FuncMap{"miniIcon": func() template.URL { return miniIconURL }}).
	Parse(loginPageHTML))

const offlineLoginPageCSP = "default-src 'none'; style-src 'unsafe-inline'; img-src data:; base-uri 'none'; form-action 'none'"

type loginPageServer struct {
	Name string
	Host string
}

func newLoginPageServer(name, serverURL string) loginPageServer {
	server := loginPageServer{Name: name}
	if u, err := url.Parse(serverURL); err == nil {
		server.Host = u.Host
	}
	return server
}

type loginPage struct {
	Server loginPageServer
	OK     bool
	Detail string
}

func loginResultPage(server loginPageServer, err error) loginPage {
	if err != nil {
		return failedLoginPage(server, err.Error())
	}
	return loginPage{Server: server, OK: true}
}

func failedLoginPage(server loginPageServer, detail string) loginPage {
	return loginPage{Server: server, Detail: detail}
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
