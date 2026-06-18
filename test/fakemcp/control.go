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
	out    *stdoutWriter
}

func startControlServer(faults *FaultRegistry, tools *ToolRegistry, out *stdoutWriter) (string, error) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return "", err
	}
	srv := &controlServer{faults: faults, tools: tools, out: out}
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
		s.handleToolsPut(w, r)
	case http.MethodDelete:
		s.tools.Remove(r.URL.Query().Get("name"))
		s.out.notifyToolsChanged()
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
	s.tools.Add(tool)
	s.out.notifyToolsChanged()
	w.WriteHeader(http.StatusNoContent)
}

func decodeTool(r *http.Request) (Tool, error) {
	var t Tool
	err := json.NewDecoder(r.Body).Decode(&t)
	return t, err
}
