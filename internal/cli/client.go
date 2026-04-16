// internal/cli/client.go
package cli

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
)

// Client wraps *http.Client with a base URL and Bearer token.
type Client struct {
	base  string
	token string
	http  *http.Client
}

// NewClient returns a Client for the given server URL and token.
// Pass token="" for unauthenticated requests.
func NewClient(serverURL, token string) *Client {
	return &Client{base: serverURL, token: token, http: &http.Client{}}
}

// do sends a JSON request and decodes the response into out (may be nil).
// Returns an error for non-2xx responses using the server's "error" field when available.
func (c *Client) do(ctx context.Context, method, path string, body, out any) error {
	var bodyBytes []byte
	if body != nil {
		var err error
		bodyBytes, err = json.Marshal(body)
		if err != nil {
			return fmt.Errorf("marshal request: %w", err)
		}
	}

	req, err := http.NewRequestWithContext(ctx, method, c.base+path, bytes.NewReader(bodyBytes))
	if err != nil {
		return err
	}
	if c.token != "" {
		req.Header.Set("Authorization", "Bearer "+c.token)
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}

	resp, err := c.http.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 400 {
		var errBody struct {
			Error string `json:"error"`
		}
		if decodeErr := json.NewDecoder(resp.Body).Decode(&errBody); decodeErr == nil && errBody.Error != "" {
			return fmt.Errorf("%s", errBody.Error)
		}
		if resp.StatusCode >= 500 {
			return fmt.Errorf("server error (%d) — try again", resp.StatusCode)
		}
		return fmt.Errorf("request failed (%d)", resp.StatusCode)
	}

	if out != nil {
		return json.NewDecoder(resp.Body).Decode(out)
	}
	return nil
}

// SSEEvent is a parsed Server-Sent Event frame.
type SSEEvent struct {
	Type string
	Data string
}

// StreamEvents opens an SSE connection to path and calls handler for each complete event.
// handler returns false to stop streaming cleanly. Returns nil when the handler stops
// or the server closes the connection; returns an error on network/HTTP failure.
func (c *Client) StreamEvents(ctx context.Context, path string, handler func(SSEEvent) bool) error {
	req, err := http.NewRequestWithContext(ctx, "GET", c.base+path, nil)
	if err != nil {
		return err
	}
	if c.token != "" {
		req.Header.Set("Authorization", "Bearer "+c.token)
	}
	req.Header.Set("Accept", "text/event-stream")

	resp, err := c.http.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("server error (%d)", resp.StatusCode)
	}

	scanner := bufio.NewScanner(resp.Body)
	var eventType string
	var dataLines []string

	for scanner.Scan() {
		line := scanner.Text()
		switch {
		case strings.HasPrefix(line, "event: "):
			eventType = strings.TrimPrefix(line, "event: ")
		case strings.HasPrefix(line, "data: "):
			dataLines = append(dataLines, strings.TrimPrefix(line, "data: "))
		case line == "" && eventType != "":
			if !handler(SSEEvent{Type: eventType, Data: strings.Join(dataLines, "\n")}) {
				return nil
			}
			eventType = ""
			dataLines = dataLines[:0]
		}
	}
	return scanner.Err()
}
