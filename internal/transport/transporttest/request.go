package transporttest

import (
	"encoding/json"

	"github.com/mcpmini/mini/internal/transport"
)

// ModernRequest encodes one newline-terminated 2026-07-28 request whose params carry the
// per-request metadata alongside the given typed params. Metadata keys the params already set win.
func ModernRequest(id any, method string, params any) []byte {
	return ModernRequestVersion(ModernRequestVersionParams{
		Version: transport.ModernVersions[0],
		ID:      id,
		Method:  method,
		Params:  params,
	})
}

type ModernRequestVersionParams struct {
	Version string
	ID      any
	Method  string
	Params  any
}

func ModernRequestVersion(p ModernRequestVersionParams) []byte {
	fields := map[string]json.RawMessage{}
	if p.Params != nil {
		mustUnmarshal(mustMarshal(p.Params), &fields)
	}
	meta := map[string]json.RawMessage{}
	mustUnmarshal(mustMarshal(transport.RequestMeta{
		ProtocolVersion:    p.Version,
		ClientInfo:         &transport.ClientInfo{Name: "test", Version: "0"},
		ClientCapabilities: &transport.ClientCapabilities{},
	}), &meta)
	if raw, ok := fields["_meta"]; ok {
		mustUnmarshal(raw, &meta)
	}
	fields["_meta"] = mustMarshal(meta)
	request := transport.Request{
		JsonRPC: transport.JsonRPCVersion,
		ID:      p.ID,
		Method:  p.Method,
		Params:  mustMarshal(fields),
	}
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
