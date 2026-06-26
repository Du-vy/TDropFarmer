package playback

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
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
	err := watcher.SendMinuteWatched(context.Background(), domain.Streamer{
		Login: "test_channel",
		ID:    "12345",
	})
	if err != nil {
		t.Fatalf("SendMinuteWatched failed: %v", err)
	}
}
