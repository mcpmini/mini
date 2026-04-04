//go:build integration

package main

import (
	"math/rand"
	"sync"
)

type FaultType string

const (
	FaultDelay        FaultType = "delay"
	FaultErrorResult  FaultType = "error_response"
	FaultRPCError     FaultType = "rpc_error"
	FaultHang         FaultType = "hang"
	FaultBadJSON      FaultType = "bad_json"
	FaultOversized    FaultType = "oversized_response"
	FaultDrop         FaultType = "connection_drop"
	FaultIntermittent FaultType = "intermittent"
	FaultSlowInit     FaultType = "slow_initialize"
)

type Fault struct {
	Tool        string    `json:"tool"`                  // tool name or "*" for all
	Method      string    `json:"method"`                // "tools/call", "initialize", "*"
	Type        FaultType `json:"type"`
	DelayMS     int       `json:"delay_ms,omitempty"`
	Message     string    `json:"message,omitempty"`
	SizeBytes   int       `json:"size_bytes,omitempty"`
	Probability float64   `json:"probability,omitempty"` // 0.0–1.0, default 1.0
}

type FaultRegistry struct {
	mu     sync.RWMutex
	faults []Fault
}

func (r *FaultRegistry) Set(f Fault) {
	if f.Probability == 0 {
		f.Probability = 1.0
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.faults = append(r.faults, f)
}

func (r *FaultRegistry) Clear() {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.faults = nil
}

func (r *FaultRegistry) All() []Fault {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make([]Fault, len(r.faults))
	copy(out, r.faults)
	return out
}

func (r *FaultRegistry) Match(method, tool string) (Fault, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	for _, f := range r.faults {
		if matchesField(f.Method, method) && matchesField(f.Tool, tool) {
			if rand.Float64() < f.Probability {
				return f, true
			}
		}
	}
	return Fault{}, false
}

func matchesField(pattern, value string) bool {
	return pattern == "*" || pattern == "" || pattern == value
}
