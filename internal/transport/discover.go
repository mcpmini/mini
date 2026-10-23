package transport

import (
	"encoding/json"
	"fmt"
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

func classifyDiscoverResult(result json.RawMessage, requested string) (probeVerdict, error) {
	versions, recognized := discoverSupportedVersions(result)
	if !recognized {
		return legacyProbeVerdict()
	}
	verdict, shared := sharedVersionVerdict(versions)
	if !shared {
		return probeVerdict{}, unsupportedVersionError(UnsupportedVersionData{Supported: versions}, requested)
	}
	if verdict.era != EraModern {
		return verdict, nil
	}
	if err := requireCompleteResult(result, "server/discover"); err != nil {
		return probeVerdict{}, fmt.Errorf("validate modern discovery result: %w", err)
	}
	var discovered DiscoverResult
	if err := json.Unmarshal(result, &discovered); err != nil {
		return legacyProbeVerdict()
	}
	verdict.listen = discovered.Capabilities.ToolsListChanged()
	return verdict, nil
}

// A modern verdict here comes from -32022 naming a version both sides support; the caller decides
// whether to probe again with it. Any other code means legacy, since legacy servers answer with
// whatever error they like.
// https://github.com/modelcontextprotocol/modelcontextprotocol/blob/80041ab0a139a8f4fb3adbc99f7ae9113ea59671/docs/specification/2026-07-28/basic/transports/stdio.mdx?plain=1#L139-L141
func classifyDiscoverRejection(err *RPCError, requested string) (probeVerdict, error) {
	if isModernRejection(err.Code) {
		return probeVerdict{}, fmt.Errorf("modern discovery rejected: %w", err)
	}
	if err.Code != CodeUnsupportedProtocolVersion {
		return legacyProbeVerdict()
	}
	data := decodeUnsupportedVersionData(err.Data)
	if verdict, shared := sharedVersionVerdict(data.Supported); shared {
		return verdict, nil
	}
	return probeVerdict{}, unsupportedVersionError(data, requested)
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

// A different protocol version can't fix these, so falling back to initialize would only hide them.
// https://github.com/modelcontextprotocol/modelcontextprotocol/blob/80041ab0a139a8f4fb3adbc99f7ae9113ea59671/docs/specification/2026-07-28/basic/transports/streamable-http.mdx?plain=1#L660-L662
func isModernRejection(code int) bool {
	return code == CodeHeaderMismatch || code == CodeMissingRequiredClientCapability
}

func decodeUnsupportedVersionData(raw json.RawMessage) UnsupportedVersionData {
	var data UnsupportedVersionData
	//nolint:errcheck // undecodable data lists no versions, which reports as sharing none with mini
	_ = json.Unmarshal(raw, &data)
	return data
}

func unsupportedVersionError(data UnsupportedVersionData, requested string) error {
	if data.Requested != "" {
		requested = data.Requested
	}
	return fmt.Errorf(
		"upstream does not support requested modern protocol %q (supported: %v)",
		requested,
		data.Supported,
	)
}

// A shared legacy version means the server also accepts initialize; sharing none is an error.
// https://github.com/modelcontextprotocol/modelcontextprotocol/blob/80041ab0a139a8f4fb3adbc99f7ae9113ea59671/docs/specification/2026-07-28/basic/versioning.mdx?plain=1#L69-L71
func sharedVersionVerdict(supported []string) (probeVerdict, bool) {
	for _, version := range ModernVersions {
		if slices.Contains(supported, version) {
			return probeVerdict{era: EraModern, version: version}, true
		}
	}
	if slices.ContainsFunc(supported, IsLegacyVersion) {
		return probeVerdict{era: EraLegacy}, true
	}
	return probeVerdict{}, false
}

func legacyProbeVerdict() (probeVerdict, error) {
	return probeVerdict{era: EraLegacy}, nil
}
