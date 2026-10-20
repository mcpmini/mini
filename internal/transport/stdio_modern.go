package transport

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/mcpmini/mini/internal/clock"
)

const stdioDiscoveryTimeout = 5 * time.Second

type explicitModernProbeError struct{ err error }

func (e explicitModernProbeError) Error() string { return e.err.Error() }
func (e explicitModernProbeError) Unwrap() error { return e.err }

func (c *StdioConnection) negotiate(ctx context.Context) (*StdioConnection, error) {
	err := c.setUpEra(ctx)
	if err == nil {
		return c, nil
	}
	if c.exitedBeforeSetup(ctx, err) {
		// Some legacy servers exit on an unknown method, before or after answering it.
		return c.replaceExitedProbe(ctx)
	}
	_ = c.Close() //nolint:errcheck // cleanup must not mask the setup error
	return nil, err
}

func (c *StdioConnection) setUpEra(ctx context.Context) error {
	verdict, err := c.discover(ctx)
	if err != nil {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		return fmt.Errorf("discover upstream protocol: %w", err)
	}
	if verdict.era != EraModern {
		return c.initialize(ctx)
	}
	c.setModern(verdict.version)
	c.logger.Info("upstream connected", "protocol", verdict.version)
	return nil
}

func (c *StdioConnection) exitedBeforeSetup(ctx context.Context, err error) bool {
	var explicit explicitModernProbeError
	return ctx.Err() == nil && !errors.As(err, &explicit) && c.isClosed()
}

func (c *StdioConnection) setModern(protocolVersion string) {
	c.modernVersion.Store(&protocolVersion)
}

func (c *StdioConnection) negotiatedModernVersion() string {
	if v := c.modernVersion.Load(); v != nil {
		return *v
	}
	return ""
}

func (c *StdioConnection) isModern() bool {
	return c.negotiatedModernVersion() != ""
}

func (c *StdioConnection) discover(ctx context.Context) (probeVerdict, error) {
	budget := discoveryBudget(ctx, c.clock)
	if budget <= 0 {
		if ctx.Err() != nil {
			return probeVerdict{}, ctx.Err()
		}
		return legacyProbeVerdict()
	}
	probeCtx, cancel := clock.WithTimeout(ctx, c.clock, budget)
	defer cancel()
	version := ModernVersions[0]
	result, err := c.discoverOnce(probeCtx, version)
	if err != nil {
		return c.classifyDiscoveryError(ctx, probeCtx, err, version)
	}
	if ctx.Err() != nil {
		return probeVerdict{}, ctx.Err()
	}
	return classifyStdioDiscover(result, version)
}

func discoveryBudget(ctx context.Context, clockSource clock.Clock) time.Duration {
	budget := stdioDiscoveryTimeout
	if deadline, ok := ctx.Deadline(); ok {
		remaining := clockSource.Until(deadline) / 2
		if remaining < budget {
			budget = remaining
		}
	}
	return budget
}

func (c *StdioConnection) classifyDiscoveryError(
	ctx context.Context,
	probeCtx context.Context,
	err error,
	version string,
) (probeVerdict, error) {
	if ctx.Err() != nil {
		return probeVerdict{}, ctx.Err()
	}
	if verdict, handled, retry := c.classifyDiscoveryRPCError(probeCtx, err, version); handled {
		return verdict, retry
	}
	if probeCtx.Err() != nil {
		return legacyProbeVerdict()
	}
	return probeVerdict{}, err
}

func (c *StdioConnection) classifyDiscoveryRPCError(
	probeCtx context.Context,
	err error,
	version string,
) (probeVerdict, bool, error) {
	var rpcErr *RPCError
	if !errors.As(err, &rpcErr) {
		return probeVerdict{}, false, nil
	}
	if rpcErr.Code == CodeUnsupportedProtocolVersion {
		return c.retrySupportedDiscovery(probeCtx, rpcErr, version)
	}
	if isModernRejection(rpcErr.Code) {
		return probeVerdict{}, true, explicitModernProbeError{fmt.Errorf("modern discovery rejected: %w", rpcErr)}
	}
	return probeVerdict{era: EraLegacy}, true, nil
}

func (c *StdioConnection) retrySupportedDiscovery(
	probeCtx context.Context,
	rpcErr *RPCError,
	version string,
) (probeVerdict, bool, error) {
	data, ok := decodeUnsupportedVersionData(rpcErr.Data)
	mutual := modernVersionVerdict(data.Supported)
	if ok && mutual.era == EraModern {
		verdict, err := c.discoverCorrected(probeCtx, mutual.version, data)
		return verdict, true, err
	}
	err := explicitModernProbeError{unsupportedStdioVersion(data, version)}
	return probeVerdict{}, true, err
}

func (c *StdioConnection) discoverOnce(ctx context.Context, protocolVersion string) (json.RawMessage, error) {
	params, err := prepareModernParams(nil, protocolVersion)
	if err != nil {
		return nil, err
	}
	return c.rpc(ctx, "server/discover", params)
}

func (c *StdioConnection) discoverCorrected(
	ctx context.Context,
	protocolVersion string,
	data UnsupportedVersionData,
) (probeVerdict, error) {
	result, err := c.discoverOnce(ctx, protocolVersion)
	if err != nil {
		return probeVerdict{}, explicitModernProbeError{
			fmt.Errorf("modern discovery retry for %q failed: %w", protocolVersion, err),
		}
	}
	if ctx.Err() != nil {
		return probeVerdict{}, ctx.Err()
	}
	return classifyStdioDiscover(result, protocolVersion)
}

func legacyProbeVerdict() (probeVerdict, error) {
	return probeVerdict{era: EraLegacy}, nil
}

func classifyStdioDiscover(result json.RawMessage, requested string) (probeVerdict, error) {
	var discovered DiscoverResult
	if err := json.Unmarshal(result, &discovered); err != nil {
		return legacyProbeVerdict()
	}
	if discovered.SupportedVersions == nil {
		return legacyProbeVerdict()
	}
	verdict := modernVersionVerdict(discovered.SupportedVersions)
	if verdict.era != EraModern {
		return probeVerdict{}, explicitModernProbeError{unsupportedStdioVersion(
			UnsupportedVersionData{Supported: discovered.SupportedVersions}, requested,
		)}
	}
	return verdict, nil
}

func unsupportedStdioVersion(data UnsupportedVersionData, requested string) error {
	if data.Requested != "" {
		requested = data.Requested
	}
	return fmt.Errorf(
		"upstream does not support requested modern protocol %q (supported: %v)",
		requested,
		data.Supported,
	)
}

func (c *StdioConnection) isClosed() bool {
	select {
	case <-c.done:
		return true
	default:
		return false
	}
}

func (c *StdioConnection) replaceExitedProbe(ctx context.Context) (*StdioConnection, error) {
	exit := c.Close()
	replacement, err := startSubprocess(context.WithoutCancel(ctx), c.command)
	if err == nil {
		if err = replacement.initialize(ctx); err != nil {
			_ = replacement.Close() //nolint:errcheck // the first exit is the failure to report
		}
	}
	if err != nil {
		// A server that crashes at startup fails the same way twice; its first exit is what explains it.
		return nil, fmt.Errorf(
			"upstream exited during setup (%s); restarting it without the modern probe failed too: %w",
			exitStatus(exit),
			err,
		)
	}
	return replacement, nil
}

func exitStatus(waitErr error) string {
	if waitErr == nil {
		return "exit status 0"
	}
	return waitErr.Error()
}
