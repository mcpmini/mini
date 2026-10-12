package toon

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
)

// FromJSON decodes raw JSON into a Value, preserving object key order as
// encountered in the document. Duplicate object keys are resolved by last
// occurrence winning while the first occurrence's position is kept. Numbers
// are canonicalized per spec §2 via json.Decoder's UseNumber, so integers
// beyond float64 precision survive digit-exact.
// https://github.com/toon-format/spec/blob/62f16b369408180f1faf1cba7da1b46d1f336f12/SPEC.md?plain=1#L192
func FromJSON(raw json.RawMessage) (Value, error) {
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.UseNumber()
	v, err := decodeValue(dec)
	if err != nil {
		return Value{}, err
	}
	if _, err := dec.Token(); err != io.EOF {
		if err == nil {
			return Value{}, errors.New("toon: trailing data after JSON value")
		}
		return Value{}, err
	}
	return v, nil
}

func consumeClosingToken(dec *json.Decoder) error {
	_, err := dec.Token()
	return err
}

func decodeValue(dec *json.Decoder) (Value, error) {
	tok, err := dec.Token()
	if err != nil {
		return Value{}, err
	}
	return valueFromToken(dec, tok)
}

func valueFromToken(dec *json.Decoder, tok json.Token) (Value, error) {
	switch t := tok.(type) {
	case json.Delim:
		return valueFromDelim(dec, t)
	case nil:
		return Value{payload: nullValue{}}, nil
	case bool:
		return Value{payload: boolValue(t)}, nil
	case json.Number:
		return valueFromNumber(t)
	case string:
		return Value{payload: stringValue(t)}, nil
	default:
		return Value{}, fmt.Errorf("toon: unexpected JSON token type %T", tok)
	}
}

func valueFromDelim(dec *json.Decoder, d json.Delim) (Value, error) {
	switch d {
	case '{':
		return decodeObject(dec)
	case '[':
		return decodeArray(dec)
	default:
		return Value{}, fmt.Errorf("toon: unexpected JSON delimiter %q", d)
	}
}

func valueFromNumber(n json.Number) (Value, error) {
	num, err := canonicalizeNumber(n.String())
	if err != nil {
		return Value{}, err
	}
	return Value{payload: numberValue(num)}, nil
}

func decodeObject(dec *json.Decoder) (Value, error) {
	var fields objectValue
	keyIndex := make(map[string]int)
	for dec.More() {
		keyTok, err := dec.Token()
		if err != nil {
			return Value{}, err
		}
		key, ok := keyTok.(string)
		if !ok {
			return Value{}, fmt.Errorf("toon: object key is not a string: %v", keyTok)
		}
		val, err := decodeValue(dec)
		if err != nil {
			return Value{}, err
		}
		if idx, dup := keyIndex[key]; dup {
			fields[idx].Val = val
		} else {
			keyIndex[key] = len(fields)
			fields = append(fields, Field{Key: key, Val: val})
		}
	}
	if err := consumeClosingToken(dec); err != nil {
		return Value{}, err
	}
	return Value{payload: fields}, nil
}

func decodeArray(dec *json.Decoder) (Value, error) {
	var items arrayValue
	for dec.More() {
		item, err := decodeValue(dec)
		if err != nil {
			return Value{}, err
		}
		items = append(items, item)
	}
	if err := consumeClosingToken(dec); err != nil {
		return Value{}, err
	}
	return Value{payload: items}, nil
}
