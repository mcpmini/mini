// Package forge runs agent-submitted TypeScript in a sandboxed Deno
// subprocess and returns its JSON result.
package forge

import (
	"context"
	"encoding/json"
	"os/exec"
	"time"

	"github.com/mcpmini/mini/internal/randutil"
)

type Params struct {
	Code    string
	Input   json.RawMessage
	Timeout time.Duration
}

type ErrorKind string

const (
	KindSyntax          ErrorKind = "syntax"
	KindRuntime         ErrorKind = "runtime"
	KindTimeout         ErrorKind = "timeout"
	KindCancelled       ErrorKind = "cancelled"
	KindNotSerializable ErrorKind = "not_serializable"
	KindOutputTooLarge  ErrorKind = "output_too_large"
	KindRunner          ErrorKind = "runner"
)

type Error struct {
	Kind    ErrorKind
	Message string
	Console string
}

func (e *Error) Error() string {
	s := "forge " + string(e.Kind) + ": " + e.Message
	if e.Console != "" {
		s += "\nconsole output:\n" + e.Console
	}
	return s
}

const (
	defaultTimeout = 30 * time.Second
	markerBytes    = 8
)

// Execute runs Code in a fresh sandboxed Deno subprocess and returns the
// function's return value serialized as JSON.
func Execute(ctx context.Context, p Params) (json.RawMessage, error) {
	if len(p.Input) > 0 && !json.Valid(p.Input) {
		return nil, &Error{Kind: KindRunner, Message: "input is not valid JSON"}
	}
	denoPath, err := lookupDeno()
	if err != nil {
		return nil, err
	}

	marker := randutil.HexString(markerBytes)
	program := buildProgram(p.Code, p.Input, marker)

	runCtx, cancel := context.WithTimeout(ctx, resolveTimeout(p.Timeout))
	defer cancel()

	result, runErr := runDeno(runCtx, denoPath, program)
	if runErr != nil {
		return nil, &Error{Kind: KindRunner, Message: runErr.Error()}
	}
	return classify(result, ctx, runCtx, marker)
}

func lookupDeno() (string, error) {
	path, err := exec.LookPath("deno")
	if err != nil {
		return "", &Error{Kind: KindRunner, Message: "deno not found in PATH; install Deno: https://deno.com/"}
	}
	return path, nil
}

func resolveTimeout(t time.Duration) time.Duration {
	if t == 0 {
		return defaultTimeout
	}
	return t
}
