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
		elided = append(elided, elidedForKey(prefix, k, origVal, projMap)...)
	}
	return elided
}

func elidedForKey(prefix, k string, origVal any, projMap map[string]any) []string {
	fullKey := joinKey(prefix, k)
	projVal, exists := projMap[k]
	if !exists {
		return []string{fullKey}
	}
	return collectElided(origVal, projVal, fullKey)
}

func joinKey(prefix, key string) string {
	if prefix == "" {
		return key
	}
	return prefix + "." + key
}
