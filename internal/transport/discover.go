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
