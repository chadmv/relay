// internal/relayclient/client.go
package relayclient

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"sync"
)

// ResponseError is returned by Client.Do for non-2xx responses.
// Callers can inspect StatusCode to handle specific HTTP errors.
type ResponseError struct {
	StatusCode int
	Message    string
}

func (e *ResponseError) Error() string { return e.Message }

// ErrorIsTransient reports whether an identical request made later could
// plausibly get a different answer. err must be non-nil - it is the error one of
// this package's own calls returned.
//
// It lives here rather than in either caller because two independent poll loops
// need the SAME partition and a second copy of it would drift: internal/mcp's
// relay_wait_for_job decides with it whether to keep polling, and internal/cli's
// `relay logs` decides with it whether the subscribe-time job snapshot is worth
// asking for again or is the command's answer. It is a property of the HTTP
// response, which is this package's subject, and neither loop's own idea.
//
// Anything that is not a ResponseError never reached a handler at all - a dial
// failure, a reset connection, a body that would not decode - so a later request
// can outlive it. Among the answers a handler DID give, the permanent ones are
// the ones that name a fact about the request rather than about the server's
// moment: a malformed id, an expired token, a permission the caller does not
// have, an entity that does not exist, a conflicting change.
//
// Everything else - 429, 5xx, and any status not enumerated - is transient.
// That direction is deliberate: an unrecognised status keeps the caller waiting
// on a server that may yet answer, where the other direction would report a
// permanent failure nobody established.
func ErrorIsTransient(err error) bool {
	var re *ResponseError
	if !errors.As(err, &re) {
		return true
	}
	switch re.StatusCode {
	case http.StatusBadRequest, http.StatusUnauthorized, http.StatusForbidden,
		http.StatusNotFound, http.StatusConflict:
		return false
	}
	return true
}

// Client wraps *http.Client with a base URL and Bearer token.
type Client struct {
	base string
	http *http.Client

	mu    sync.RWMutex
	token string
}

// NewClient returns a Client for the given server URL and token.
// Pass token="" for unauthenticated requests.
func NewClient(serverURL, token string) *Client {
	return &Client{base: strings.TrimRight(serverURL, "/"), token: token, http: &http.Client{}}
}

// BaseURL returns the base server URL this client connects to.
func (c *Client) BaseURL() string { return c.base }

// Token returns the current bearer token.
func (c *Client) Token() string {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.token
}

// SetToken atomically replaces the bearer token used by subsequent requests.
// Safe for concurrent use with Do and StreamEvents.
func (c *Client) SetToken(token string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.token = token
}

// Do sends a JSON request and decodes the response into out (may be nil).
// Returns an error for non-2xx responses using the server's "error" field when available.
func (c *Client) Do(ctx context.Context, method, path string, body, out any) error {
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
	if tok := c.Token(); tok != "" {
		req.Header.Set("Authorization", "Bearer "+tok)
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
			return &ResponseError{StatusCode: resp.StatusCode, Message: errBody.Error}
		}
		if resp.StatusCode >= 500 {
			return &ResponseError{StatusCode: resp.StatusCode, Message: fmt.Sprintf("server error (%d) — try again", resp.StatusCode)}
		}
		return &ResponseError{StatusCode: resp.StatusCode, Message: fmt.Sprintf("request failed (%d)", resp.StatusCode)}
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
// onSubscribed, if non-nil, is called once after the server returns HTTP 200 (the
// subscription is established server-side at that point) and before any event is read;
// if it returns false, StreamEvents returns nil immediately without reading the stream.
// handler returns false to stop streaming cleanly. Returns nil when the handler stops
// or the server closes the connection; returns an error on network/HTTP failure.
func (c *Client) StreamEvents(ctx context.Context, path string, onSubscribed func() bool, handler func(SSEEvent) bool) error {
	req, err := http.NewRequestWithContext(ctx, "GET", c.base+path, nil)
	if err != nil {
		return err
	}
	if tok := c.Token(); tok != "" {
		req.Header.Set("Authorization", "Bearer "+tok)
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

	if onSubscribed != nil && !onSubscribed() {
		return nil
	}

	scanner := bufio.NewScanner(resp.Body)
	// bufio.Scanner's default token limit is 64 KiB, which a single task_log
	// frame can exceed: an agent chunk is up to ~32 KiB raw (os/exec's copy
	// buffer feeding chunkWriter in internal/agent/runner.go) and JSON escaping
	// expands arbitrary bytes by up to ~6x, since each control byte becomes a
	// 6-character \u00XX escape - so a worst-case chunk of binary output reaches
	// ~192 KiB. Without this, StreamEvents fails the whole stream with
	// "bufio.Scanner: token too long" on one oversized log line. Status payloads
	// are tiny, so this never bit before task_log existed. 1 MiB is ~5x that
	// worst case; do not shrink it on the assumption that escaping only doubles.
	scanner.Buffer(make([]byte, 0, 64*1024), 1<<20)
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
