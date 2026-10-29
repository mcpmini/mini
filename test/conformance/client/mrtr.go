package main

import (
	"encoding/json"
	"fmt"

	"github.com/mcpmini/mini/internal/transport"
)

const (
	mrtrScenario  = "sep-2322-client-request-state"
	maxMRTRRounds = 4
)

type inputRequest struct {
	Method string          `json:"method"`
	Params json.RawMessage `json:"params"`
}

type elicitationParams struct {
	RequestedSchema objectSchema `json:"requestedSchema"`
}

type elicitationResult struct {
	Action  string         `json:"action"`
	Content map[string]any `json:"content"`
}

type samplingResult struct {
	Role    string                `json:"role"`
	Content transport.ContentItem `json:"content"`
	Model   string                `json:"model"`
}

type rootsResult struct {
	Roots []struct{} `json:"roots"`
}

func runMRTR(d driver) error {
	if err := completeRounds(d, "test_mrtr_echo_state", "test_mrtr_unrelated"); err != nil {
		return err
	}
	if err := completeRounds(d, "test_mrtr_no_state", ""); err != nil {
		return err
	}
	_, err := d.callTool(bareCall("test_mrtr_no_result_type"))
	return err
}

func completeRounds(d driver, name, unrelatedTool string) error {
	result, err := d.callTool(bareCall(name))
	for round := 0; err == nil && result.ResultType == transport.ResultInputRequired; round++ {
		if round == maxMRTRRounds {
			return fmt.Errorf("%s: still input_required after %d rounds", name, maxMRTRRounds)
		}
		// The suite checks that this call carries none of the pending tool's inputResponses or requestState.
		if unrelatedTool != "" {
			if _, unrelatedErr := d.callTool(bareCall(unrelatedTool)); unrelatedErr != nil {
				return unrelatedErr
			}
			unrelatedTool = ""
		}
		next, answerErr := nextRound(result)
		if answerErr != nil {
			return answerErr
		}
		result, err = d.callTool(toolCall{Name: name, Arguments: map[string]any{}, Round: next})
	}
	return err
}

func bareCall(name string) toolCall { return toolCall{Name: name, Arguments: map[string]any{}} }

func nextRound(previous toolResult) (*transport.ToolCallMRTR, error) {
	answers, err := inputResponses(previous.InputRequests)
	if err != nil {
		return nil, err
	}
	encoded, err := json.Marshal(answers)
	if err != nil {
		return nil, err
	}
	return &transport.ToolCallMRTR{InputResponses: encoded, RequestState: previous.RequestState}, nil
}

func inputResponses(requests map[string]inputRequest) (map[string]any, error) {
	answers := make(map[string]any)
	for key, request := range requests {
		switch request.Method {
		case "elicitation/create":
			var params elicitationParams
			if err := json.Unmarshal(request.Params, &params); err != nil {
				return nil, fmt.Errorf("decode elicitation request %s: %w", key, err)
			}
			answers[key] = elicitationAnswer(params.RequestedSchema)
		case "sampling/createMessage":
			answers[key] = samplingResult{
				Role: "assistant", Content: transport.ContentItem{Type: "text", Text: "ok"}, Model: "harness",
			}
		case "roots/list":
			answers[key] = rootsResult{Roots: []struct{}{}}
		}
	}
	return answers, nil
}

func elicitationAnswer(schema objectSchema) elicitationResult {
	content := make(map[string]any)
	for key, property := range schema.Properties {
		content[key] = elicitationValue(property)
	}
	return elicitationResult{Action: "accept", Content: content}
}

var elicitationFormatValues = map[string]string{
	"email": "a@example.com", "uri": "https://example.com", "date": "2000-01-01",
	"date-time": "2000-01-01T00:00:00Z",
}

func elicitationValue(property schemaProperty) any {
	if len(property.Enum) > 0 {
		return property.Enum[0]
	}
	switch property.Type {
	case "boolean":
		return true
	case "integer", "number":
		if property.Minimum != nil {
			return *property.Minimum
		}
		return 1
	}
	if value, ok := elicitationFormatValues[property.Format]; ok {
		return value
	}
	return "Ada"
}
