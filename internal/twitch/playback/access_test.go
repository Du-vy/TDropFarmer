package playback

import (
	"bytes"
	"compress/gzip"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Du-vy/TDropFarmer/internal/domain"
	"github.com/Du-vy/TDropFarmer/internal/twitch/gql"
)

type mockGQLClient struct {
	response      gql.Response
	watchResponse gql.Response
	err           error
	watchErr      error
}

func (m mockGQLClient) Do(ctx context.Context, req gql.Request) (gql.Response, error) {
	if strings.Contains(req.Query, "sendSpadeEvents") {
		if m.watchErr != nil {
			return gql.Response{}, m.watchErr
		}
		if len(m.watchResponse.Data) > 0 {
			return m.watchResponse, nil
		}
		return gql.Response{Data: []byte(`{"sendSpadeEvents":{"statusCode":204}}`)}, nil
	}
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
			w.WriteHeader(http.StatusNoContent)
		default:
			t.Errorf("unexpected request: %s %s", r.Method, r.URL.String())
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer server.Close()

	oldUsherURL := usherBaseURL
	usherBaseURL = server.URL
	defer func() { usherBaseURL = oldUsherURL }()

	tokenClient := mockGQLClient{
		response: gql.Response{
			Data: []byte(`{"streamPlaybackAccessToken":{"value":"val","signature":"sig","__typename":"PlaybackAccessToken"}}`),
		},
	}
	fetcher := TokenFetcher{Client: tokenClient}

	watcher := NewWatcher(fetcher)
	watcher.spadeURL = server.URL + "/track"
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
}

func TestEncodeGQLWatchPayload(t *testing.T) {
	encoded, err := encodeGQLWatchPayload(domain.Streamer{
		Login:       "test_channel",
		ID:          "12345",
		GameID:      "game-1",
		GameName:    "Test Game",
		BroadcastID: "987654321",
	}, "user_id_123")
	if err != nil {
		t.Fatalf("encodeGQLWatchPayload returned error: %v", err)
	}

	compressed, err := base64.StdEncoding.DecodeString(encoded)
	if err != nil {
		t.Fatalf("payload is not base64: %v", err)
	}
	reader, err := gzip.NewReader(bytes.NewReader(compressed))
	if err != nil {
		t.Fatalf("payload is not gzip: %v", err)
	}
	decompressed, err := io.ReadAll(reader)
	if err != nil {
		t.Fatalf("read gzip payload: %v", err)
	}
	if err := reader.Close(); err != nil {
		t.Fatalf("close gzip payload: %v", err)
	}

	var events []struct {
		Event      string         `json:"event"`
		Properties map[string]any `json:"properties"`
	}
	if err := json.Unmarshal(decompressed, &events); err != nil {
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
	if props["game"] != "Test Game" || props["game_id"] != "game-1" || props["minutes_logged"] != float64(1) || props["user_id"] != "user_id_123" {
		t.Fatalf("unexpected watch properties: %+v", props)
	}
}
