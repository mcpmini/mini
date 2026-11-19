package toolresult

import (
	"encoding/json"
	"log/slog"
	"strings"

	"github.com/mcpmini/mini/internal/toon"
)

// Toon re-encodes the result's JSON text blocks as TOON. A block that fails to encode stays JSON;
// logger records why.
func (s Shaped) Toon(logger *slog.Logger) (json.RawMessage, error) {
	d, ok := parseResultDoc(s.Raw)
	if !ok || len(d.result.Content) == 0 {
		return s.Raw, nil
	}
	for i := range d.result.Content {
		// mini's own notice block stays JSON; a notice carried inside an object is encoded with it.
		if i != s.noticeBlock {
			toonifyBlock(logger, &d.result.Content[i])
		}
	}
	return d.encode()
}

func toonifyBlock(logger *slog.Logger, block *contentBlock) {
	if block.Type != "text" || block.Text == nil {
		return
	}
	trimmed := json.RawMessage(strings.TrimSpace(*block.Text))
	if !json.Valid(trimmed) {
		return
	}
	if text, encoded := encodeToonText(logger, trimmed); encoded {
		block.setText(text)
	}
}

func encodeToonText(logger *slog.Logger, raw json.RawMessage) (string, bool) {
	text, err := encodeToonJSON(raw)
	if err != nil {
		logger.Warn("toon encode failed", "err", err)
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
