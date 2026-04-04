package projection


// collectElided diffs original vs projected to find keys that were removed.
func collectElided(original, projected any, prefix string) []string {
	origMap, ok1 := original.(map[string]any)
	projMap, ok2 := projected.(map[string]any)
	if !ok1 || !ok2 {
		return nil
	}

	var elided []string
	for k, origVal := range origMap {
		fullKey := joinKey(prefix, k)
		projVal, exists := projMap[k]
		if !exists {
			elided = append(elided, fullKey)
			continue
		}
		elided = append(elided, collectElided(origVal, projVal, fullKey)...)
	}
	return elided
}

func joinKey(prefix, key string) string {
	if prefix == "" {
		return key
	}
	return prefix + "." + key
}
