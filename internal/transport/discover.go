package transport

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"slices"
)

type DiscoverResult struct {
	ResultType        string             `json:"resultType,omitempty"`
	SupportedVersions []string           `json:"supportedVersions"`
	Capabilities      ServerCapabilities `json:"capabilities"`
	Instructions      string             `json:"instructions,omitempty"`
	Meta              *DiscoverMeta      `json:"_meta,omitempty"`
	TTLMs             int                `json:"ttlMs"`
	CacheScope        string             `json:"cacheScope,omitempty"`
}

type ServerCapabilities struct {
	Tools *ToolsCapability `json:"tools,omitempty"`
}

type ToolsCapability struct {
	ListChanged bool `json:"listChanged"`
}

func (c ServerCapabilities) ToolsListChanged() bool {
	return c.Tools != nil && c.Tools.ListChanged
}

type DiscoverMeta struct {
	ServerInfo ServerInfo `json:"io.modelcontextprotocol/serverInfo"`
}

type UnsupportedVersionData struct {
	Supported []string `json:"supported"`
	Requested string   `json:"requested"`
}

type probeVerdict struct {
	era     Era
	version string
	listen  bool
}

func setDiscoverRequestHeaders(req *http.Request) {
	req.Header.Set("MCP-Protocol-Version", ModernVersions[0])
	req.Header.Set("Mcp-Method", "server/discover")
}

// probeOnce returns the modern version this upstream speaks, or "" for legacy.
func (c *HTTPConnection) probeOnce(ctx context.Context) (string, error) {
	c.initMu.Lock()
	defer c.initMu.Unlock()
	if c.eraKnown {
		if version := c.modernVersion.Load(); version != nil {
			return *version, nil
		}
		return "", nil
	}
	result, err := c.probe(ctx)
	verdict, err := classifyDiscover(result, err)
	if err != nil {
		return "", err
	}
	c.eraKnown = true
	if verdict.era == EraModern {
		c.modernVersion.Store(&verdict.version)
		slog.Info("upstream supports MCP 2026-07-28", "server", c.serverName)
	}
	if verdict.listen {
		//nolint:contextcheck // Listener lifetime belongs to the connection, not the probe request.
		c.startListener()
	}
	return verdict.version, nil
}

func (c *HTTPConnection) probe(ctx context.Context) (json.RawMessage, error) {
	params, err := c.prepareModernParams(nil, ModernVersions[0])
	if err != nil {
		return nil, err
	}
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
	verdict := modernVersionVerdict(discovered.SupportedVersions)
	verdict.listen = verdict.era == EraModern && discovered.Capabilities.ToolsListChanged()
	return verdict
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
