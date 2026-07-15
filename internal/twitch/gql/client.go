package gql

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/Du-vy/TDropFarmer/internal/twitch/profile"
)

const (
	defaultEndpoint = "https://gql.twitch.tv/gql"

	// Retry policy for transient failures (network errors, 429s, 5xx, and
	// upstream "service error" GraphQL responses).
	defaultMaxAttempts = 4
	retryBaseDelay     = 2 * time.Second
	retryMaxDelay      = 30 * time.Second
)

type Client struct {
	ClientID    string
	AccessToken string
	HTTPClient  *http.Client
	Endpoint    string

	// Limiter, when set, paces requests. Copies of the client share the same
	// limiter, so one limiter bounds the whole process.
	Limiter *RateLimiter
	// MaxAttempts caps how many times a request is attempted, including the
	// first try. Zero means the default.
	MaxAttempts int

	// sleep is overridable in tests to avoid real retry delays.
	sleep func(context.Context, time.Duration) error
}

type Request struct {
	OperationName string         `json:"operationName,omitempty"`
	Query         string         `json:"query,omitempty"`
	Variables     map[string]any `json:"variables,omitempty"`
	Extensions    map[string]any `json:"extensions,omitempty"`
}

type Response struct {
	Data   json.RawMessage `json:"data,omitempty"`
	Errors []Error         `json:"errors,omitempty"`
}

type Error struct {
	Message string         `json:"message"`
	Path    []any          `json:"path,omitempty"`
	Extra   map[string]any `json:"extensions,omitempty"`
}

func (e Error) Error() string {
	return e.Message
}

func IsPersistedQueryNotFound(err error) bool {
	var gqlErr Error
	if !errors.As(err, &gqlErr) {
		return false
	}
	if persistedQueryErrorCode(gqlErr.Message) == "persistedquerynotfound" {
		return true
	}
	code, _ := gqlErr.Extra["code"].(string)
	return persistedQueryErrorCode(code) == "persistedquerynotfound"
}

func persistedQueryErrorCode(value string) string {
	value = strings.ToLower(strings.TrimSpace(value))
	value = strings.ReplaceAll(value, "_", "")
	value = strings.ReplaceAll(value, "-", "")
	return strings.ReplaceAll(value, " ", "")
}

func (c Client) Do(ctx context.Context, request Request) (Response, error) {
	if request.Query == "" && request.OperationName == "" && len(request.Extensions) == 0 {
		return Response{}, fmt.Errorf("graphql query, operation name, or extensions are required")
	}
	body, err := json.Marshal(request)
	if err != nil {
		return Response{}, err
	}

	maxAttempts := c.MaxAttempts
	if maxAttempts <= 0 {
		maxAttempts = defaultMaxAttempts
	}
	sleep := c.sleep
	if sleep == nil {
		sleep = sleepContext
	}

	var (
		response  Response
		lastErr   error
		retryable bool
	)
	for attempt := 1; ; attempt++ {
		if err := c.Limiter.Wait(ctx); err != nil {
			return Response{}, err
		}

		var retryAfter time.Duration
		response, retryable, retryAfter, lastErr = c.doOnce(ctx, body)
		if lastErr == nil {
			return response, nil
		}
		if !retryable || attempt >= maxAttempts || ctx.Err() != nil {
			return response, lastErr
		}

		delay := retryBaseDelay << (attempt - 1)
		if delay > retryMaxDelay {
			delay = retryMaxDelay
		}
		if retryAfter > delay {
			delay = retryAfter
		}
		if err := sleep(ctx, delay); err != nil {
			return Response{}, err
		}
	}
}

// doOnce performs a single request attempt. It reports whether the failure is
// transient and worth retrying, along with any server-requested delay.
func (c Client) doOnce(ctx context.Context, body []byte) (Response, bool, time.Duration, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.endpoint(), bytes.NewReader(body))
	if err != nil {
		return Response{}, false, 0, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	if c.ClientID != "" {
		req.Header.Set("Client-Id", c.ClientID)
	}
	if c.AccessToken != "" {
		req.Header.Set("Authorization", "OAuth "+c.AccessToken)
	}
	profile.ApplyMobileApp(req)

	client := c.HTTPClient
	if client == nil {
		client = &http.Client{Timeout: 20 * time.Second}
	}

	resp, err := client.Do(req)
	if err != nil {
		// Network-level failures are transient unless the context is done.
		return Response{}, ctx.Err() == nil, 0, err
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return Response{}, ctx.Err() == nil, 0, err
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		retryable := resp.StatusCode == http.StatusTooManyRequests || resp.StatusCode >= 500
		return Response{}, retryable, retryAfterDelay(resp.Header), fmt.Errorf("graphql request failed: status %d", resp.StatusCode)
	}

	var gqlResponse Response
	if err := json.Unmarshal(respBody, &gqlResponse); err != nil {
		return Response{}, false, 0, fmt.Errorf("decode graphql response: %w", err)
	}
	if len(gqlResponse.Errors) > 0 {
		return gqlResponse, isTransientGQLFailure(gqlResponse.Errors), 0, gqlResponse.Errors[0]
	}
	return gqlResponse, false, 0, nil
}

// isTransientGQLFailure reports whether every GraphQL error in the response is
// a known transient upstream failure. Anything else (bad query, auth issues,
// PersistedQueryNotFound) must reach the caller immediately.
func isTransientGQLFailure(errs []Error) bool {
	if len(errs) == 0 {
		return false
	}
	for _, e := range errs {
		switch strings.ToLower(strings.TrimSpace(e.Message)) {
		case "service error", "service unavailable", "service timeout", "internal server error":
		default:
			return false
		}
	}
	return true
}

func retryAfterDelay(header http.Header) time.Duration {
	value := header.Get("Retry-After")
	if value == "" {
		return 0
	}
	if seconds, err := strconv.Atoi(value); err == nil {
		if seconds <= 0 {
			return 0
		}
		return time.Duration(seconds) * time.Second
	}
	if when, err := http.ParseTime(value); err == nil {
		if delay := time.Until(when); delay > 0 {
			return delay
		}
	}
	return 0
}

func (c Client) endpoint() string {
	if c.Endpoint != "" {
		return c.Endpoint
	}
	return defaultEndpoint
}
