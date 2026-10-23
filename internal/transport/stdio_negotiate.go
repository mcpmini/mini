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

var errProbeUnanswered = errors.New("upstream did not answer the modern probe")

type explicitModernProbeError struct{ err error }

func (e explicitModernProbeError) Error() string { return e.err.Error() }
func (e explicitModernProbeError) Unwrap() error { return e.err }

func (c *StdioConnection) negotiate(ctx context.Context) (*StdioConnection, error) {
	err := c.setUpEra(ctx)
	switch {
	case err == nil:
		return c, nil
	case errors.Is(err, errProbeUnanswered):
		_ = c.Close() //nolint:errcheck // the unanswered probe is the failure to report
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		return c.restartWithInitialize(ctx, errProbeUnanswered.Error())
	case c.exitedBeforeSetup(ctx, err):
		// Some legacy servers exit on an unknown method, before or after answering it.
		exit := c.Close()
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		return c.restartWithInitialize(ctx, fmt.Sprintf("upstream exited during setup (%s)", exitStatus(exit)))
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
	if verdict.listen {
		c.startSubscriptionListener(context.WithoutCancel(ctx))
	}
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
		return c.classifyDiscoveryError(discoveryErrorParams{
			ctx: ctx, probeCtx: probeCtx, err: err, version: version,
		})
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

type discoveryErrorParams struct {
	ctx      context.Context
	probeCtx context.Context
	err      error
	version  string
}

func (c *StdioConnection) classifyDiscoveryError(p discoveryErrorParams) (probeVerdict, error) {
	if p.ctx.Err() != nil {
		return probeVerdict{}, p.ctx.Err()
	}
	if verdict, handled, retry := c.classifyDiscoveryRPCError(p.probeCtx, p.err, p.version); handled {
		return verdict, retry
	}
	if p.probeCtx.Err() != nil {
		return probeVerdict{}, errProbeUnanswered
	}
	return probeVerdict{}, p.err
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
		verdict, err := c.discoverCorrected(probeCtx, mutual.version)
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

func (c *StdioConnection) discoverCorrected(ctx context.Context, protocolVersion string) (probeVerdict, error) {
	result, err := c.discoverOnce(ctx, protocolVersion)
	if err != nil {
		return probeVerdict{}, explicitModernProbeError{
			fmt.Errorf("modern discovery retry for %q failed: %w", protocolVersion, err),
		}
	}
	if ctx.Err() != nil {
		return probeVerdict{}, ctx.Err()
	}
	verdict, err := classifyStdioDiscover(result, protocolVersion)
	if err == nil && verdict.era != EraModern {
		return probeVerdict{}, explicitModernProbeError{
			fmt.Errorf("modern discovery retry for %q returned no supported versions", protocolVersion),
		}
	}
	return verdict, err
}

func legacyProbeVerdict() (probeVerdict, error) {
	return probeVerdict{era: EraLegacy}, nil
}

func classifyStdioDiscover(result json.RawMessage, requested string) (probeVerdict, error) {
	versions, recognized := discoverSupportedVersions(result)
	if !recognized {
		return legacyProbeVerdict()
	}
	verdict := modernVersionVerdict(versions)
	if verdict.era != EraModern {
		// Unlike HTTP, don't fall back: a server that answers discover is modern, so initialize would
		// fail too, and naming the versions it does support tells the user what to change.
		return probeVerdict{}, explicitModernProbeError{unsupportedStdioVersion(
			UnsupportedVersionData{Supported: versions}, requested,
		)}
	}
	if err := requireCompleteResult(result, "server/discover"); err != nil {
		return probeVerdict{}, explicitModernProbeError{fmt.Errorf("validate modern discovery result: %w", err)}
	}
	var discovered DiscoverResult
	if err := json.Unmarshal(result, &discovered); err != nil {
		return legacyProbeVerdict()
	}
	verdict.listen = discovered.Capabilities.ToolsListChanged()
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

func (c *StdioConnection) restartWithInitialize(ctx context.Context, probeFailure string) (*StdioConnection, error) {
	if ctx.Err() != nil {
		return nil, ctx.Err()
	}
	replacement, err := startSubprocess(context.WithoutCancel(ctx), c.command)
	if err == nil {
		if err = replacement.initialize(ctx); err != nil {
			_ = replacement.Close() //nolint:errcheck // the probe failure is the failure to report
		}
	}
	if err != nil {
		return nil, fmt.Errorf("%s; restarting it without the modern probe failed too: %w", probeFailure, err)
	}
	return replacement, nil
}

func (c *StdioConnection) initialize(ctx context.Context) error {
	//nolint:errcheck // fixed handshake fields contain only JSON-serializable values.
	params, _ := json.Marshal(newInitializeParams())
	raw, err := c.Call(ctx, "initialize", params)
	if err != nil {
		return err
	}
	result, err := parseInitializeResult(raw)
	if err != nil {
		return err
	}
	c.logger.Info("upstream connected", "server", result.ServerInfo.Name, "protocol", result.ProtocolVersion)
	return c.writeJSONContext(ctx, Notification{JsonRPC: JsonRPCVersion, Method: NotificationInitialized})
}
