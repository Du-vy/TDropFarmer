package playback

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/Du-vy/TDropFarmer/internal/domain"
	"github.com/Du-vy/TDropFarmer/internal/twitch/gql"
)

const (
	playbackAccessTokenOp   = "PlaybackAccessToken"
	playbackAccessTokenHash = "ed230aa1e33e07eebb8928504583da78a5173989fadfb1ac94be06a04f3cdbe9"
	usherBaseURL            = "https://usher.ttvnw.net/api/channel/hls"
)

var (
	errMissingToken = errors.New("playback access token missing signature or value")
	errNoStream     = errors.New("no stream qualities returned")
	errNoMedia      = errors.New("no media segments returned")
)

type GQLClient interface {
	Do(context.Context, gql.Request) (gql.Response, error)
}

type TokenFetcher struct {
	Client GQLClient
}

type AccessToken struct {
	Signature string
	Value     string
}

func (f TokenFetcher) Fetch(ctx context.Context, channelLogin string) (AccessToken, error) {
	response, err := f.Client.Do(ctx, gql.Request{
		OperationName: playbackAccessTokenOp,
		Variables: map[string]any{
			"login":      channelLogin,
			"isLive":     true,
			"isVod":      false,
			"vodID":      "",
			"platform":   "web",
			"playerType": "site",
		},
		Extensions: persistedQuery(playbackAccessTokenHash),
	})
	if err != nil {
		return AccessToken{}, err
	}

	var result struct {
		Data struct {
			StreamPlaybackAccessToken struct {
				Signature string `json:"signature"`
				Value     string `json:"value"`
			} `json:"streamPlaybackAccessToken"`
		} `json:"data"`
	}
	if err := json.Unmarshal(response.Data, &result); err != nil {
		return AccessToken{}, fmt.Errorf("decode playback access token: %w", err)
	}
	token := result.Data.StreamPlaybackAccessToken
	if token.Signature == "" || token.Value == "" {
		return AccessToken{}, errMissingToken
	}
	return AccessToken{Signature: token.Signature, Value: token.Value}, nil
}

func (t AccessToken) UsherURL(login string) string {
	return fmt.Sprintf("%s/%s.m3u8?sig=%s&token=%s&allow_source=true",
		usherBaseURL, login, t.Signature, url.QueryEscape(t.Value))
}

type Watcher struct {
	fetcher  TokenFetcher
	client   *http.Client
	spadeURL string
}

func NewWatcher(fetcher TokenFetcher) *Watcher {
	return &Watcher{
		fetcher: fetcher,
		client:  &http.Client{Timeout: 20 * time.Second},
	}
}

func (w *Watcher) SetSpadeURL(spadeURL string) {
	w.spadeURL = spadeURL
}

func (w *Watcher) SendMinuteWatched(ctx context.Context, streamer domain.Streamer) error {
	token, err := w.fetcher.Fetch(ctx, streamer.Login)
	if err != nil {
		return fmt.Errorf("fetch playback token: %w", err)
	}

	usherURL := token.UsherURL(streamer.Login)
	qualities, err := w.httpGet(ctx, usherURL)
	if err != nil {
		return fmt.Errorf("get stream qualities: %w", err)
	}
	qualityLines := nonEmptyLines(qualities)
	if len(qualityLines) == 0 {
		return errNoStream
	}
	lowQualityURL := qualityLines[len(qualityLines)-1]

	mediaPlaylist, err := w.httpGet(ctx, lowQualityURL)
	if err != nil {
		return fmt.Errorf("get media segments: %w", err)
	}
	mediaLines := nonEmptyLines(mediaPlaylist)
	if len(mediaLines) < 2 {
		return errNoMedia
	}
	segmentURL := mediaLines[len(mediaLines)-2]

	if err := w.httpHead(ctx, segmentURL); err != nil {
		return fmt.Errorf("head media segment: %w", err)
	}

	if w.spadeURL != "" {
		payload := encodeSpadePayload(streamer.ID)
		_ = w.httpPostForm(ctx, w.spadeURL, payload)
	}

	return nil
}

func (w *Watcher) httpGet(ctx context.Context, target string) (string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, target, nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("User-Agent", userAgent())
	resp, err := w.client.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return "", fmt.Errorf("http status %d", resp.StatusCode)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return "", err
	}
	return string(body), nil
}

func (w *Watcher) httpHead(ctx context.Context, target string) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodHead, target, nil)
	if err != nil {
		return err
	}
	req.Header.Set("User-Agent", userAgent())
	resp, err := w.client.Do(req)
	if err != nil {
		return err
	}
	resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("http status %d", resp.StatusCode)
	}
	return nil
}

func (w *Watcher) httpPostForm(ctx context.Context, target string, data []byte) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, target, strings.NewReader(string(data)))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("User-Agent", userAgent())
	resp, err := w.client.Do(req)
	if err != nil {
		return err
	}
	resp.Body.Close()
	return nil
}

func encodeSpadePayload(channelID string) []byte {
	payload := []map[string]any{
		{
			"event": "minute-watched",
			"properties": map[string]any{
				"channel_id": channelID,
				"player":     "site",
				"live":       true,
			},
		},
	}
	raw, _ := json.Marshal(payload)
	encoded := base64.StdEncoding.EncodeToString(raw)
	return []byte(fmt.Sprintf("data=%s", encoded))
}

func nonEmptyLines(text string) []string {
	lines := strings.Split(text, "\n")
	result := make([]string, 0, len(lines))
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line != "" {
			result = append(result, line)
		}
	}
	return result
}

func userAgent() string {
	return "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/131.0.0.0 Safari/537.36"
}

func persistedQuery(hash string) map[string]any {
	return map[string]any{
		"persistedQuery": map[string]any{
			"version":    1,
			"sha256Hash": hash,
		},
	}
}
