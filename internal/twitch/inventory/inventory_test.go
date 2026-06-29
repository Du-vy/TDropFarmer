package inventory

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/Du-vy/TDropFarmer/internal/twitch/gql"
)

type recordingGQLClient struct {
	request  gql.Request
	response gql.Response
	err      error
}

func (c *recordingGQLClient) Do(ctx context.Context, req gql.Request) (gql.Response, error) {
	c.request = req
	return c.response, c.err
}

type campaignGamesGQLClient struct {
	dashboard []byte
	details   map[string][]byte
	inventory []byte
}

func (c campaignGamesGQLClient) Do(ctx context.Context, req gql.Request) (gql.Response, error) {
	if req.OperationName == viewerCampaignsOperation {
		return gql.Response{Data: c.dashboard}, nil
	}
	if req.OperationName == campaignDetailsOperation {
		id, _ := req.Variables["dropID"].(string)
		return gql.Response{Data: c.details[id]}, nil
	}
	if req.OperationName == inventoryOperation {
		return gql.Response{Data: c.inventory}, nil
	}
	return gql.Response{}, nil
}

func TestGetInventory(t *testing.T) {
	mockResponse := `{"currentUser":{"inventory":{"dropCampaignsInProgress":[
		{
			"id": "campaign-1",
			"name": "Campaign 1",
			"game": {
				"id": "game-1",
				"name": "Game 1",
				"slug": "game-1",
				"boxArtURL": "https://static-cdn.jtvnw.net/ttv-boxart/game-1-285x380.jpg"
			},
			"timeBasedDrops": [
				{
					"id": "drop-1",
					"name": "Drop 1",
					"requiredMinutesWatched": 60,
					"benefitEdges": [
						{
							"benefit": {
								"id": "benefit-1",
								"name": "Benefit 1",
								"imageAssetURL": "https://static-cdn.jtvnw.net/twitch-quests-assets/REWARD/drop-1.png"
							}
						}
					],
					"self": {
						"currentMinutesWatched": 45,
						"hasPreconditionsMet": true,
						"dropInstanceID": null,
						"isClaimed": false
					}
				},
				{
					"id": "drop-2",
					"name": "Drop 2",
					"requiredMinutesWatched": 60,
					"self": {
						"currentMinutesWatched": 60,
						"hasPreconditionsMet": true,
						"dropInstanceID": "instance-2",
						"isClaimed": false
					}
				}
			]
		}
	]}}}`

	client := &recordingGQLClient{
		response: gql.Response{
			Data: json.RawMessage(mockResponse),
		},
	}
	inventoryClient := Client{Client: client}

	drops, err := inventoryClient.GetInventory(context.Background())
	if err != nil {
		t.Fatalf("GetInventory returned error: %v", err)
	}

	if len(drops) != 2 {
		t.Fatalf("expected 2 drops, got %d", len(drops))
	}

	if drops[0].ID != "drop-1" || drops[0].CurrentMinutes != 45 || drops[0].IsClaimable || !drops[0].IsEarnable || drops[0].GameName != "Game 1" {
		t.Errorf("drop 0 incorrect: %+v", drops[0])
	}

	if drops[0].GameImageURL != "https://static-cdn.jtvnw.net/ttv-boxart/game-1.jpg" {
		t.Errorf("expected GameImageURL to be https://static-cdn.jtvnw.net/ttv-boxart/game-1.jpg, got %q", drops[0].GameImageURL)
	}

	if drops[0].ImageURL != "https://static-cdn.jtvnw.net/twitch-quests-assets/REWARD/drop-1.png" {
		t.Errorf("expected ImageURL to be https://static-cdn.jtvnw.net/twitch-quests-assets/REWARD/drop-1.png, got %q", drops[0].ImageURL)
	}

	if drops[1].ID != "drop-2" || drops[1].CurrentMinutes != 60 || !drops[1].IsClaimable || !drops[1].IsEarnable || drops[1].DropInstanceID != "instance-2" || drops[1].GameName != "Game 1" {
		t.Errorf("drop 1 incorrect: %+v", drops[1])
	}
}

func TestGetInventoryMarksExpiredDropNotEarnable(t *testing.T) {
	mockResponse := `{"currentUser":{"inventory":{"dropCampaignsInProgress":[
		{
			"id": "campaign-1",
			"name": "Campaign 1",
			"status": "EXPIRED",
			"startAt": "2020-01-01T00:00:00Z",
			"endAt": "2020-01-02T00:00:00Z",
			"game": {"id": "game-1", "name": "Game 1", "slug": "game-1"},
			"timeBasedDrops": [
				{
					"id": "drop-1",
					"name": "Drop 1",
					"startAt": "2020-01-01T00:00:00Z",
					"endAt": "2020-01-02T00:00:00Z",
					"requiredMinutesWatched": 60,
					"self": {
						"currentMinutesWatched": 45,
						"hasPreconditionsMet": true,
						"dropInstanceID": null,
						"isClaimed": false
					}
				}
			]
		}
	]}}}`

	client := &recordingGQLClient{response: gql.Response{Data: json.RawMessage(mockResponse)}}
	drops, err := Client{Client: client}.GetInventory(context.Background())
	if err != nil {
		t.Fatalf("GetInventory returned error: %v", err)
	}
	if len(drops) != 1 {
		t.Fatalf("expected 1 drop, got %d", len(drops))
	}
	if drops[0].IsEarnable {
		t.Fatalf("expired drop marked earnable: %+v", drops[0])
	}
}

func TestClaimDropSuccess(t *testing.T) {
	mockResponse := `{"claimDropRewards":{"status":"ELIGIBLE_FOR_ALL"}}`
	client := &recordingGQLClient{
		response: gql.Response{
			Data: json.RawMessage(mockResponse),
		},
	}
	inventoryClient := Client{Client: client}

	success, err := inventoryClient.ClaimDrop(context.Background(), "instance-2")
	if err != nil {
		t.Fatalf("ClaimDrop returned error: %v", err)
	}

	if !success {
		t.Errorf("expected success to be true")
	}

	if client.request.OperationName != claimDropOperation {
		t.Errorf("expected operation %s, got %s", claimDropOperation, client.request.OperationName)
	}
}

func TestClaimDropFailed(t *testing.T) {
	mockResponse := `{"claimDropRewards":{"status":"FAILED"}}`
	client := &recordingGQLClient{
		response: gql.Response{
			Data: json.RawMessage(mockResponse),
		},
	}
	inventoryClient := Client{Client: client}

	success, err := inventoryClient.ClaimDrop(context.Background(), "instance-2")
	if err == nil {
		t.Fatalf("expected error, got nil")
	}

	if success {
		t.Errorf("expected success to be false")
	}
}

func TestGetActiveCampaignGamesSkipsCompletedCampaigns(t *testing.T) {
	dashboard := []byte(`{
		"currentUser": {
			"dropCampaigns": [
				{"id": "marvel", "status": "ACTIVE", "game": {"displayName": "Marvel Rivals"}},
				{"id": "overwatch", "status": "ACTIVE", "game": {"displayName": "Overwatch"}}
			]
		}
	}`)
	completedMarvel := []byte(`{
		"user": {"dropCampaign": {
			"id": "marvel",
			"status": "ACTIVE",
			"self": {"isAccountConnected": true},
			"game": {"displayName": "Marvel Rivals"},
			"timeBasedDrops": [
				{"requiredMinutesWatched": 60, "self": {"hasPreconditionsMet": true, "isClaimed": true}}
			]
		}}
	}`)
	earnableOverwatch := []byte(`{
		"user": {"dropCampaign": {
			"id": "overwatch",
			"status": "ACTIVE",
			"self": {"isAccountConnected": true},
			"game": {"displayName": "Overwatch"},
			"timeBasedDrops": [
				{"requiredMinutesWatched": 60, "self": {"hasPreconditionsMet": true, "isClaimed": false}}
			]
		}}
	}`)

	client := campaignGamesGQLClient{
		dashboard: dashboard,
		details: map[string][]byte{
			"marvel":    completedMarvel,
			"overwatch": earnableOverwatch,
		},
	}
	games, unconnected, _, err := Client{Client: client, UserID: "805921782"}.GetActiveCampaignGames(context.Background())
	if err != nil {
		t.Fatalf("GetActiveCampaignGames returned error: %v", err)
	}

	expected := []string{"Overwatch"}
	if len(games) != len(expected) {
		t.Fatalf("expected %v, got %v", expected, games)
	}
	if len(unconnected) != 0 {
		t.Fatalf("expected 0 unconnected games, got %v", unconnected)
	}
	for i := range expected {
		if games[i] != expected[i] {
			t.Fatalf("expected %v, got %v", expected, games)
		}
	}
}

func TestGetActiveCampaignGamesAllowsNilPreconditions(t *testing.T) {
	dashboard := []byte(`{
		"currentUser": {
			"dropCampaigns": [
				{"id": "rocketleague", "status": "ACTIVE", "game": {"displayName": "Rocket League"}},
				{"id": "overwatch", "status": "ACTIVE", "game": {"displayName": "Overwatch"}}
			]
		}
	}`)
	nilPreconditionsRL := []byte(`{
		"user": {"dropCampaign": {
			"id": "rocketleague",
			"status": "ACTIVE",
			"self": {"isAccountConnected": true},
			"game": {"displayName": "Rocket League"},
			"timeBasedDrops": [
				{"requiredMinutesWatched": 60, "self": {"isClaimed": false}}
			]
		}}
	}`)
	earnableOverwatch := []byte(`{
		"user": {"dropCampaign": {
			"id": "overwatch",
			"status": "ACTIVE",
			"self": {"isAccountConnected": true},
			"game": {"displayName": "Overwatch"},
			"timeBasedDrops": [
				{"requiredMinutesWatched": 60, "self": {"hasPreconditionsMet": true, "isClaimed": false}}
			]
		}}
	}`)

	client := campaignGamesGQLClient{
		dashboard: dashboard,
		details: map[string][]byte{
			"rocketleague": nilPreconditionsRL,
			"overwatch":    earnableOverwatch,
		},
	}
	games, unconnected, _, err := Client{Client: client, UserID: "805921782"}.GetActiveCampaignGames(context.Background())
	if err != nil {
		t.Fatalf("GetActiveCampaignGames returned error: %v", err)
	}

	expected := []string{"Rocket League", "Overwatch"}
	if len(games) != len(expected) {
		t.Fatalf("expected %v, got %v", expected, games)
	}
	if len(unconnected) != 0 {
		t.Fatalf("expected 0 unconnected games, got %v", unconnected)
	}
}

func TestGetInventoryNilPreconditionsNotEarnable(t *testing.T) {
	mockResponse := `{"currentUser":{"inventory":{"dropCampaignsInProgress":[
		{
			"id": "campaign-1",
			"name": "Campaign 1",
			"status": "ACTIVE",
			"startAt": "2099-01-01T00:00:00Z",
			"endAt": "2099-12-31T00:00:00Z",
			"game": {"id": "game-1", "name": "Game 1", "slug": "game-1"},
			"timeBasedDrops": [
				{
					"id": "drop-1",
					"name": "Drop 1",
					"startAt": "2099-01-01T00:00:00Z",
					"endAt": "2099-12-31T00:00:00Z",
					"requiredMinutesWatched": 60,
					"self": {
						"currentMinutesWatched": 31,
						"dropInstanceID": null,
						"isClaimed": false
					}
				}
			]
		}
	]}}}`

	client := &recordingGQLClient{response: gql.Response{Data: json.RawMessage(mockResponse)}}
	drops, err := Client{Client: client}.GetInventory(context.Background())
	if err != nil {
		t.Fatalf("GetInventory returned error: %v", err)
	}
	if len(drops) != 1 {
		t.Fatalf("expected 1 drop, got %d", len(drops))
	}
	if drops[0].IsEarnable {
		t.Fatalf("drop with nil hasPreconditionsMet should not be earnable: %+v", drops[0])
	}
}

func TestClientGraphQLRequired(t *testing.T) {
	_, err := Client{}.GetInventory(context.Background())
	if err == nil {
		t.Errorf("expected error from nil client")
	}
}

func TestGetActiveCampaignGamesSkipsGlobalClaimedCampaigns(t *testing.T) {
	dashboard := []byte(`{
		"currentUser": {
			"dropCampaigns": [
				{"id": "rocketleague", "status": "ACTIVE", "game": {"displayName": "Rocket League"}}
			]
		}
	}`)
	rlDetails := []byte(`{
		"user": {"dropCampaign": {
			"id": "rocketleague",
			"status": "ACTIVE",
			"startAt": "2026-06-26T16:00:00Z",
			"endAt": "2026-06-29T03:59:59.999Z",
			"self": {"isAccountConnected": true},
			"game": {"displayName": "Rocket League"},
			"timeBasedDrops": [
				{
					"startAt": "2026-06-26T16:00:00Z",
					"endAt": "2026-06-29T03:59:59.999Z",
					"requiredMinutesWatched": 60,
					"benefitEdges": [
						{"benefit": {"name": "RLCS 2025 Exotic Drop"}}
					],
					"self": {"hasPreconditionsMet": true, "isClaimed": false}
				}
			]
		}}
	}`)
	inventoryResp := []byte(`{
		"currentUser": {
			"inventory": {
				"gameEventDrops": [
					{"name": "RLCS 2025 Exotic Drop", "lastAwardedAt": "2026-06-26T21:31:01Z"}
				]
			}
		}
	}`)

	client := campaignGamesGQLClient{
		dashboard: dashboard,
		details: map[string][]byte{
			"rocketleague": rlDetails,
		},
		inventory: inventoryResp,
	}
	games, unconnected, _, err := Client{Client: client, UserID: "805921782"}.GetActiveCampaignGames(context.Background())
	if err != nil {
		t.Fatalf("GetActiveCampaignGames returned error: %v", err)
	}

	if len(games) != 0 || len(unconnected) != 0 {
		t.Fatalf("expected 0 games, got connected:%v unconnected:%v", games, unconnected)
	}
}

