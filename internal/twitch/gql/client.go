package gql

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"
)

const defaultEndpoint = "https://gql.twitch.tv/gql"

type Client struct {
	ClientID    string
	AccessToken string
	HTTPClient  *http.Client
	Endpoint    string
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

func (c Client) Do(ctx context.Context, request Request) (Response, error) {
	if request.Query == "" && request.OperationName == "" && len(request.Extensions) == 0 {
		return Response{}, fmt.Errorf("graphql query, operation name, or extensions are required")
	}
	body, err := json.Marshal(request)
	if err != nil {
		return Response{}, err
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.endpoint(), bytes.NewReader(body))
	if err != nil {
		return Response{}, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	if c.ClientID != "" {
		req.Header.Set("Client-Id", c.ClientID)
	}
	if c.AccessToken != "" {
		req.Header.Set("Authorization", "OAuth "+c.AccessToken)
	}

	client := c.HTTPClient
	if client == nil {
		client = &http.Client{Timeout: 20 * time.Second}
	}

	resp, err := client.Do(req)
	if err != nil {
		return Response{}, err
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return Response{}, err
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return Response{}, fmt.Errorf("graphql request failed: status %d", resp.StatusCode)
	}

	var gqlResponse Response
	if err := json.Unmarshal(respBody, &gqlResponse); err != nil {
		return Response{}, fmt.Errorf("decode graphql response: %w", err)
	}
	if len(gqlResponse.Errors) > 0 {
		return gqlResponse, gqlResponse.Errors[0]
	}
	return gqlResponse, nil
}

func (c Client) endpoint() string {
	if c.Endpoint != "" {
		return c.Endpoint
	}
	return defaultEndpoint
}
