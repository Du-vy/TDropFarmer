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

func TestGetInventory(t *testing.T) {
	mockResponse := `{"currentUser":{"inventory":{"dropCampaignsInProgress":[
		{
			"id": "campaign-1",
			"name": "Campaign 1",
			"timeBasedDrops": [
				{
					"id": "drop-1",
					"name": "Drop 1",
					"requiredMinutesWatched": 60,
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

	if drops[0].ID != "drop-1" || drops[0].CurrentMinutes != 45 || drops[0].IsClaimable {
		t.Errorf("drop 0 incorrect: %+v", drops[0])
	}

	if drops[1].ID != "drop-2" || drops[1].CurrentMinutes != 60 || !drops[1].IsClaimable || drops[1].DropInstanceID != "instance-2" {
		t.Errorf("drop 1 incorrect: %+v", drops[1])
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

func TestClientGraphQLRequired(t *testing.T) {
	_, err := Client{}.GetInventory(context.Background())
	if err == nil {
		t.Errorf("expected error from nil client")
	}
}
