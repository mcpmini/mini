package transport

import (
	"encoding/json"
	"fmt"
)

type ResultType string

const (
	ResultComplete      ResultType = "complete"
	ResultInputRequired ResultType = "input_required"
)

func ResultTypeOf(raw json.RawMessage) (ResultType, error) {
	var result struct {
		ResultType json.RawMessage `json:"resultType"`
	}
	if err := json.Unmarshal(raw, &result); err != nil {
		return "", fmt.Errorf("upstream returned non-standard response: %w", err)
	}
	if len(result.ResultType) == 0 {
		return ResultComplete, nil
	}
	var value any
	if err := json.Unmarshal(result.ResultType, &value); err != nil {
		return "", fmt.Errorf("upstream returned unknown resultType %q", string(result.ResultType))
	}
	resultType, ok := value.(string)
	if ok && (resultType == string(ResultComplete) || resultType == string(ResultInputRequired)) {
		return ResultType(resultType), nil
	}
	return "", fmt.Errorf("upstream returned unknown resultType %q", fmt.Sprint(value))
}
