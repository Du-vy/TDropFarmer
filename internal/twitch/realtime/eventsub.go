package realtime

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

const defaultEventSubEndpoint = "https://api.twitch.tv/helix/eventsub/subscriptions"

type Client struct {
	ClientID    string
	AccessToken string
	HTTPClient  *http.Client

	EventSubEndpoint string
	WebSocketURL     string
}

type SubscriptionRequest struct {
	Type      string            `json:"type"`
	Version   string            `json:"version"`
	Condition map[string]string `json:"condition"`
	Transport Transport         `json:"transport"`
}

type SubscriptionResponse struct {
	Data         []Subscription `json:"data"`
	Total        int            `json:"total"`
	TotalCost    int            `json:"total_cost"`
	MaxTotalCost int            `json:"max_total_cost"`
}

func (c Client) CreateWebSocketSubscription(ctx context.Context, sessionID string, request SubscriptionRequest) (SubscriptionResponse, error) {
	if sessionID == "" {
		return SubscriptionResponse{}, fmt.Errorf("eventsub session_id is required")
	}
	if request.Type == "" {
		return SubscriptionResponse{}, fmt.Errorf("eventsub subscription type is required")
	}
	if request.Version == "" {
		return SubscriptionResponse{}, fmt.Errorf("eventsub subscription version is required")
	}
	if len(request.Condition) == 0 {
		return SubscriptionResponse{}, fmt.Errorf("eventsub subscription condition is required")
	}
	request.Transport = Transport{Method: "websocket", SessionID: sessionID}

	body, err := json.Marshal(request)
	if err != nil {
		return SubscriptionResponse{}, err
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.eventSubEndpoint(), bytes.NewReader(body))
	if err != nil {
		return SubscriptionResponse{}, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")

	var response SubscriptionResponse
	if err := c.doJSON(req, &response); err != nil {
		return SubscriptionResponse{}, err
	}
	return response, nil
}

func (c Client) doJSON(req *http.Request, target any) error {
	if c.ClientID == "" {
		return fmt.Errorf("eventsub client_id is required")
	}
	if c.AccessToken == "" {
		return fmt.Errorf("eventsub access token is required")
	}
	req.Header.Set("Client-Id", c.ClientID)
	req.Header.Set("Authorization", "Bearer "+c.AccessToken)

	client := c.HTTPClient
	if client == nil {
		client = &http.Client{Timeout: 20 * time.Second}
	}

	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return err
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		var apiErr APIError
		if err := json.Unmarshal(respBody, &apiErr); err == nil && (apiErr.Message != "" || apiErr.Kind != "") {
			apiErr.StatusCode = resp.StatusCode
			return apiErr
		}
		return fmt.Errorf("eventsub request failed: status %d", resp.StatusCode)
	}
	if target == nil {
		return nil
	}
	if err := json.Unmarshal(respBody, target); err != nil {
		return fmt.Errorf("decode eventsub response: %w", err)
	}
	return nil
}

func (c Client) eventSubEndpoint() string {
	if c.EventSubEndpoint != "" {
		return c.EventSubEndpoint
	}
	return defaultEventSubEndpoint
}

func (c Client) webSocketURL() string {
	if c.WebSocketURL != "" {
		return c.WebSocketURL
	}
	return defaultWebSocketURL
}

type APIError struct {
	StatusCode int    `json:"-"`
	Kind       string `json:"error"`
	Status     int    `json:"status"`
	Message    string `json:"message"`
}

func (e APIError) Error() string {
	if e.Message != "" {
		return e.Message
	}
	if e.Kind != "" {
		return e.Kind
	}
	return fmt.Sprintf("eventsub request failed: status %d", e.StatusCode)
}

func NormalizeSubscriptionType(value string) string {
	return strings.ToLower(strings.TrimSpace(value))
}
