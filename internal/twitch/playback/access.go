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
	"regexp"
	"strings"
	"time"

	"github.com/Du-vy/TDropFarmer/internal/domain"
	"github.com/Du-vy/TDropFarmer/internal/twitch/gql"
	"github.com/Du-vy/TDropFarmer/internal/twitch/profile"
)

var usherBaseURL = "https://usher.ttvnw.net/api/channel/hls"

const (
	DefaultSpadeURL = "https://spade.twitch.tv/track"

	// Raw GQL query instead of persisted query hash.
	// Persisted query hashes get rotated by Twitch periodically; using the
	// raw query string avoids breakage when hashes change.
	// Only live stream tokens are needed, so VOD-related variables are omitted.
	playbackAccessTokenQuery = `query PlaybackAccessToken($login: String!, $playerType: String!) {
  streamPlaybackAccessToken(channelName: $login, params: {platform: "web", playerBackend: "mediaplayer", playerType: $playerType}) {
    value
    signature
    __typename
  }
}`
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
		Query: playbackAccessTokenQuery,
		Variables: map[string]any{
			"login":      channelLogin,
			"playerType": "site",
		},
	})
	if err != nil {
		return AccessToken{}, err
	}

	var result struct {
		StreamPlaybackAccessToken struct {
			Signature string `json:"signature"`
			Value     string `json:"value"`
		} `json:"streamPlaybackAccessToken"`
	}
	if err := json.Unmarshal(response.Data, &result); err != nil {
		return AccessToken{}, fmt.Errorf("decode playback access token: %w", err)
	}
	token := result.StreamPlaybackAccessToken
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
		fetcher:  fetcher,
		client:   &http.Client{Timeout: 20 * time.Second},
		spadeURL: DefaultSpadeURL,
	}
}

func (w *Watcher) SetSpadeURL(target string) {
	if target != "" {
		w.spadeURL = target
	}
}

func (w *Watcher) SendMinuteWatched(ctx context.Context, streamer domain.Streamer, userID string) error {
	return w.sendWatched(ctx, streamer, userID, true)
}

func (w *Watcher) SendPresence(ctx context.Context, streamer domain.Streamer, userID string) error {
	return w.sendWatched(ctx, streamer, userID, false)
}

func (w *Watcher) sendWatched(ctx context.Context, streamer domain.Streamer, userID string, includeDropMetadata bool) error {
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
	// Find the lowest quality playlist URL (skip any lines starting with #)
	var lowQualityURL string
	for i := len(qualityLines) - 1; i >= 0; i-- {
		line := qualityLines[i]
		if !strings.HasPrefix(line, "#") {
			lowQualityURL = line
			break
		}
	}
	if lowQualityURL == "" {
		return errNoStream
	}

	// Parse usherURL to resolve relative quality playlist URLs
	usherBase, err := url.Parse(usherURL)
	if err != nil {
		return fmt.Errorf("parse usher URL: %w", err)
	}

	// Resolve relative quality URL if necessary
	uq, err := url.Parse(lowQualityURL)
	if err != nil {
		return fmt.Errorf("parse quality URL: %w", err)
	}
	resolvedLowQualityURL := usherBase.ResolveReference(uq).String()

	mediaPlaylist, err := w.httpGet(ctx, resolvedLowQualityURL)
	if err != nil {
		return fmt.Errorf("get media segments: %w", err)
	}
	mediaLines := nonEmptyLines(mediaPlaylist)

	// Parse base URL for resolving relative segment URLs
	base, err := url.Parse(resolvedLowQualityURL)
	if err != nil {
		return fmt.Errorf("parse base URL: %w", err)
	}

	// Find the latest media segment URL (skip any lines starting with #)
	var segmentURL string
	for i := len(mediaLines) - 1; i >= 0; i-- {
		line := mediaLines[i]
		if !strings.HasPrefix(line, "#") {
			segmentURL = line
			break
		}
	}
	if segmentURL == "" {
		return errNoMedia
	}

	// Resolve relative URLs if necessary
	u, err := url.Parse(segmentURL)
	if err != nil {
		return fmt.Errorf("parse segment URL: %w", err)
	}
	resolvedURL := base.ResolveReference(u).String()

	if err := w.httpHead(ctx, resolvedURL); err != nil {
		return fmt.Errorf("head media segment: %w", err)
	}

	if err := w.sendSpadeEvent(ctx, streamer, userID, includeDropMetadata); err != nil {
		return fmt.Errorf("send watch event: %w", err)
	}

	return nil
}

func (w *Watcher) sendSpadeEvent(ctx context.Context, streamer domain.Streamer, userID string, includeDropMetadata bool) error {
	encoded, err := encodeSpadePayload(streamer, userID, includeDropMetadata)
	if err != nil {
		return err
	}

	form := url.Values{}
	form.Set("data", encoded)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, w.spadeURL, strings.NewReader(form.Encode()))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	profile.ApplyWebPlayer(req)
	resp, err := w.client.Do(req)
	if err != nil {
		return err
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusNoContent {
		return fmt.Errorf("unexpected watch event status %d", resp.StatusCode)
	}
	return nil
}

func (w *Watcher) httpGet(ctx context.Context, target string) (string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, target, nil)
	if err != nil {
		return "", err
	}
	profile.ApplyWebPlayer(req)
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
	profile.ApplyWebPlayer(req)
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

func encodeSpadePayload(streamer domain.Streamer, userID string, includeDropMetadata bool) (string, error) {
	properties := map[string]any{
		"broadcast_id": streamer.BroadcastID,
		"channel_id":   streamer.ID,
		"player":       "site",
		"user_id":      userID,
		"live":         true,
		"channel":      streamer.Login,
	}
	if includeDropMetadata {
		properties["game"] = streamer.GameName
		properties["game_id"] = streamer.GameID
	}
	payload := []map[string]any{
		{
			"event":      "minute-watched",
			"properties": properties,
		},
	}

	raw, err := json.Marshal(payload)
	if err != nil {
		return "", err
	}

	return base64.StdEncoding.EncodeToString(raw), nil
}

func DiscoverSpadeURL(ctx context.Context) (string, error) {
	client := &http.Client{Timeout: 10 * time.Second}
	return discoverSpadeURL(ctx, client, "https://www.twitch.tv/")
}

func discoverSpadeURL(ctx context.Context, client *http.Client, twitchURL string) (string, error) {
	home, err := fetchText(ctx, client, twitchURL)
	if err != nil {
		return "", fmt.Errorf("fetch Twitch page: %w", err)
	}

	settingsPattern := regexp.MustCompile(`https?://[^"'\\\s]+/config/settings[^"'\\\s]*?\.js`)
	settingsURL := settingsPattern.FindString(home)
	if settingsURL == "" {
		return "", fmt.Errorf("settings script URL not found")
	}

	settings, err := fetchText(ctx, client, settingsURL)
	if err != nil {
		return "", fmt.Errorf("fetch Twitch settings: %w", err)
	}

	endpointValue := ""
	for _, key := range []string{"spade", "beacon"} {
		endpointPattern := regexp.MustCompile(fmt.Sprintf(`"%s_url"\s*:\s*"([^"]+)"`, key))
		matches := endpointPattern.FindStringSubmatch(settings)
		if len(matches) >= 2 {
			endpointValue = matches[1]
			break
		}
	}
	if endpointValue == "" {
		return "", fmt.Errorf("Spade endpoint not found in Twitch settings")
	}

	endpoint, err := url.Parse(endpointValue)
	if err != nil || endpoint.Scheme != "https" || endpoint.Host == "" {
		return "", fmt.Errorf("invalid Spade endpoint %q", endpointValue)
	}
	return endpoint.String(), nil
}

func fetchText(ctx context.Context, client *http.Client, target string) (string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, target, nil)
	if err != nil {
		return "", err
	}
	profile.ApplyWebPlayer(req)
	resp, err := client.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return "", fmt.Errorf("http status %d", resp.StatusCode)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	if err != nil {
		return "", err
	}
	return string(body), nil
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
