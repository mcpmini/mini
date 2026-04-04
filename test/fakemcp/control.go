//go:build integration

package main

import (
	"encoding/json"
	"net"
	"net/http"
)

type controlServer struct {
	faults *FaultRegistry
	tools  *ToolRegistry
}

func startControlServer(faults *FaultRegistry, tools *ToolRegistry) (string, error) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return "", err
	}
	srv := &controlServer{faults: faults, tools: tools}
	mux := http.NewServeMux()
	mux.HandleFunc("/fault", srv.handleFault)
	mux.HandleFunc("/faults", srv.handleFaults)
	mux.HandleFunc("/tools", srv.handleTools)
	go http.Serve(ln, mux) //nolint:errcheck
	return ln.Addr().String(), nil
}

func (s *controlServer) handleFault(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodPut:
		var f Fault
		if err := json.NewDecoder(r.Body).Decode(&f); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		s.faults.Set(f)
		w.WriteHeader(http.StatusNoContent)
	case http.MethodDelete:
		s.faults.Clear()
		w.WriteHeader(http.StatusNoContent)
	default:
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
	}
}

func (s *controlServer) handleFaults(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	json.NewEncoder(w).Encode(s.faults.All()) //nolint:errcheck
}

func (s *controlServer) handleTools(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodPut:
		var t Tool
		if err := json.NewDecoder(r.Body).Decode(&t); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		s.tools.Add(t)
		w.WriteHeader(http.StatusNoContent)
	case http.MethodDelete:
		name := r.URL.Query().Get("name")
		s.tools.Remove(name)
		w.WriteHeader(http.StatusNoContent)
	case http.MethodGet:
		json.NewEncoder(w).Encode(s.tools.List()) //nolint:errcheck
	default:
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
	}
}
