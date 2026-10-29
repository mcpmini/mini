package transport

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
)

type HTTPStatusError struct {
	Method          string
	StatusCode      int
	Body            string
	RPC             *RPCError
	WWWAuthenticate string
}

func (e *HTTPStatusError) Error() string {
	return fmt.Sprintf("http %s: status %d: %s", e.Method, e.StatusCode, e.Body)
}

func (c *HTTPConnection) parsePostBody(body []byte, requestID any) (json.RawMessage, error) {
	messages, err := splitHTTPMessages(body)
	if err != nil {
		return nil, err
	}
	return c.findPostResponse(messages, requestID)
}

func (c *HTTPConnection) findPostResponse(messages []json.RawMessage, requestID any) (json.RawMessage, error) {
	var responses unmatchedPostResponses
	for _, message := range messages {
		parsed, err := c.parseHTTPMessage(message, requestID)
		if err != nil {
			return nil, err
		}
		if !parsed.response {
			continue
		}
		responses.add(parsed)
		if parsed.matched && parsed.rpcError != nil {
			return nil, parsed.rpcError
		}
		if parsed.matched {
			return parsed.result, nil
		}
	}
	return nil, responses.notFoundError(requestID)
}

type unmatchedPostResponses struct {
	count     int
	soleError *RPCError
}

func (r *unmatchedPostResponses) add(message parsedHTTPMessage) {
	r.count++
	r.soleError = message.rpcError
}

func (r unmatchedPostResponses) notFoundError(requestID any) error {
	if r.count == 1 && r.soleError != nil {
		return r.soleError
	}
	return unusableResponseError{fmt.Errorf("response for request id %v not found", requestID)}
}

// unusableResponseError marks a reply body that answers nothing; the discover probe reads it as a legacy server.
type unusableResponseError struct{ err error }

func (e unusableResponseError) Error() string { return e.err.Error() }

func (e unusableResponseError) Unwrap() error { return e.err }

type parsedHTTPMessage struct {
	result   json.RawMessage
	rpcError *RPCError
	response bool
	matched  bool
}

func (c *HTTPConnection) parseHTTPMessage(message []byte, requestID any) (parsedHTTPMessage, error) {
	var rpc struct {
		ID     json.RawMessage `json:"id"`
		Method string          `json:"method"`
		Params json.RawMessage `json:"params"`
		Result json.RawMessage `json:"result"`
		Error  *RPCError       `json:"error"`
	}
	if err := json.Unmarshal(message, &rpc); err != nil {
		err = fmt.Errorf("parse response: %w: %s", err, message[:min(200, len(message))])
		return parsedHTTPMessage{}, unusableResponseError{err}
	}
	if len(rpc.ID) == 0 && rpc.Method != "" {
		c.handleNotification(Notification{JsonRPC: JsonRPCVersion, Method: rpc.Method, Params: rpc.Params})
		return parsedHTTPMessage{}, nil
	}
	return parsedHTTPMessage{
		result: rpc.Result, rpcError: rpc.Error,
		response: true, matched: sameJSONID(rpc.ID, requestID),
	}, nil
}

func sameJSONID(raw json.RawMessage, id any) bool {
	want, err := json.Marshal(id)
	return err == nil && bytes.Equal(bytes.TrimSpace(raw), want)
}

func nonRetryableHTTPError(resp *http.Response, method string, errBody []byte) error {
	if resp.StatusCode == http.StatusUnauthorized {
		return fmt.Errorf("http %s: status %d: %w", method, resp.StatusCode,
			&UnauthorizedError{WWWAuthenticate: wwwAuthenticate(resp.Header)})
	}
	body := string(errBody)
	return &HTTPStatusError{
		Method: method, StatusCode: resp.StatusCode, Body: body, RPC: parseHTTPStatusRPC(errBody),
		WWWAuthenticate: wwwAuthenticate(resp.Header),
	}
}

func parseHTTPStatusRPC(body []byte) *RPCError {
	response, ok := decodeHTTPStatusRPC(body)
	if !ok {
		return nil
	}
	return response.Error
}

type httpStatusRPCResponse struct {
	JsonRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id"`
	Error   *RPCError       `json:"error"`
}

func decodeHTTPStatusRPC(body []byte) (httpStatusRPCResponse, bool) {
	var response httpStatusRPCResponse
	err := json.Unmarshal(body, &response)
	valid := err == nil && response.JsonRPC == JsonRPCVersion && len(response.ID) > 0 && response.Error != nil
	return response, valid
}

func wwwAuthenticate(header http.Header) string {
	return strings.Join(header.Values("WWW-Authenticate"), ", ")
}
