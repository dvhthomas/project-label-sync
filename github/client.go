package github

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"time"

	applog "github.com/dvhthomas/project-label-sync/internal/log"
)

// Client wraps GitHub GraphQL API calls with retry and rate-limit handling.
type Client struct {
	Token      string
	HTTPClient *http.Client
}

// NewClient creates a Client with the given PAT.
func NewClient(token string) *Client {
	return &Client{
		Token:      token,
		HTTPClient: &http.Client{Timeout: 30 * time.Second},
	}
}

// graphqlRequest is the JSON body sent to the GraphQL endpoint.
type graphqlRequest struct {
	Query     string         `json:"query"`
	Variables map[string]any `json:"variables,omitempty"`
}

// graphqlResponse is the top-level response envelope.
type graphqlResponse struct {
	Data   json.RawMessage `json:"data"`
	Errors []struct {
		Message string `json:"message"`
		Type    string `json:"type"`
	} `json:"errors"`
}

// GraphQL executes a GraphQL query/mutation and unmarshals the data field
// into dest. It retries on transient failures with exponential backoff.
func (c *Client) GraphQL(ctx context.Context, name string, query string, variables map[string]any, dest any) error {
	return withRetry(ctx, name, 3, func() error {
		body, err := json.Marshal(graphqlRequest{Query: query, Variables: variables})
		if err != nil {
			return fmt.Errorf("marshal request: %w", err)
		}

		req, err := http.NewRequestWithContext(ctx, http.MethodPost, "https://api.github.com/graphql", bytes.NewReader(body))
		if err != nil {
			return fmt.Errorf("create request: %w", err)
		}
		req.Header.Set("Authorization", "Bearer "+c.Token)
		req.Header.Set("Content-Type", "application/json")

		resp, err := c.HTTPClient.Do(req)
		if err != nil {
			return &retryableError{err: err}
		}
		defer resp.Body.Close()

		respBody, err := io.ReadAll(resp.Body)
		if err != nil {
			return &retryableError{err: fmt.Errorf("read response: %w", err)}
		}

		// Check rate limits
		if resp.StatusCode == http.StatusForbidden || resp.StatusCode == 429 {
			retryAfter := parseRetryAfter(resp.Header)
			return &retryableError{
				err:        fmt.Errorf("rate limited (HTTP %d): %s", resp.StatusCode, string(respBody)),
				retryAfter: retryAfter,
			}
		}

		if resp.StatusCode >= 500 {
			return &retryableError{err: fmt.Errorf("server error (HTTP %d): %s", resp.StatusCode, string(respBody))}
		}

		if resp.StatusCode != http.StatusOK {
			return fmt.Errorf("HTTP %d: %s", resp.StatusCode, string(respBody))
		}

		var gqlResp graphqlResponse
		if err := json.Unmarshal(respBody, &gqlResp); err != nil {
			return fmt.Errorf("unmarshal response: %w", err)
		}

		if len(gqlResp.Errors) > 0 {
			msg := gqlResp.Errors[0].Message
			errType := gqlResp.Errors[0].Type
			if errType == "RATE_LIMITED" {
				return &retryableError{err: fmt.Errorf("GraphQL rate limited: %s", msg)}
			}
			return fmt.Errorf("GraphQL error: %s", msg)
		}

		if dest != nil {
			if err := json.Unmarshal(gqlResp.Data, dest); err != nil {
				return fmt.Errorf("unmarshal data: %w", err)
			}
		}
		return nil
	})
}

func parseRetryAfter(h http.Header) time.Duration {
	val := h.Get("Retry-After")
	if val == "" {
		return 0
	}
	secs, err := strconv.Atoi(val)
	if err != nil {
		return 0
	}
	return time.Duration(secs) * time.Second
}

// retryableError signals that the operation can be retried.
type retryableError struct {
	err        error
	retryAfter time.Duration
}

func (e *retryableError) Error() string { return e.err.Error() }
func (e *retryableError) Unwrap() error { return e.err }

// withRetry executes fn up to maxAttempts times with exponential backoff
// for retryable errors.
func withRetry(ctx context.Context, name string, maxAttempts int, fn func() error) error {
	var lastErr error
	for attempt := 1; attempt <= maxAttempts; attempt++ {
		lastErr = fn()
		if lastErr == nil {
			return nil
		}

		re, ok := lastErr.(*retryableError)
		if !ok {
			return lastErr
		}

		if attempt < maxAttempts {
			backoff := time.Duration(attempt*attempt) * time.Second
			if re.retryAfter > backoff {
				backoff = re.retryAfter
			}
			applog.Warn("%s failed (attempt %d/%d): %v, retrying in %v", name, attempt, maxAttempts, lastErr, backoff)
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-time.After(backoff):
			}
		}
	}
	return fmt.Errorf("%s failed after %d attempts: %w", name, maxAttempts, lastErr)
}
