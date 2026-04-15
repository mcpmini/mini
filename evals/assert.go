//go:build evals

package evals

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

func toolCallCounts(callLogDir, server string) map[string]int {
	data, err := os.ReadFile(filepath.Join(callLogDir, server+".log"))
	if err != nil {
		return nil
	}
	counts := make(map[string]int)
	for _, line := range strings.Split(strings.TrimSpace(string(data)), "\n") {
		var entry struct {
			Tool string `json:"tool"`
		}
		if json.Unmarshal([]byte(line), &entry) == nil && entry.Tool != "" {
			counts[entry.Tool]++
		}
	}
	return counts
}

func AssertToolCalled(callLogDir, server, tool string) error {
	counts := toolCallCounts(callLogDir, server)
	if counts[tool] == 0 {
		return fmt.Errorf("%s.%s was not called — actual: %v", server, tool, counts)
	}
	return nil
}

func AssertResponseContains(text string, want ...string) error {
	lower := strings.ToLower(text)
	for _, w := range want {
		if strings.Contains(lower, strings.ToLower(w)) {
			return nil
		}
	}
	preview := text
	if len(preview) > 300 {
		preview = preview[:300]
	}
	return fmt.Errorf("response missing all of %v\nResponse: %s", want, preview)
}
