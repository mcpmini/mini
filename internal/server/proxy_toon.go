package server

import (
	"encoding/json"
	"strings"

	"github.com/mcpmini/mini/internal/toon"
)

type toonParams struct {
	Server string
	Tool   string
	Built  builtProxyResult
}

func (s *Server) toonifyProxyResult(p toonParams) (json.RawMessage, error) {
	d, ok := parseResultDoc(p.Built.Raw)
	if !ok || len(d.result.Content) == 0 {
		return p.Built.Raw, nil
	}
	for i := range d.result.Content {
		// mini's own notice block stays JSON; a notice carried inside an object is encoded with it.
		if i != p.Built.NoticeBlock {
			s.toonifyBlock(p, &d.result.Content[i])
		}
	}
	return d.encode()
}

func (s *Server) toonifyBlock(p toonParams, block *contentBlock) {
	if block.Type != "text" || block.Text == nil {
		return
	}
	trimmed := json.RawMessage(strings.TrimSpace(*block.Text))
	if !json.Valid(trimmed) {
		return
	}
	if text, encoded := s.encodeToonText(p, trimmed); encoded {
		block.setText(text)
	}
}

func (s *Server) encodeToonText(p toonParams, raw json.RawMessage) (string, bool) {
	text, err := encodeToonJSON(raw)
	if err != nil {
		s.logger.Warn("toon encode failed", "server", p.Server, "tool", p.Tool, "err", err)
		return "", false
	}
	// TOON renders an empty object as "", which reads as no output at all.
	return text, text != ""
}

// encodeToonJSON keeps the key order of the text it is given; decoding into a map would sort it.
func encodeToonJSON(raw json.RawMessage) (string, error) {
	v, err := toon.FromJSON(raw)
	if err != nil {
		return "", err
	}
	return toon.Encode(v)
}
