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
// See https://github.com/toon-format/spec/blob/f55b93ac489f297ff597d95e4c19ae84675eaeb7/SPEC.md#2-data-model
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
		return Value{Kind: KindNull}, nil
	case bool:
		return Value{Kind: KindBool, Bool: t}, nil
	case json.Number:
		return valueFromNumber(t)
	case string:
		return Value{Kind: KindString, Str: t}, nil
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
	return Value{Kind: KindNumber, Num: num}, nil
}

func decodeObject(dec *json.Decoder) (Value, error) {
	v := Value{Kind: KindObject}
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
			v.Fields[idx].Val = val
		} else {
			keyIndex[key] = len(v.Fields)
			v.Fields = append(v.Fields, Field{Key: key, Val: val})
		}
	}
	if err := consumeClosingToken(dec); err != nil {
		return Value{}, err
	}
	return v, nil
}

func decodeArray(dec *json.Decoder) (Value, error) {
	v := Value{Kind: KindArray}
	for dec.More() {
		item, err := decodeValue(dec)
		if err != nil {
			return Value{}, err
		}
		v.Items = append(v.Items, item)
	}
	if err := consumeClosingToken(dec); err != nil {
		return Value{}, err
	}
	return v, nil
}
