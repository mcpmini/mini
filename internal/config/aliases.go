package config

// AliasesFromProjections extracts the realToolName→alias map from a set of
// per-tool projection configs, for tools that define a non-empty Alias.
// Returns nil if no tool defines an alias.
func AliasesFromProjections(proj map[string]*ProjectionConfig) map[string]string {
	aliases := make(map[string]string)
	for tool, pc := range proj {
		if pc != nil && pc.Alias != "" {
			aliases[tool] = pc.Alias
		}
	}
	if len(aliases) == 0 {
		return nil
	}
	return aliases
}
