package transport

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"io"
)

const maxSSEMessageBytes = 64 << 20
const maxSSELineBytes = maxSSEMessageBytes + len("data: ") + 2

var utf8BOM = []byte{0xef, 0xbb, 0xbf}

var errSSEMessageTooLarge = errors.New("sse message exceeds 64 MiB")

type sseLimits struct {
	messageBytes int
	lineBytes    int
}

var defaultSSELimits = sseLimits{
	messageBytes: maxSSEMessageBytes,
	lineBytes:    maxSSELineBytes,
}

func splitHTTPMessages(body []byte) ([]json.RawMessage, error) {
	return splitHTTPMessagesWithLimits(body, defaultSSELimits)
}

func splitHTTPMessagesWithLimits(body []byte, limits sseLimits) ([]json.RawMessage, error) {
	trimmed := bytes.TrimSpace(body)
	if len(trimmed) == 0 {
		return nil, nil
	}
	isSSE, err := looksLikeSSE(body, limits)
	if err != nil {
		return nil, err
	}
	if !isSSE {
		return []json.RawMessage{bytes.Clone(trimmed)}, nil
	}
	var messages []json.RawMessage
	err = scanSSEMessagesWithLimits(bytes.NewReader(body), limits, func(message json.RawMessage) error {
		messages = append(messages, message)
		return nil
	})
	return messages, err
}

func looksLikeSSE(body []byte, limits sseLimits) (bool, error) {
	reader := newSSELineReader(bytes.NewReader(body), limits)
	for {
		line, err := reader.readLine()
		if errors.Is(err, io.EOF) {
			return false, nil
		}
		if err != nil {
			return false, err
		}
		if shouldSkipSSELine(line) {
			continue
		}
		field, _, ok := parseSSEDataLine(line)
		if ok && bytes.Equal(field, []byte("data")) {
			return true, nil
		}
	}
}

func shouldSkipSSELine(line []byte) bool {
	return len(line) == 0 || line[0] == ':'
}

func ScanSSEMessages(body io.Reader, handle func(json.RawMessage) error) error {
	return scanSSEMessagesWithLimits(body, defaultSSELimits, handle)
}

func scanSSEMessagesWithLimits(body io.Reader, limits sseLimits, handle func(json.RawMessage) error) error {
	scanner := sseScanner{
		reader: newSSELineReader(body, limits),
		event:  make([]byte, 0, 64<<10),
		limits: limits,
		handle: handle,
	}
	return scanner.scan()
}

type sseScanner struct {
	reader   *sseLineReader
	event    []byte
	seenData bool
	limits   sseLimits
	handle   func(json.RawMessage) error
}

func (s *sseScanner) scan() error {
	for {
		line, err := s.reader.readLine()
		if errors.Is(err, io.EOF) {
			return nil
		}
		if err != nil {
			return err
		}
		if err := s.processLine(line); err != nil {
			return err
		}
	}
}

func (s *sseScanner) processLine(line []byte) error {
	if len(line) == 0 {
		return s.flush()
	}
	field, value, ok := parseSSEDataLine(line)
	if !ok || !bytes.Equal(field, []byte("data")) {
		return nil
	}
	if s.seenData {
		if err := appendBounded(&s.event, []byte{'\n'}, s.limits.messageBytes); err != nil {
			return err
		}
	}
	s.seenData = true
	return appendBounded(&s.event, value, s.limits.messageBytes)
}

func (s *sseScanner) flush() error {
	if !s.seenData {
		return nil
	}
	s.seenData = false
	raw := json.RawMessage(bytes.Clone(s.event))
	s.event = s.event[:0]
	if !json.Valid(raw) {
		return nil
	}
	return s.handle(raw)
}

type sseLineReader struct {
	reader *bufio.Reader
	limits sseLimits
}

func newSSELineReader(body io.Reader, limits sseLimits) *sseLineReader {
	reader := bufio.NewReaderSize(body, 64<<10)
	if prefix, _ := reader.Peek(len(utf8BOM)); bytes.Equal(prefix, utf8BOM) {
		reader.Discard(len(utf8BOM)) //nolint:errcheck
	}
	return &sseLineReader{reader: reader, limits: limits}
}

func (r *sseLineReader) readLine() ([]byte, error) {
	var line []byte
	for {
		b, err := r.reader.ReadByte()
		if errors.Is(err, io.EOF) {
			if len(line) == 0 {
				return nil, io.EOF
			}
			return line, nil
		}
		if err != nil {
			return nil, err
		}
		done, err := r.handleLineByte(&line, b)
		if done || err != nil {
			return line, err
		}
	}
}

func (r *sseLineReader) consumeOptionalLF() {
	next, err := r.reader.Peek(1)
	if err == nil && next[0] == '\n' {
		r.reader.ReadByte() //nolint:errcheck
	}
}

func (r *sseLineReader) handleLineByte(line *[]byte, b byte) (bool, error) {
	switch b {
	case '\n':
		return true, nil
	case '\r':
		r.consumeOptionalLF()
		return true, nil
	default:
		*line = append(*line, b)
		return false, r.checkLineLength(*line)
	}
}

func parseSSEDataLine(line []byte) ([]byte, []byte, bool) {
	if len(line) == 0 || line[0] == ':' {
		return nil, nil, false
	}
	field, value, found := bytes.Cut(line, []byte{':'})
	if !found {
		return field, nil, true
	}
	if len(value) > 0 && value[0] == ' ' {
		value = value[1:]
	}
	return field, value, true
}

func appendBounded(dst *[]byte, value []byte, maxBytes int) error {
	if len(value) > maxBytes-len(*dst) {
		return errSSEMessageTooLarge
	}
	*dst = append(*dst, value...)
	return nil
}

func (r *sseLineReader) checkLineLength(line []byte) error {
	if len(line) > r.limits.lineBytes {
		return errSSEMessageTooLarge
	}
	return nil
}
