package forge

import (
	"bytes"
	"context"
	"os/exec"
	"time"
)

const depResolveTimeout = 60 * time.Second

// The only network-touching step in Execute: packages are fetched host-side
// so the sandboxed execution stays offline under --cached-only.
func resolveDeps(ctx context.Context, denoPath string, packages, extraEnv []string) error {
	resolveCtx, cancel := context.WithTimeout(ctx, depResolveTimeout)
	defer cancel()

	args := append([]string{"cache"}, packages...)
	cmd := exec.CommandContext(resolveCtx, denoPath, args...)
	cmd.Env = append(childEnv(), extraEnv...)

	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return classifyResolveFailure(ctx, resolveCtx, stderr.Bytes(), err)
	}
	return nil
}

func classifyResolveFailure(ctx, resolveCtx context.Context, stderr []byte, err error) error {
	switch {
	case ctx.Err() != nil:
		return &Error{Kind: KindCancelled, Message: "execution cancelled"}
	case resolveCtx.Err() != nil:
		return &Error{Kind: KindDependency, Message: "dependency resolution timed out"}
	default:
		return &Error{Kind: KindDependency, Message: trimStderr(stderr, err)}
	}
}
