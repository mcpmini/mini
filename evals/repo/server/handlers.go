// Package server handles HTTP requests for the webapp API.
package server

import (
	"encoding/json"
	"net/http"
)

type apiResponse struct {
	OK      bool   `json:"ok"`
	Message string `json:"message,omitempty"`
}

// HandleHealth returns a 200 OK with server status.
func HandleHealth(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(apiResponse{OK: true, Message: "healthy"}) //nolint:errcheck
}

// HandleNotFound returns a 404 with a JSON error body.
func HandleNotFound(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusNotFound)
	json.NewEncoder(w).Encode(apiResponse{OK: false, Message: "not found"}) //nolint:errcheck
}
