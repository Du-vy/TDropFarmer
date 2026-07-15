package inventory

import (
	"context"
	"encoding/json"
	"fmt"
	"testing"
	"time"

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

func TestGetInventoryMarksUnfinishableDropNotEarnable(t *testing.T) {
	now := time.Now().UTC()
	// Campaign ends in 30 minutes; the second drop still needs 110 minutes and
	// can no longer be completed, while the first only needs 5 more.
	endAt := now.Add(30 * time.Minute).Format(time.RFC3339)
	startAt := now.Add(-24 * time.Hour).Format(time.RFC3339)
	mockResponse := `{"currentUser":{"inventory":{"dropCampaignsInProgress":[
		{
			"id": "campaign-1",
			"name": "Campaign 1",
			"status": "ACTIVE",
			"startAt": "` + startAt + `",
			"endAt": "` + endAt + `",
			"game": {"id": "game-1", "name": "Game 1", "slug": "game-1"},
			"timeBasedDrops": [
				{
					"id": "drop-finishable",
					"name": "Finishable",
					"requiredMinutesWatched": 60,
					"self": {"currentMinutesWatched": 55, "hasPreconditionsMet": true, "dropInstanceID": null, "isClaimed": false}
				},
				{
					"id": "drop-doomed",
					"name": "Doomed",
					"requiredMinutesWatched": 120,
					"self": {"currentMinutesWatched": 10, "hasPreconditionsMet": true, "dropInstanceID": null, "isClaimed": false}
				}
			]
		}
	]}}}`

	client := &recordingGQLClient{response: gql.Response{Data: json.RawMessage(mockResponse)}}
	drops, err := Client{Client: client}.GetInventory(context.Background())
	if err != nil {
		t.Fatalf("GetInventory returned error: %v", err)
	}
	if len(drops) != 2 {
		t.Fatalf("expected 2 drops, got %d", len(drops))
	}
	if !drops[0].IsEarnable {
		t.Errorf("finishable drop marked not earnable: %+v", drops[0])
	}
	if drops[1].IsEarnable {
		t.Errorf("unfinishable drop marked earnable: %+v", drops[1])
	}
	if drops[0].EndsAt.IsZero() || drops[1].EndsAt.IsZero() {
		t.Errorf("expected EndsAt to be populated, got %v and %v", drops[0].EndsAt, drops[1].EndsAt)
	}
}

func TestDropCompletable(t *testing.T) {
	now := time.Now().UTC()
	tests := []struct {
		name string
		drop Drop
		want bool
	}{
		{name: "no deadline", drop: Drop{RequiredMinutes: 600}, want: true},
		{name: "fits with buffer", drop: Drop{RequiredMinutes: 60, EndsAt: now.Add(2 * time.Hour)}, want: true},
		{name: "does not fit", drop: Drop{RequiredMinutes: 120, EndsAt: now.Add(time.Hour)}, want: false},
		{name: "progress makes it fit", drop: Drop{RequiredMinutes: 120, CurrentMinutes: 80, EndsAt: now.Add(time.Hour)}, want: true},
		{name: "inside safety buffer", drop: Drop{RequiredMinutes: 55, EndsAt: now.Add(time.Hour)}, want: false},
		{name: "fully watched near deadline", drop: Drop{RequiredMinutes: 60, CurrentMinutes: 60, EndsAt: now.Add(time.Minute)}, want: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.drop.Completable(now); got != tt.want {
				t.Fatalf("Completable() = %v, want %v (drop %+v)", got, tt.want, tt.drop)
			}
		})
	}
}

func TestGetActiveCampaignGamesSkipsUncompletableDropEntries(t *testing.T) {
	now := time.Now().UTC()
	startAt := now.Add(-time.Hour).Format(time.RFC3339)
	endAt := now.Add(time.Hour).Format(time.RFC3339)
	dashboard := []byte(`{"currentUser":{"dropCampaigns":[{"id":"camp","status":"ACTIVE","game":{"displayName":"Game 1"}}]}}`)
	details := map[string][]byte{
		"camp": []byte(`{"user":{"dropCampaign":{
			"id": "camp",
			"name": "Campaign",
			"status": "ACTIVE",
			"startAt": "` + startAt + `",
			"endAt": "` + endAt + `",
			"self": {"isAccountConnected": true},
			"game": {"displayName": "Game 1"},
			"timeBasedDrops": [
				{"id": "quick", "requiredMinutesWatched": 30, "self": {"hasPreconditionsMet": true, "isClaimed": false}},
				{"id": "doomed", "requiredMinutesWatched": 120, "self": {"hasPreconditionsMet": true, "isClaimed": false}}
			]
		}}}`),
	}
	client := campaignGamesGQLClient{dashboard: dashboard, details: details}

	connected, _, allDrops, err := Client{Client: client, UserID: "viewer"}.GetActiveCampaignGames(context.Background())
	if err != nil {
		t.Fatalf("GetActiveCampaignGames returned error: %v", err)
	}
	if len(connected) != 1 || connected[0] != "Game 1" {
		t.Fatalf("expected campaign with a viable drop to stay, got %v", connected)
	}
	if len(allDrops) != 1 || allDrops[0].ID != "quick" {
		t.Fatalf("expected only the completable drop entry, got %+v", allDrops)
	}
	if allDrops[0].EndsAt.IsZero() {
		t.Fatalf("expected EndsAt to be populated on campaign drops, got %+v", allDrops[0])
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

func TestGetActiveCampaignGamesSkipsCampaignWithoutCompletableDrops(t *testing.T) {
	now := time.Now().UTC()
	startAt := now.Add(-time.Hour).Format(time.RFC3339Nano)
	endAt := now.Add(30 * time.Minute).Format(time.RFC3339Nano)
	dashboard := []byte(`{
		"currentUser": {
			"dropCampaigns": [
				{"id": "late", "status": "ACTIVE", "game": {"displayName": "Too Late"}}
			]
		}
	}`)
	details := []byte(fmt.Sprintf(`{
		"user": {"dropCampaign": {
			"id": "late",
			"status": "ACTIVE",
			"startAt": %q,
			"endAt": %q,
			"self": {"isAccountConnected": true},
			"game": {"displayName": "Too Late"},
			"timeBasedDrops": [
				{"requiredMinutesWatched": 60, "self": {"hasPreconditionsMet": true, "isClaimed": false}}
			]
		}}
	}`, startAt, endAt))

	client := campaignGamesGQLClient{
		dashboard: dashboard,
		details: map[string][]byte{
			"late": details,
		},
	}
	games, unconnected, _, err := Client{Client: client, UserID: "805921782"}.GetActiveCampaignGames(context.Background())
	if err != nil {
		t.Fatalf("GetActiveCampaignGames returned error: %v", err)
	}

	if len(games) != 0 || len(unconnected) != 0 {
		t.Fatalf("expected no games, got connected:%v unconnected:%v", games, unconnected)
	}
}

func TestGetActiveCampaignGamesAllowsCampaignWithCompletableDropWindow(t *testing.T) {
	now := time.Now().UTC()
	startAt := now.Add(-time.Hour).Format(time.RFC3339Nano)
	soonEndAt := now.Add(30 * time.Minute).Format(time.RFC3339Nano)
	laterEndAt := now.Add(3 * time.Hour).Format(time.RFC3339Nano)
	dashboard := []byte(`{
		"currentUser": {
			"dropCampaigns": [
				{"id": "bdo", "status": "ACTIVE", "game": {"displayName": "Black Desert"}}
			]
		}
	}`)
	details := []byte(fmt.Sprintf(`{
		"user": {"dropCampaign": {
			"id": "bdo",
			"status": "ACTIVE",
			"startAt": %q,
			"endAt": %q,
			"self": {"isAccountConnected": true},
			"game": {"displayName": "Black Desert"},
			"timeBasedDrops": [
				{
					"startAt": %q,
					"endAt": %q,
					"requiredMinutesWatched": 60,
					"self": {"hasPreconditionsMet": true, "isClaimed": false}
				},
				{
					"startAt": %q,
					"endAt": %q,
					"requiredMinutesWatched": 60,
					"self": {"hasPreconditionsMet": true, "isClaimed": false}
				}
			]
		}}
	}`, startAt, laterEndAt, startAt, soonEndAt, startAt, laterEndAt))

	client := campaignGamesGQLClient{
		dashboard: dashboard,
		details: map[string][]byte{
			"bdo": details,
		},
	}
	games, unconnected, _, err := Client{Client: client, UserID: "805921782"}.GetActiveCampaignGames(context.Background())
	if err != nil {
		t.Fatalf("GetActiveCampaignGames returned error: %v", err)
	}

	if len(games) != 1 || games[0] != "Black Desert" {
		t.Fatalf("expected Black Desert, got connected:%v unconnected:%v", games, unconnected)
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

func TestGetActiveCampaignGamesSkipsIgnoredGames(t *testing.T) {
	dashboard := []byte(`{
		"currentUser": {
			"dropCampaigns": [
				{"id": "specialevents", "status": "ACTIVE", "game": {"displayName": "Special Events"}},
				{"id": "overwatch", "status": "ACTIVE", "game": {"displayName": "Overwatch"}}
			]
		}
	}`)
	specialEventsDetails := []byte(`{
		"user": {"dropCampaign": {
			"id": "specialevents",
			"status": "ACTIVE",
			"self": {"isAccountConnected": true},
			"game": {"displayName": "Special Events"},
			"timeBasedDrops": [
				{"requiredMinutesWatched": 60, "self": {"hasPreconditionsMet": true, "isClaimed": false}}
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
			"specialevents": specialEventsDetails,
			"overwatch":     earnableOverwatch,
		},
	}
	games, unconnected, _, err := Client{
		Client:       client,
		UserID:       "805921782",
		IgnoredGames: []string{"Special Events"},
	}.GetActiveCampaignGames(context.Background())
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
	if games[0] != "Overwatch" {
		t.Fatalf("expected Overwatch, got %v", games[0])
	}
}

type failingDetailsGQLClient struct {
	campaignGamesGQLClient
	failIDs map[string]bool
}

func TestGetInventoryDoesNotTreatSubscriptionDropAsWatchEarnable(t *testing.T) {
	mockResponse := `{"currentUser":{"inventory":{"dropCampaignsInProgress":[
		{
			"id":"subscription-campaign",
			"name":"Jubilee Badge",
			"status":"ACTIVE",
			"game":{"name":"Marvel Rivals"},
			"timeBasedDrops":[{
				"id":"subscription-drop",
				"name":"Jubilee Badge",
				"requiredMinutesWatched":0,
				"requiredSubs":1,
				"self":{"currentMinutesWatched":0,"hasPreconditionsMet":true,"dropInstanceID":null,"isClaimed":false}
			}]
		}
	]}}}`

	client := &recordingGQLClient{response: gql.Response{Data: json.RawMessage(mockResponse)}}
	drops, err := Client{Client: client}.GetInventory(context.Background())
	if err != nil {
		t.Fatalf("GetInventory returned error: %v", err)
	}
	if len(drops) != 1 {
		t.Fatalf("expected one subscription drop, got %d", len(drops))
	}
	if drops[0].IsEarnable {
		t.Fatalf("subscription drop marked watch-earnable: %+v", drops[0])
	}
}

func (c failingDetailsGQLClient) Do(ctx context.Context, req gql.Request) (gql.Response, error) {
	if req.OperationName == campaignDetailsOperation {
		if id, _ := req.Variables["dropID"].(string); c.failIDs[id] {
			return gql.Response{}, fmt.Errorf("details request failed for %s", id)
		}
	}
	return c.campaignGamesGQLClient.Do(ctx, req)
}

func TestGetActiveCampaignGamesSkipsCampaignWithFailedDetails(t *testing.T) {
	dashboard := []byte(`{
		"currentUser": {
			"dropCampaigns": [
				{"id": "flaky", "status": "ACTIVE", "game": {"displayName": "Flaky Game"}},
				{"id": "overwatch", "status": "ACTIVE", "game": {"displayName": "Overwatch"}}
			]
		}
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

	client := failingDetailsGQLClient{
		campaignGamesGQLClient: campaignGamesGQLClient{
			dashboard: dashboard,
			details: map[string][]byte{
				"overwatch": earnableOverwatch,
			},
		},
		failIDs: map[string]bool{"flaky": true},
	}
	games, unconnected, _, err := Client{Client: client, UserID: "805921782"}.GetActiveCampaignGames(context.Background())
	if err != nil {
		t.Fatalf("GetActiveCampaignGames returned error: %v", err)
	}

	if len(games) != 1 || games[0] != "Overwatch" {
		t.Fatalf("expected [Overwatch], got %v", games)
	}
	if len(unconnected) != 0 {
		t.Fatalf("expected 0 unconnected games, got %v", unconnected)
	}
}

func TestGetActiveCampaignGamesStopsOnContextCancel(t *testing.T) {
	dashboard := []byte(`{
		"currentUser": {
			"dropCampaigns": [
				{"id": "one", "status": "ACTIVE", "game": {"displayName": "Game One"}},
				{"id": "two", "status": "ACTIVE", "game": {"displayName": "Game Two"}}
			]
		}
	}`)

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	client := campaignGamesGQLClient{dashboard: dashboard}
	_, _, _, err := Client{Client: client, UserID: "805921782"}.GetActiveCampaignGames(ctx)
	if err == nil {
		t.Fatalf("GetActiveCampaignGames returned nil error, want context error")
	}
}
