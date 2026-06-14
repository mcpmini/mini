package config

// Returns nil rather than an empty map when no alias is defined.
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
