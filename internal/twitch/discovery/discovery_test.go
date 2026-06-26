package discovery

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"testing"

	"github.com/Du-vy/TDropFarmer/internal/domain"
	"github.com/Du-vy/TDropFarmer/internal/twitch/gql"
)

func TestSlugify(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		{"Smite", "smite"},
		{"Destiny 2", "destiny-2"},
		{"Assassin's Creed", "assassins-creed"},
		{"Just Chatting", "just-chatting"},
		{"A---B", "a-b"},
	}

	for _, tt := range tests {
		got := Slugify(tt.input)
		if got != tt.want {
			t.Errorf("Slugify(%q) = %q, want %q", tt.input, got, tt.want)
		}
	}
}

type mockGQLClient struct {
	doFunc func(context.Context, gql.Request) (gql.Response, error)
}

func (m mockGQLClient) Do(ctx context.Context, req gql.Request) (gql.Response, error) {
	return m.doFunc(ctx, req)
}

func TestGetLiveStreams(t *testing.T) {
	mockResponse := `{
		"game": {
			"streams": {
				"edges": [
					{
						"node": {
							"id": "12345",
							"broadcaster": {
								"id": "999",
								"login": "streamer1",
								"displayName": "StreamerOne"
							}
						}
					}
				]
			}
		}
	}`

	client := Client{
		Client: mockGQLClient{
			doFunc: func(ctx context.Context, req gql.Request) (gql.Response, error) {
				if req.OperationName != "DirectoryPage_Game" {
					return gql.Response{}, errors.New("unexpected operation name")
				}
				slug, _ := req.Variables["slug"].(string)
				if slug != "smite" {
					return gql.Response{}, fmt.Errorf("expected slug 'smite', got %q", slug)
				}
				return gql.Response{
					Data: json.RawMessage(mockResponse),
				}, nil
			},
		},
	}

	streamers, err := client.GetLiveStreams(context.Background(), "Smite", 10)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	expected := []domain.Streamer{
		{ID: "999", Login: "streamer1", DisplayName: "StreamerOne", BroadcastID: "12345"},
	}
	if !reflect.DeepEqual(streamers, expected) {
		t.Errorf("got %v, want %v", streamers, expected)
	}
}
