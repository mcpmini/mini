package transport

import "uuid"

// NewSessionID generates a random UUID v4 for session identification.
func NewSessionID() string {
	return uuid.NewV4().String()
}

// normalizeID converts JSON-decoded float64 IDs to int64.
// The JSON decoder unmarshals numbers into interface{} as float64,
// but RPC IDs are integers — callers compare them as int64.
func normalizeID(id any) any {
	if f, ok := id.(float64); ok {
		return int64(f)
	}
	return id
}
