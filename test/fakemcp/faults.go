//go:build integration

package main

import (
	"fmt"
	"math/rand"
	"sync"
	"time"
)

type FaultType string

const (
	FaultDelay       FaultType = "delay"
	FaultErrorResult FaultType = "error_response"
	FaultRPCError    FaultType = "rpc_error"
	FaultHang        FaultType = "hang"
	FaultBadJSON     FaultType = "bad_json"
	FaultOversized   FaultType = "oversized_response"
	FaultDrop        FaultType = "connection_drop"
	FaultSlowInit    FaultType = "slow_initialize"
)

type Fault struct {
	Tool        string    `json:"tool"`   // tool name or "*" for all
	Method      string    `json:"method"` // "tools/call", "initialize", "*"
	Type        FaultType `json:"type"`
	DelayMS     int       `json:"delay_ms,omitempty"`
	Message     string    `json:"message,omitempty"`
	SizeBytes   int       `json:"size_bytes,omitempty"`
	Probability float64   `json:"probability,omitempty"` // 0.0–1.0, default 1.0
}

type FaultRegistry struct {
	mu     sync.RWMutex
	faults []registeredFault
}

func (r *FaultRegistry) Set(f Fault) error {
	effect, err := f.effect()
	if err != nil {
		return err
	}
	if f.Probability == 0 {
		f.Probability = 1.0
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.faults = append(r.faults, registeredFault{spec: f, effect: effect})
	return nil
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
	for i, f := range r.faults {
		out[i] = f.spec
	}
	return out
}

func (r *FaultRegistry) Match(method, tool string) (registeredFault, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	for _, f := range r.faults {
		if matchesField(f.spec.Method, method) && matchesField(f.spec.Tool, tool) {
			if rand.Float64() < f.spec.Probability {
				return f, true
			}
		}
	}
	return registeredFault{}, false
}

func matchesField(pattern, value string) bool {
	return pattern == "*" || pattern == "" || pattern == value
}

func (f Fault) effect() (faultEffect, error) {
	switch f.Type {
	case FaultDelay:
		return delayFault{duration: time.Duration(f.DelayMS) * time.Millisecond}, nil
	case FaultSlowInit:
		return slowInitFault{duration: time.Duration(f.DelayMS) * time.Millisecond}, nil
	case FaultHang:
		return hangFault{}, nil
	case FaultDrop:
		return dropFault{}, nil
	case FaultBadJSON:
		return badJSONFault{}, nil
	case FaultRPCError:
		return rpcErrorFault{message: faultMessage(f.Message, "injected rpc error")}, nil
	case FaultErrorResult:
		return errorResultFault{message: faultMessage(f.Message, "injected tool error")}, nil
	case FaultOversized:
		return oversizedFault{sizeBytes: f.SizeBytes}, nil
	}
	return nil, fmt.Errorf("unsupported fault type %q", f.Type)
}
