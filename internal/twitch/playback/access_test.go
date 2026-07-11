package playback

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/Du-vy/TDropFarmer/internal/domain"
	"github.com/Du-vy/TDropFarmer/internal/twitch/gql"
)

type mockGQLClient struct {
	response gql.Response
	err      error
}

func (m mockGQLClient) Do(ctx context.Context, req gql.Request) (gql.Response, error) {
	return m.response, m.err
}

func TestFetch(t *testing.T) {
	t.Run("success", func(t *testing.T) {
		rawResponse := []byte(`{"streamPlaybackAccessToken":{"value":"value_token","signature":"signature_token","__typename":"PlaybackAccessToken"}}`)
		client := mockGQLClient{
			response: gql.Response{
				Data: rawResponse,
			},
		}
		fetcher := TokenFetcher{Client: client}
		token, err := fetcher.Fetch(context.Background(), "test_channel")
		if err != nil {
			t.Fatalf("Fetch returned error: %v", err)
		}
		if token.Signature != "signature_token" {
			t.Errorf("Signature = %q, want signature_token", token.Signature)
		}
		if token.Value != "value_token" {
			t.Errorf("Value = %q, want value_token", token.Value)
		}
	})

	t.Run("missing token", func(t *testing.T) {
		rawResponse := []byte(`{"streamPlaybackAccessToken":{"value":"","signature":"","__typename":"PlaybackAccessToken"}}`)
		client := mockGQLClient{
			response: gql.Response{
				Data: rawResponse,
			},
		}
		fetcher := TokenFetcher{Client: client}
		_, err := fetcher.Fetch(context.Background(), "test_channel")
		if !errors.Is(err, errMissingToken) {
			t.Fatalf("Fetch returned error = %v, want %v", err, errMissingToken)
		}
	})
}

func TestWatcher_SendMinuteWatched(t *testing.T) {
	var spadePayload string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.Contains(r.URL.Path, "test_channel.m3u8"):
			w.Write([]byte(`#EXTM3U
#EXT-X-STREAM-INF:BANDWIDTH=100000
/low_quality_stream.m3u8`))
		case strings.Contains(r.URL.Path, "low_quality_stream.m3u8"):
			// Test parsing by including metadata comments/prefetch tags
			w.Write([]byte(`#EXTM3U
#EXT-X-VERSION:3
#EXTINF:2.000,live
/segment_abc.ts
#EXT-X-PREFETCH:/prefetch_xyz.ts`))
		case strings.Contains(r.URL.Path, "segment_abc.ts"):
			if r.Method != http.MethodHead {
				t.Errorf("expected HEAD request for segment, got %s", r.Method)
			}
			w.WriteHeader(http.StatusOK)
		case strings.Contains(r.URL.Path, "/track"):
			if r.Method != http.MethodPost {
				t.Errorf("expected POST request for spade, got %s", r.Method)
			}
			if got := r.Header.Get("Content-Type"); got != "application/x-www-form-urlencoded" {
				t.Errorf("Content-Type = %q, want application/x-www-form-urlencoded", got)
			}
			if err := r.ParseForm(); err != nil {
				t.Errorf("parse spade form: %v", err)
			}
			spadePayload = r.Form.Get("data")
			w.WriteHeader(http.StatusNoContent)
		default:
			t.Errorf("unexpected request: %s %s", r.Method, r.URL.String())
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer server.Close()

	oldUsherURL := usherBaseURL
	usherBaseURL = server.URL
	defer func() {
		usherBaseURL = oldUsherURL
	}()

	tokenClient := mockGQLClient{
		response: gql.Response{
			Data: []byte(`{"streamPlaybackAccessToken":{"value":"val","signature":"sig","__typename":"PlaybackAccessToken"}}`),
		},
	}
	fetcher := TokenFetcher{Client: tokenClient}

	watcher := NewWatcher(fetcher)
	watcher.SetSpadeURL(server.URL + "/track")
	err := watcher.SendMinuteWatched(context.Background(), domain.Streamer{
		Login:       "test_channel",
		ID:          "12345",
		GameID:      "game-1",
		GameName:    "Test Game",
		BroadcastID: "987654321",
	}, "user_id_123")
	if err != nil {
		t.Fatalf("SendMinuteWatched failed: %v", err)
	}
	if spadePayload == "" {
		t.Fatal("spade payload was not posted")
	}
	assertSpadePayload(t, spadePayload)
}

func TestEncodeSpadePayload(t *testing.T) {
	encoded, err := encodeSpadePayload(domain.Streamer{
		Login:       "test_channel",
		ID:          "12345",
		GameID:      "game-1",
		GameName:    "Test Game",
		BroadcastID: "987654321",
	}, "user_id_123")
	if err != nil {
		t.Fatalf("encodeSpadePayload returned error: %v", err)
	}
	assertSpadePayload(t, encoded)
}

func assertSpadePayload(t *testing.T, encoded string) {
	t.Helper()
	raw, err := base64.StdEncoding.DecodeString(encoded)
	if err != nil {
		t.Fatalf("payload is not base64: %v", err)
	}

	var events []struct {
		Event      string         `json:"event"`
		Properties map[string]any `json:"properties"`
	}
	if err := json.Unmarshal(raw, &events); err != nil {
		t.Fatalf("decode watch events: %v", err)
	}
	if len(events) != 1 {
		t.Fatalf("events length = %d, want 1", len(events))
	}
	if events[0].Event != "minute-watched" {
		t.Fatalf("event = %q, want minute-watched", events[0].Event)
	}
	props := events[0].Properties
	if props["broadcast_id"] != "987654321" || props["channel_id"] != "12345" || props["channel"] != "test_channel" {
		t.Fatalf("unexpected channel properties: %+v", props)
	}
	if props["game"] != "Test Game" || props["game_id"] != "game-1" || props["player"] != "site" || props["live"] != true || props["user_id"] != "user_id_123" {
		t.Fatalf("unexpected watch properties: %+v", props)
	}
}

func TestSpadePayloadIsFormSafe(t *testing.T) {
	encoded, err := encodeSpadePayload(domain.Streamer{Login: "test_channel"}, "user_id_123")
	if err != nil {
		t.Fatalf("encodeSpadePayload returned error: %v", err)
	}
	form := url.Values{"data": {encoded}}.Encode()
	parsed, err := url.ParseQuery(form)
	if err != nil {
		t.Fatalf("parse encoded form: %v", err)
	}
	if parsed.Get("data") != encoded {
		t.Fatal("base64 payload changed during form encoding")
	}
}

func TestDiscoverSpadeURL(t *testing.T) {
	var server *httptest.Server
	server = httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/":
			fmt.Fprintf(w, `<script src="%s/config/settings.test.js"></script>`, server.URL)
		case "/config/settings.test.js":
			fmt.Fprintf(w, `window.__settings={"beacon_url":"%s/beacon","spade_url":"%s/spade"};`, server.URL, server.URL)
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer server.Close()

	got, err := discoverSpadeURL(context.Background(), server.Client(), server.URL+"/")
	if err != nil {
		t.Fatalf("discoverSpadeURL returned error: %v", err)
	}
	if want := server.URL + "/spade"; got != want {
		t.Fatalf("Spade URL = %q, want %q", got, want)
	}
}

func TestDiscoverSpadeURLRejectsMissingSettingsScript(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("no settings here"))
	}))
	defer server.Close()

	if _, err := discoverSpadeURL(context.Background(), server.Client(), server.URL); err == nil {
		t.Fatal("discoverSpadeURL returned nil error without a settings script")
	}
}
