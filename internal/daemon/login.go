package daemon

// LoginPath runs a login for a terminal: the response streams one LoginLine with the URL, then one with
// the outcome.
const LoginPath = "/login"

type LoginRequest struct {
	Server string `json:"server"`
}

type LoginLine struct {
	URL     string `json:"url,omitempty"`
	OK      bool   `json:"ok,omitempty"`
	Expires string `json:"expires,omitempty"`
	// ConnectError is set when the login saved its token but connecting with it then failed.
	ConnectError string `json:"connect_error,omitempty"`
	Error        string `json:"error,omitempty"`
	Message      string `json:"message,omitempty"`
}
