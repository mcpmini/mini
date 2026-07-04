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
	Code     string
	Input    json.RawMessage
	Timeout  time.Duration
	Packages []string

	// Net and Env are the effective grants for this run, resolved by the
	// caller from config.CodeModeConfig — never widened by the agent's request.
	Net []string
	Env []string

	// DangerousAllowAllNet grants unrestricted network access (bare
	// --allow-net), ignoring Net for flag purposes.
	DangerousAllowAllNet bool

	// Tools enables the mini tool bridge; nil disables it.
	Tools ToolBridge
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
	KindDependency      ErrorKind = "dependency"
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
	return execute(ctx, p, nil)
}

func execute(ctx context.Context, p Params, extraEnv []string) (json.RawMessage, error) {
	if err := validateParams(p); err != nil {
		return nil, err
	}
	denoPath, err := lookupDeno()
	if err != nil {
		return nil, err
	}
	if len(p.Packages) > 0 {
		env, err := withPackagesCacheDir(p.Packages, extraEnv)
		if err != nil {
			return nil, err
		}
		extraEnv = env
		if err := resolveDeps(ctx, denoPath, p.Packages, extraEnv); err != nil {
			return nil, err
		}
	}
	return runAndClassify(ctx, denoPath, p, extraEnv)
}

func validateParams(p Params) error {
	if len(p.Input) > 0 && !json.Valid(p.Input) {
		return &Error{Kind: KindRunner, Message: "input is not valid JSON"}
	}
	if err := validatePackages(p.Packages); err != nil {
		return err
	}
	if err := validateNetAllowList(p.Net); err != nil {
		return err
	}
	return validateEnvAllowList(p.Env)
}

func runAndClassify(ctx context.Context, denoPath string, p Params, extraEnv []string) (json.RawMessage, error) {
	marker := randutil.HexString(markerBytes)
	runCtx, cancel := context.WithTimeout(ctx, resolveTimeout(p.Timeout))
	defer cancel()
	br, err := maybeStartBridge(runCtx, p.Tools)
	if err != nil {
		return nil, &Error{Kind: KindRunner, Message: err.Error()}
	}
	defer br.close()
	pp := programParams{code: p.Code, input: p.Input, marker: marker, bridgeHostPort: br.hostPort, bridgeToken: br.token}
	opts := execOptions{packages: p.Packages, net: p.Net, env: p.Env, allowAllNet: p.DangerousAllowAllNet, extraEnv: extraEnv, bridgeHostPort: br.hostPort}
	result, runErr := runDeno(runCtx, denoPath, buildProgram(pp), opts)
	if runErr != nil {
		return nil, &Error{Kind: KindRunner, Message: runErr.Error()}
	}
	return classify(result, ctx, runCtx, marker)
}

type bridgeResult struct {
	hostPort string
	token    string
	close    func()
}

func maybeStartBridge(ctx context.Context, tools ToolBridge) (bridgeResult, error) {
	if tools == nil {
		return bridgeResult{close: func() {}}, nil
	}
	b, err := startToolBridge(ctx, tools)
	if err != nil {
		return bridgeResult{}, err
	}
	return bridgeResult{hostPort: b.hostPort(), token: b.token, close: b.close}, nil
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
