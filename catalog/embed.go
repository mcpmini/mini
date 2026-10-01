package catalog

import _ "embed"

// Embedding only reaches files in this directory, and v1.json lives here because GitHub
// Pages publishes it. internal/catalog parses and validates these bytes into typed entries.
//
//go:embed v1.json
var v1 []byte

func V1() []byte { return v1 }
