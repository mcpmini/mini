package transporttest

import (
	"encoding/json"

	"github.com/mcpmini/mini/internal/transport"
)

// ModernRequest encodes one newline-terminated 2026-07-28 request whose params carry the
// per-request metadata alongside the given typed params.
func ModernRequest(id any, method string, params any) []byte {
	return ModernRequestVersion(transport.ModernVersions[0], id, method, params)
}

func ModernRequestVersion(version string, id any, method string, params any) []byte {
	fields := map[string]any{}
	if params != nil {
		mustUnmarshal(mustMarshal(params), &fields)
	}
	fields["_meta"] = transport.RequestMeta{
		ProtocolVersion:    version,
		ClientInfo:         &transport.ClientInfo{Name: "test", Version: "0"},
		ClientCapabilities: &transport.ClientCapabilities{},
	}
	request := transport.Request{JSONRPC: "2.0", ID: id, Method: method, Params: mustMarshal(fields)}
	return append(mustMarshal(request), '\n')
}

func mustMarshal(value any) []byte {
	encoded, err := json.Marshal(value)
	if err != nil {
		panic(err)
	}
	return encoded
}

func mustUnmarshal(data []byte, target any) {
	if err := json.Unmarshal(data, target); err != nil {
		panic(err)
	}
}
