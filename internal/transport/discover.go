package transport

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"slices"

	"github.com/mcpmini/mini/internal/version"
)

type DiscoverResult struct {
	SupportedVersions []string       `json:"supportedVersions"`
	Capabilities      map[string]any `json:"capabilities"`
	Instructions      string         `json:"instructions,omitempty"`
}

type UnsupportedVersionData struct {
	Supported []string `json:"supported"`
	Requested string   `json:"requested"`
}

type probeVerdict struct {
	era     Era
	version string
}

type discoverRequestMeta struct {
	ProtocolVersion string         `json:"io.modelcontextprotocol/protocolVersion"`
	ClientInfo      ClientInfo     `json:"io.modelcontextprotocol/clientInfo"`
	Capabilities    map[string]any `json:"io.modelcontextprotocol/clientCapabilities"`
}

type discoverRequestParams struct {
	Meta discoverRequestMeta `json:"_meta"`
}

func setDiscoverRequestHeaders(req *http.Request) {
	req.Header.Set("MCP-Protocol-Version", ModernVersions[0])
	req.Header.Set("Mcp-Method", "server/discover")
}

func (c *HTTPConnection) probeOnce(ctx context.Context) error {
	c.initMu.Lock()
	defer c.initMu.Unlock()
	if c.eraKnown {
		return nil
	}
	result, err := c.probe(ctx)
	verdict, err := classifyDiscover(result, err)
	if err != nil {
		return err
	}
	c.era, c.modernVersion, c.eraKnown = verdict.era, verdict.version, true
	if verdict.era == EraModern {
		slog.Info("upstream supports MCP 2026-07-28", "server", c.serverName)
	}
	return nil
}

func (c *HTTPConnection) probe(ctx context.Context) (json.RawMessage, error) {
	//nolint:errcheck // fixed discover fields contain only JSON-serializable values.
	params, _ := json.Marshal(discoverRequestParams{Meta: discoverRequestMeta{
		ProtocolVersion: ModernVersions[0],
		ClientInfo:      ClientInfo{Name: "mini", Version: version.Version},
		Capabilities:    map[string]any{},
	}})
	return c.rpc(ctx, "server/discover", params)
}

func classifyDiscover(result json.RawMessage, err error) (probeVerdict, error) {
	if err == nil {
		return classifyDiscoverResult(result), nil
	}
	return classifyDiscoverError(err)
}

func classifyDiscoverError(err error) (probeVerdict, error) {
	// The spec reads anything other than a recognized modern answer as legacy, including a reply that answers nothing.
	var unusable unusableResponseError
	if errors.As(err, &unusable) {
		return probeVerdict{era: EraLegacy}, nil
	}
	var rpcErr *RPCError
	if errors.As(err, &rpcErr) {
		return classifyDiscoverRPC(rpcErr)
	}
	var statusErr *HTTPStatusError
	if errors.As(err, &statusErr) {
		if statusErr.RPC != nil {
			return classifyDiscoverRPC(statusErr.RPC)
		}
		if isLegacyProbeStatus(statusErr.StatusCode) {
			return probeVerdict{era: EraLegacy}, nil
		}
	}
	return probeVerdict{}, err
}

func classifyDiscoverResult(result json.RawMessage) probeVerdict {
	var discovered DiscoverResult
	if json.Unmarshal(result, &discovered) != nil {
		return probeVerdict{era: EraLegacy}
	}
	return modernVersionVerdict(discovered.SupportedVersions)
}

func classifyDiscoverRPC(err *RPCError) (probeVerdict, error) {
	if err.Code == CodeHeaderMismatch || err.Code == CodeMissingRequiredClientCapability {
		return probeVerdict{}, err
	}
	if err.Code != CodeUnsupportedProtocolVersion {
		return probeVerdict{era: EraLegacy}, nil
	}
	data, ok := decodeUnsupportedVersionData(err.Data)
	if !ok {
		return probeVerdict{era: EraLegacy}, nil
	}
	// The spec says not to fall back after -32022, but servers that list only legacy versions here
	// (Atlassian v2) still accept initialize.
	return modernVersionVerdict(data.Supported), nil
}

func decodeUnsupportedVersionData(raw json.RawMessage) (UnsupportedVersionData, bool) {
	var data UnsupportedVersionData
	err := json.Unmarshal(raw, &data)
	return data, err == nil
}

func modernVersionVerdict(supported []string) probeVerdict {
	for _, version := range ModernVersions {
		if slices.Contains(supported, version) {
			return probeVerdict{era: EraModern, version: version}
		}
	}
	return probeVerdict{era: EraLegacy}
}

func isLegacyProbeStatus(status int) bool {
	return status >= 400 && status < 500 && status != 403 && status != 408 && status != 429
}
