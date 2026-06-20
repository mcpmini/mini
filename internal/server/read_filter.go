package server

import (
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
)

func applyReadFilter(data []byte, filter string) ([]byte, error) {
	if filter == "" || filter == "." {
		return data, nil
	}
	var root any
	if err := json.Unmarshal(data, &root); err != nil {
		return nil, fmt.Errorf("not valid JSON: %w", err)
	}
	segments, err := parseFilterPath(filter)
	if err != nil {
		return nil, err
	}
	cur := root
	for _, seg := range segments {
		cur, err = navigateSegment(cur, seg)
		if err != nil {
			return nil, err
		}
	}
	return json.Marshal(cur)
}

func parseFilterPath(filter string) ([]string, error) {
	if !strings.HasPrefix(filter, ".") {
		return nil, fmt.Errorf("filter must start with '.' (got %q)", filter)
	}
	raw := filter[1:] // drop leading dot
	if raw == "" {
		return nil, nil
	}
	var segs []string
	for _, part := range strings.Split(raw, ".") {
		if part == "" {
			continue
		}
		segs = append(segs, part)
	}
	return segs, nil
}

func navigateSegment(cur any, seg string) (any, error) {
	if strings.HasPrefix(seg, "[") && strings.HasSuffix(seg, "]") {
		return indexArray(cur, seg)
	}
	m, ok := cur.(map[string]any)
	if !ok {
		return nil, fmt.Errorf(".%s: not an object (got %T)", seg, cur)
	}
	v, exists := m[seg]
	if !exists {
		return nil, fmt.Errorf(".%s: key not found", seg)
	}
	return v, nil
}

func indexArray(cur any, seg string) (any, error) {
	idxStr := seg[1 : len(seg)-1]
	idx, err := strconv.Atoi(idxStr)
	if err != nil {
		return nil, fmt.Errorf("%s: invalid array index", seg)
	}
	arr, ok := cur.([]any)
	if !ok {
		return nil, fmt.Errorf("%s: not an array (got %T)", seg, cur)
	}
	if idx < 0 || idx >= len(arr) {
		return nil, fmt.Errorf("%s: index %d out of range (len %d)", seg, idx, len(arr))
	}
	return arr[idx], nil
}
