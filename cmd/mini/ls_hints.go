package main

import (
	"encoding/json"
	"strings"
)

type toolAnnotations struct {
	ReadOnly    bool `json:"readOnlyHint"`
	Destructive bool `json:"destructiveHint"`
	Idempotent  bool `json:"idempotentHint"`
	OpenWorld   bool `json:"openWorldHint"`
}

func toolHints(raw json.RawMessage) string {
	var hints toolAnnotations
	if err := json.Unmarshal(raw, &hints); err != nil {
		return ""
	}
	return strings.Join(hints.labels(), ", ")
}

func (h toolAnnotations) labels() []string {
	var labels []string
	if h.ReadOnly {
		labels = append(labels, "read-only")
	} else {
		if h.Destructive {
			labels = append(labels, "destructive")
		}
		if h.Idempotent {
			labels = append(labels, "idempotent")
		}
	}
	if h.OpenWorld {
		labels = append(labels, "open-world")
	}
	return labels
}
