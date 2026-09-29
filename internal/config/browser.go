package config

func (c *Config) BrowserCommandFor(sc ServerConfig) (command string, open bool) {
	if c.DisableAuthBrowserOpen {
		return "", false
	}
	if sc.Auth != nil && sc.Auth.BrowserCmd != "" {
		return sc.Auth.BrowserCmd, true
	}
	return c.BrowserCommand, true
}
