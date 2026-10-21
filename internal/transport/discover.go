package transport

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
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

func (c *HTTPConnection) probeOnce(ctx context.Context) (modernVersion string, err error) {
	c.initMu.Lock()
	defer c.initMu.Unlock()
	if c.eraKnown {
		return c.negotiatedModernVersion(), nil
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
	params, err := prepareModernParams(nil, ModernVersions[0])
	if err != nil {
		return nil, err
	}
	return c.rpc(ctx, "server/discover", params)
}

func classifyDiscover(result json.RawMessage, err error) (probeVerdict, error) {
	if err == nil {
		return classifyDiscoverResult(result)
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

func classifyDiscoverResult(result json.RawMessage) (probeVerdict, error) {
	versions, recognized := discoverSupportedVersions(result)
	if !recognized {
		return legacyProbeVerdict()
	}
	verdict := modernVersionVerdict(versions)
	if verdict.era != EraModern {
		return verdict, nil
	}
	if err := requireCompleteResult(result, "server/discover"); err != nil {
		return probeVerdict{}, err
	}
	var discovered DiscoverResult
	if err := json.Unmarshal(result, &discovered); err != nil {
		return legacyProbeVerdict()
	}
	verdict.listen = verdict.era == EraModern && discovered.Capabilities.ToolsListChanged()
	return verdict, nil
}

func discoverSupportedVersions(raw json.RawMessage) ([]string, bool) {
	var result struct {
		SupportedVersions []string `json:"supportedVersions"`
	}
	if err := json.Unmarshal(raw, &result); err != nil || result.SupportedVersions == nil {
		return nil, false
	}
	return result.SupportedVersions, true
}

func requireCompleteResult(raw json.RawMessage, method string) error {
	resultType, err := ResultTypeOf(raw)
	if err != nil {
		return err
	}
	if resultType != ResultComplete {
		return fmt.Errorf("upstream asked for input during %s, which mini cannot relay", method)
	}
	return nil
}

func classifyDiscoverRPC(err *RPCError) (probeVerdict, error) {
	if isModernRejection(err.Code) {
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

// A different protocol version can't fix these, so falling back to initialize would only hide them.
func isModernRejection(code int) bool {
	return code == CodeHeaderMismatch || code == CodeMissingRequiredClientCapability
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
