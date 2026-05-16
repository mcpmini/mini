//go:build integration

// fakemcp is a configurable fake MCP stdio server for integration testing.
// It serves fixture JSON files as tool responses and supports runtime fault
// injection via an HTTP control API.
//
// Usage:
//
//	fakemcp --fixtures benchmarks/fixtures/github
//	fakemcp --fixtures DIR --control-addr 127.0.0.1:0
//
// The control API address is printed to stderr on startup:
//
//	fakemcp control=127.0.0.1:PORT
//
// Control API:
//
//	PUT    /fault   {"tool":"*","method":"*","type":"delay","delay_ms":500}
//	DELETE /fault   clear all faults
//	GET    /faults  list current faults
//	PUT    /tools   {"name":"mytool","description":"...","content":"{}"}
//	DELETE /tools?name=mytool
//	GET    /tools
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"
)

type fakeOpts struct {
	tools  *ToolRegistry
	faults *FaultRegistry
}

func parseFlags() (fakeOpts, error) {
	fixturesDir := flag.String("fixtures", "", "directory of fixture JSON files (each .json = one tool)")
	_ = flag.String("control-addr", "127.0.0.1:0", "host:port for HTTP control API (0 = random port)")
	initialFault := flag.String("initial-fault", "", "JSON-encoded Fault to apply at startup (e.g. for subprocess fault injection)")
	callLog := flag.String("call-log", "", "append a JSON line per tool call to this file")
	flag.Parse()
	faults := &FaultRegistry{}
	tools := newToolRegistry(*callLog)
	loadFixtures(tools, *fixturesDir)
	if err := setInitialFault(faults, *initialFault); err != nil {
		return fakeOpts{}, err
	}
	return fakeOpts{tools: tools, faults: faults}, nil
}

func loadFixtures(tools *ToolRegistry, fixturesDir string) {
	if fixturesDir != "" {
		tools.LoadFixtures(fixturesDir)
	}
}

func setInitialFault(faults *FaultRegistry, raw string) error {
	if raw == "" {
		return nil
	}
	var f Fault
	if err := json.Unmarshal([]byte(raw), &f); err != nil {
		return fmt.Errorf("--initial-fault: %w", err)
	}
	faults.Set(f)
	return nil
}

func main() {
	opts, err := parseFlags()
	if err != nil {
		fmt.Fprintf(os.Stderr, "fakemcp: %v\n", err)
		os.Exit(1)
	}
	addr, err := startControlServer(opts.faults, opts.tools)
	if err != nil {
		fmt.Fprintf(os.Stderr, "fakemcp: control server: %v\n", err)
		os.Exit(1)
	}
	fmt.Fprintf(os.Stderr, "fakemcp control=%s\n", addr)
	serve(&mcpHandler{tools: opts.tools, faults: opts.faults})
}
