package twitch

import (
	"context"
	"encoding/json"
	"net/http"
	"net/url"
)

type StreamInfo struct {
	ID           string   `json:"id"`
	UserID       string   `json:"user_id"`
	UserLogin    string   `json:"user_login"`
	UserName     string   `json:"user_name"`
	GameID       string   `json:"game_id"`
	GameName     string   `json:"game_name"`
	Type         string   `json:"type"`
	Title        string   `json:"title"`
	ViewerCount  int      `json:"viewer_count"`
	StartedAt    string   `json:"started_at"`
	Language     string   `json:"language"`
	ThumbnailURL string   `json:"thumbnail_url"`
	TagIDs       []string `json:"tag_ids"`
	Tags         []string `json:"tags"`
}

func (c Client) GetStreams(ctx context.Context, userIDs []string) ([]StreamInfo, error) {
	if len(userIDs) == 0 {
		return nil, nil
	}
	endpoint, err := url.Parse(c.helixURL() + "/streams")
	if err != nil {
		return nil, err
	}
	q := endpoint.Query()
	for _, id := range userIDs {
		q.Add("user_id", id)
	}
	endpoint.RawQuery = q.Encode()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint.String(), nil)
	if err != nil {
		return nil, err
	}

	var response struct {
		Data []StreamInfo `json:"data"`
	}
	if err := c.doJSON(req, &response); err != nil {
		return nil, err
	}
	return response.Data, nil
}

func (c Client) GetStreamsByLogin(ctx context.Context, logins []string) ([]StreamInfo, error) {
	if len(logins) == 0 {
		return nil, nil
	}
	endpoint, err := url.Parse(c.helixURL() + "/streams")
	if err != nil {
		return nil, err
	}
	q := endpoint.Query()
	for _, login := range logins {
		q.Add("user_login", login)
	}
	endpoint.RawQuery = q.Encode()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint.String(), nil)
	if err != nil {
		return nil, err
	}

	var response struct {
		Data []StreamInfo `json:"data"`
	}
	if err := c.doJSON(req, &response); err != nil {
		return nil, err
	}
	return response.Data, nil
}

func streamInfoToJSON(info StreamInfo) []byte {
	data, _ := json.Marshal(info)
	return data
}
