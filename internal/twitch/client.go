package twitch

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/Du-vy/TDropFarmer/internal/domain"
)

const defaultHelixEndpoint = "https://api.twitch.tv/helix"

type Client struct {
	ClientID    string
	AccessToken string
	HTTPClient  *http.Client
	HelixURL    string
}

type User struct {
	ID              string `json:"id"`
	Login           string `json:"login"`
	DisplayName     string `json:"display_name"`
	Type            string `json:"type"`
	BroadcasterType string `json:"broadcaster_type"`
	Description     string `json:"description"`
	ProfileImageURL string `json:"profile_image_url"`
	OfflineImageURL string `json:"offline_image_url"`
	ViewCount       int    `json:"view_count"`
	CreatedAt       string `json:"created_at"`
}

func (c Client) ResolveUsers(ctx context.Context, logins []string) ([]User, error) {
	logins = uniqueLogins(logins)
	if len(logins) == 0 {
		return nil, nil
	}

	var users []User
	for start := 0; start < len(logins); start += 100 {
		end := start + 100
		if end > len(logins) {
			end = len(logins)
		}

		batch, err := c.resolveUserBatch(ctx, logins[start:end])
		if err != nil {
			return nil, err
		}
		users = append(users, batch...)
	}
	return users, nil
}

func (c Client) ResolveStreamers(ctx context.Context, logins []string) ([]domain.Streamer, error) {
	users, err := c.ResolveUsers(ctx, logins)
	if err != nil {
		return nil, err
	}
	streamers := make([]domain.Streamer, 0, len(users))
	for _, user := range users {
		streamers = append(streamers, domain.Streamer{
			ID:          user.ID,
			Login:       user.Login,
			DisplayName: user.DisplayName,
			Broadcaster: user.BroadcasterType,
			ProfileURL:  user.ProfileImageURL,
		})
	}
	return streamers, nil
}

func (c Client) resolveUserBatch(ctx context.Context, logins []string) ([]User, error) {
	endpoint, err := url.Parse(c.helixURL() + "/users")
	if err != nil {
		return nil, err
	}
	query := endpoint.Query()
	for _, login := range logins {
		query.Add("login", login)
	}
	endpoint.RawQuery = query.Encode()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint.String(), nil)
	if err != nil {
		return nil, err
	}

	var response struct {
		Data []User `json:"data"`
	}
	if err := c.doJSON(req, &response); err != nil {
		return nil, err
	}
	return response.Data, nil
}

func (c Client) doJSON(req *http.Request, target any) error {
	if c.ClientID == "" {
		return fmt.Errorf("twitch client_id is required")
	}
	if c.AccessToken == "" {
		return fmt.Errorf("twitch access token is required")
	}

	req.Header.Set("Client-Id", c.ClientID)
	req.Header.Set("Authorization", "Bearer "+c.AccessToken)
	req.Header.Set("Accept", "application/json")

	client := c.HTTPClient
	if client == nil {
		client = &http.Client{Timeout: 20 * time.Second}
	}

	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return err
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		var twitchErr Error
		if err := json.Unmarshal(body, &twitchErr); err == nil && (twitchErr.Message != "" || twitchErr.Kind != "") {
			twitchErr.StatusCode = resp.StatusCode
			return twitchErr
		}
		return fmt.Errorf("twitch request failed: status %d", resp.StatusCode)
	}
	if target == nil {
		return nil
	}
	if err := json.Unmarshal(body, target); err != nil {
		return fmt.Errorf("decode twitch response: %w", err)
	}
	return nil
}

func (c Client) helixURL() string {
	if c.HelixURL != "" {
		return strings.TrimRight(c.HelixURL, "/")
	}
	return defaultHelixEndpoint
}

type Error struct {
	StatusCode int    `json:"-"`
	Kind       string `json:"error"`
	Status     int    `json:"status"`
	Message    string `json:"message"`
}

func (e Error) Error() string {
	if e.Message != "" {
		return e.Message
	}
	if e.Kind != "" {
		return e.Kind
	}
	return fmt.Sprintf("twitch request failed: status %d", e.StatusCode)
}

func uniqueLogins(logins []string) []string {
	seen := make(map[string]struct{}, len(logins))
	unique := make([]string, 0, len(logins))
	for _, login := range logins {
		login = strings.ToLower(strings.TrimSpace(login))
		if login == "" {
			continue
		}
		if _, ok := seen[login]; ok {
			continue
		}
		seen[login] = struct{}{}
		unique = append(unique, login)
	}
	return unique
}
