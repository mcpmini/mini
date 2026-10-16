package server

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"strings"

	"github.com/mcpmini/mini/internal/config"
	"github.com/mcpmini/mini/internal/projection"
	"github.com/mcpmini/mini/internal/response"
)

// upstreamResult is a tools/call result sent to the client exactly as built, not wrapped as text.
type upstreamResult json.RawMessage

func (r upstreamResult) MarshalJSON() ([]byte, error) { return []byte(r), nil }

type proxyResultParams struct {
	Raw     json.RawMessage
	ProjCfg *config.ProjectionConfig
}

type builtProxyResult struct {
	Raw json.RawMessage
	// NoticeBlock is the index of the appended notice block in Raw's content; -1 when absent.
	NoticeBlock int
}

func (s *Server) buildProxyResult(p proxyResultParams) (builtProxyResult, error) {
	unchanged := builtProxyResult{Raw: p.Raw, NoticeBlock: -1}
	doc, ok := parseResultDoc(p.Raw)
	if !ok {
		return unchanged, nil
	}
	units := doc.collectUnits()
	for i := range units {
		units[i].project(p.ProjCfg, s.projDefaults)
	}
	carrier := pickCarrier(units, doc.hasStructured)
	notice, err := s.proxyNotice(p.Raw, units, carrier)
	if err != nil {
		return builtProxyResult{}, err
	}
	if notice == nil && !anyChanged(units) {
		return unchanged, nil
	}
	return doc.assemble(units, carrier, notice)
}

func (s *Server) proxyNotice(
	raw json.RawMessage,
	units []projectUnit,
	carrier *projectUnit,
) (*response.ProjectionMeta, error) {
	excluded, truncated := reportedPaths(units)
	if len(excluded) == 0 && len(truncated) == 0 {
		return nil, nil //nolint:nilnil // No notice is a normal outcome, not an error.
	}
	key, err := s.store.WriteRaw(raw)
	if err != nil {
		return nil, fmt.Errorf("store response: %w", err)
	}
	meta := &response.ProjectionMeta{Msg: response.ProxyMsg, File: key, Excluded: excluded, Truncated: truncated}
	if carrier != nil {
		meta.Passthrough = carrier.result.Passthrough
	}
	return meta, nil
}

func reportedPaths(units []projectUnit) (excluded []string, truncated []projection.Truncation) {
	for _, u := range units {
		for _, path := range u.result.ExcludedKeys {
			excluded = append(excluded, u.prefix+" | "+path)
		}
		for _, t := range u.result.Truncated {
			t.JQPath = u.prefix + " | " + t.JQPath
			truncated = append(truncated, t)
		}
	}
	return excluded, truncated
}

func anyChanged(units []projectUnit) bool {
	for _, u := range units {
		if u.changed {
			return true
		}
	}
	return false
}

func (d *resultDoc) assemble(
	units []projectUnit,
	carrier *projectUnit,
	notice *response.ProjectionMeta,
) (builtProxyResult, error) {
	if carrier != nil && notice != nil {
		carrier.carry(notice)
	}
	for i := range units {
		if err := d.applyUnit(&units[i]); err != nil {
			return builtProxyResult{}, err
		}
	}
	built := builtProxyResult{NoticeBlock: -1}
	if notice != nil && !d.noticeCarried(carrier) {
		if err := d.appendNoticeBlock(notice); err != nil {
			return builtProxyResult{}, err
		}
		built.NoticeBlock = len(d.result.Content) - 1
	}
	raw, err := d.encode()
	built.Raw = raw
	return built, err
}

func (d *resultDoc) noticeCarried(carrier *projectUnit) bool {
	return carrier != nil && (carrier.kind != unitStructured || len(d.mirrors) > 0)
}

func (d *resultDoc) appendNoticeBlock(notice *response.ProjectionMeta) error {
	body, err := marshalCompact(noticeBlockBody{Mini: notice})
	if err != nil {
		return err
	}
	d.result.Content = append(d.result.Content, newTextBlock("\n"+string(body)))
	return nil
}

type noticeBlockBody struct {
	Mini *response.ProjectionMeta `json:"__mini"`
}

func (d *resultDoc) encode() (json.RawMessage, error) {
	return marshalCompact(d.result)
}

func marshalCompact(v any) (json.RawMessage, error) {
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(v); err != nil {
		return nil, err
	}
	return bytes.TrimRight(buf.Bytes(), "\n"), nil
}

func decodeJSONValue(b []byte) (any, bool) {
	dec := json.NewDecoder(bytes.NewReader(b))
	dec.UseNumber()
	var v any
	if err := dec.Decode(&v); err != nil {
		return nil, false
	}
	if _, err := dec.Token(); err != io.EOF {
		return nil, false
	}
	return v, true
}

func isMiniFreeObject(v any) bool {
	m, ok := v.(map[string]any)
	if !ok {
		return false
	}
	_, hasMini := m["__mini"]
	return !hasMini
}

func trimmedJSONValue(text string) (any, bool) {
	return decodeJSONValue([]byte(strings.TrimSpace(text)))
}
