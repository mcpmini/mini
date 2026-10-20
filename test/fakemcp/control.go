//go:build integration

package main

import (
	"context"
	"encoding/json"
	"net"
	"net/http"
)

type controlServer struct {
	faults *FaultRegistry
	tools  *ToolRegistry
	notify func()
}

type controlServerParams struct {
	Addr   string
	Faults *FaultRegistry
	Tools  *ToolRegistry
	Notify func()
}

func startControlServer(p controlServerParams) (string, error) {
	ln, err := (&net.ListenConfig{}).Listen(context.Background(), "tcp", p.Addr)
	if err != nil {
		return "", err
	}
	srv := &controlServer{faults: p.Faults, tools: p.Tools, notify: p.Notify}
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
		if err := s.faults.Set(f); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
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
		s.handleToolsPut(w, r)
	case http.MethodDelete:
		if s.tools.Remove(r.URL.Query().Get("name")) && s.notify != nil {
			s.notify()
		}
		w.WriteHeader(http.StatusNoContent)
	case http.MethodGet:
		json.NewEncoder(w).Encode(s.tools.List()) //nolint:errcheck
	default:
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
	}
}

func (s *controlServer) handleToolsPut(w http.ResponseWriter, r *http.Request) {
	tool, err := decodeTool(r)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	if s.tools.Add(tool) && s.notify != nil {
		s.notify()
	}
	w.WriteHeader(http.StatusNoContent)
}

func decodeTool(r *http.Request) (Tool, error) {
	var t Tool
	err := json.NewDecoder(r.Body).Decode(&t)
	return t, err
}
