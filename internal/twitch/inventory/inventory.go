package inventory

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/Du-vy/TDropFarmer/internal/twitch/gql"
)

const (
	inventoryOperation = "Inventory"
	inventoryHash      = "d86775d0ef16a63a33ad52e80eaff963b2d5b72fada7c991504a57496e1d8e4b"

	claimDropOperation = "DropsPage_ClaimDropRewards"
	claimDropHash      = "a455deea71bdc9015b78eb49f4acfbce8baa7ccbedd28e549bb025bd0f751930"
)

type GQLClient interface {
	Do(context.Context, gql.Request) (gql.Response, error)
}

type Client struct {
	Client GQLClient
}

type Drop struct {
	ID              string
	Name            string
	CampaignID      string
	CampaignName    string
	RequiredMinutes int
	CurrentMinutes  int
	DropInstanceID  string
	IsClaimed       bool
	IsClaimable     bool
}

func (c Client) GetInventory(ctx context.Context) ([]Drop, error) {
	if c.Client == nil {
		return nil, fmt.Errorf("graphql client is required")
	}

	response, err := c.Client.Do(ctx, gql.Request{
		OperationName: inventoryOperation,
		Variables: map[string]any{
			"fetchRewardCampaigns": true,
		},
		Extensions: persistedQuery(inventoryHash),
	})
	if err != nil {
		return nil, err
	}

	var data inventoryResponse
	if err := json.Unmarshal(response.Data, &data); err != nil {
		return nil, fmt.Errorf("decode inventory response: %w", err)
	}

	if data.CurrentUser == nil {
		return nil, nil
	}

	var drops []Drop
	for _, campaign := range data.CurrentUser.Inventory.DropCampaignsInProgress {
		for _, td := range campaign.TimeBasedDrops {
			var dropInstanceID string
			if td.Self.DropInstanceID != nil {
				dropInstanceID = *td.Self.DropInstanceID
			}

			isClaimable := !td.Self.IsClaimed && dropInstanceID != ""

			drops = append(drops, Drop{
				ID:              td.ID,
				Name:            td.Name,
				CampaignID:      campaign.ID,
				CampaignName:    campaign.Name,
				RequiredMinutes: td.RequiredMinutesWatched,
				CurrentMinutes:  td.Self.CurrentMinutesWatched,
				DropInstanceID:  dropInstanceID,
				IsClaimed:       td.Self.IsClaimed,
				IsClaimable:     isClaimable,
			})
		}
	}

	return drops, nil
}

func (c Client) ClaimDrop(ctx context.Context, dropInstanceID string) (bool, error) {
	if c.Client == nil {
		return false, fmt.Errorf("graphql client is required")
	}
	if dropInstanceID == "" {
		return false, fmt.Errorf("drop instance id is required")
	}

	response, err := c.Client.Do(ctx, gql.Request{
		OperationName: claimDropOperation,
		Variables: map[string]any{
			"input": map[string]any{
				"dropInstanceID": dropInstanceID,
			},
		},
		Extensions: persistedQuery(claimDropHash),
	})
	if err != nil {
		return false, err
	}

	var data struct {
		ClaimDropRewards *struct {
			Status string `json:"status"`
		} `json:"claimDropRewards"`
	}
	if err := json.Unmarshal(response.Data, &data); err != nil {
		return false, fmt.Errorf("decode claim drop response: %w", err)
	}

	if data.ClaimDropRewards == nil {
		return false, fmt.Errorf("claim response missing claimDropRewards payload")
	}

	status := data.ClaimDropRewards.Status
	if status == "ELIGIBLE_FOR_ALL" || status == "DROP_INSTANCE_ALREADY_CLAIMED" {
		return true, nil
	}

	return false, fmt.Errorf("claim drop failed with status: %s", status)
}

func persistedQuery(hash string) map[string]any {
	return map[string]any{
		"persistedQuery": map[string]any{
			"version":    1,
			"sha256Hash": hash,
		},
	}
}

type inventoryResponse struct {
	CurrentUser *struct {
		Inventory struct {
			DropCampaignsInProgress []struct {
				ID             string `json:"id"`
				Name           string `json:"name"`
				TimeBasedDrops []struct {
					ID                     string `json:"id"`
					Name                   string `json:"name"`
					RequiredMinutesWatched int    `json:"requiredMinutesWatched"`
					Self                   struct {
						CurrentMinutesWatched int     `json:"currentMinutesWatched"`
						HasPreconditionsMet   bool    `json:"hasPreconditionsMet"`
						DropInstanceID        *string `json:"dropInstanceID"`
						IsClaimed             bool    `json:"isClaimed"`
					} `json:"self"`
				} `json:"timeBasedDrops"`
			} `json:"dropCampaignsInProgress"`
		} `json:"inventory"`
	} `json:"currentUser"`
}
