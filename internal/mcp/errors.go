package mcp

import (
	"errors"

	"relay/internal/relayclient"
)

// ToolError is the structured payload returned to MCP clients on tool failure.
type ToolError struct {
	Code    string `json:"code"`
	Message string `json:"message"`
	Hint    string `json:"hint,omitempty"`
}

func (e *ToolError) Error() string { return e.Code + ": " + e.Message }

// MapError translates an HTTP/network error from relayclient into a structured
// ToolError. Returns nil if err is nil.
func MapError(err error) *ToolError {
	if err == nil {
		return nil
	}
	var re *relayclient.ResponseError
	if errors.As(err, &re) {
		switch {
		case re.StatusCode == 401:
			return &ToolError{Code: "auth_expired", Message: re.Message,
				Hint: "run `relay login` to refresh credentials"}
		case re.StatusCode == 403:
			return &ToolError{Code: "forbidden", Message: re.Message,
				Hint: "this action requires an admin token"}
		case re.StatusCode == 404:
			return &ToolError{Code: "not_found", Message: re.Message,
				Hint: "check the id; the entity may have been deleted"}
		case re.StatusCode == 400:
			return &ToolError{Code: "validation", Message: re.Message,
				Hint: "fix the input and try again"}
		case re.StatusCode == 409:
			return &ToolError{Code: "conflict", Message: re.Message,
				Hint: "another change conflicts with this request"}
		case re.StatusCode == 429:
			return &ToolError{Code: "rate_limited", Message: re.Message,
				Hint: "rate limit hit; wait and retry"}
		case re.StatusCode >= 500:
			return &ToolError{Code: "server_error", Message: re.Message,
				Hint: "server error; check `relay-server` logs"}
		}
		return &ToolError{Code: "server_error", Message: re.Message}
	}
	return &ToolError{Code: "network", Message: err.Error(),
		Hint: "could not reach server; check RELAY_URL and network connectivity"}
}
