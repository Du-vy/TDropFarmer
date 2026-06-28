package inventory

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/Du-vy/TDropFarmer/internal/twitch/gql"
)

const (
	inventoryOperation = "Inventory"
	inventoryHash      = "d86775d0ef16a63a33ad52e80eaff963b2d5b72fada7c991504a57496e1d8e4b"

	claimDropOperation = "DropsPage_ClaimDropRewards"
	claimDropHash      = "a455deea71bdc9015b78eb49f4acfbce8baa7ccbedd28e549bb025bd0f751930"

	viewerCampaignsOperation = "ViewerDropsDashboard"
	viewerCampaignsHash      = "5a4da2ab3d5b47c9f9ce864e727b2cb346af1e3ea8b897fe8f704a97ff017619"

	campaignDetailsOperation = "DropCampaignDetails"
	campaignDetailsHash      = "039277bf98f3130929262cc7c6efd9c141ca3749cb6dca442fc8ead9a53f77c1"
)

type GQLClient interface {
	Do(context.Context, gql.Request) (gql.Response, error)
}

type Client struct {
	Client GQLClient
	UserID string
	Logger *slog.Logger
}

type Drop struct {
	ID              string
	Name            string
	CampaignID      string
	CampaignName    string
	GameName        string
	RequiredMinutes int
	CurrentMinutes  int
	DropInstanceID  string
	IsClaimed       bool
	IsClaimable     bool
	IsEarnable      bool
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

	now := time.Now().UTC()
	var drops []Drop
	for _, campaign := range data.CurrentUser.Inventory.DropCampaignsInProgress {
		for _, td := range campaign.TimeBasedDrops {
			var dropInstanceID string
			if td.Self.DropInstanceID != nil {
				dropInstanceID = *td.Self.DropInstanceID
			}

			isClaimable := !td.Self.IsClaimed && dropInstanceID != ""
			preconditionsMet := td.Self.HasPreconditionsMet != nil && *td.Self.HasPreconditionsMet
			isEarnable := !td.Self.IsClaimed && preconditionsMet && campaignDropActive(now, campaign.Status, campaign.StartAt, campaign.EndAt, td.StartAt, td.EndAt)

			drops = append(drops, Drop{
				ID:              td.ID,
				Name:            td.Name,
				CampaignID:      campaign.ID,
				CampaignName:    campaign.Name,
				GameName:        campaign.Game.Name,
				RequiredMinutes: td.RequiredMinutesWatched,
				CurrentMinutes:  td.Self.CurrentMinutesWatched,
				DropInstanceID:  dropInstanceID,
				IsClaimed:       td.Self.IsClaimed,
				IsClaimable:     isClaimable,
				IsEarnable:      isEarnable,
			})
		}
	}

	return drops, nil
}

func campaignDropActive(now time.Time, status, campaignStart, campaignEnd, dropStart, dropEnd string) bool {
	if status != "" && status != "ACTIVE" {
		return false
	}
	if !withinWindow(now, campaignStart, campaignEnd) {
		return false
	}
	return withinWindow(now, dropStart, dropEnd)
}

func withinWindow(now time.Time, start, end string) bool {
	if start != "" {
		startAt, err := parseTwitchTime(start)
		if err == nil && now.Before(startAt) {
			return false
		}
	}
	if end != "" {
		endAt, err := parseTwitchTime(end)
		if err == nil && !now.Before(endAt) {
			return false
		}
	}
	return true
}

func parseTwitchTime(value string) (time.Time, error) {
	parsed, err := time.Parse(time.RFC3339Nano, value)
	if err != nil {
		return time.Time{}, err
	}
	return parsed.UTC(), nil
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
				ID      string `json:"id"`
				Name    string `json:"name"`
				Status  string `json:"status"`
				StartAt string `json:"startAt"`
				EndAt   string `json:"endAt"`
				Game    struct {
					ID   string `json:"id"`
					Name string `json:"name"`
					Slug string `json:"slug"`
				} `json:"game"`
				TimeBasedDrops []struct {
					ID                     string `json:"id"`
					Name                   string `json:"name"`
					StartAt                string `json:"startAt"`
					EndAt                  string `json:"endAt"`
					RequiredMinutesWatched int    `json:"requiredMinutesWatched"`
					Self                   struct {
						CurrentMinutesWatched int     `json:"currentMinutesWatched"`
						HasPreconditionsMet   *bool   `json:"hasPreconditionsMet"`
						DropInstanceID        *string `json:"dropInstanceID"`
						IsClaimed             bool    `json:"isClaimed"`
					} `json:"self"`
				} `json:"timeBasedDrops"`
			} `json:"dropCampaignsInProgress"`
		} `json:"inventory"`
	} `json:"currentUser"`
}

type viewerCampaignsResponse struct {
	CurrentUser *struct {
		DropCampaigns []struct {
			ID     string `json:"id"`
			Name   string `json:"name"`
			Status string `json:"status"` // ACTIVE, UPCOMING, EXPIRED
			Game   struct {
				ID          string `json:"id"`
				DisplayName string `json:"displayName"`
			} `json:"game"`
		} `json:"dropCampaigns"`
	} `json:"currentUser"`
}

type campaignDetailsResponse struct {
	User *struct {
		DropCampaign *struct {
			ID      string `json:"id"`
			Name    string `json:"name"`
			Status  string `json:"status"`
			StartAt string `json:"startAt"`
			EndAt   string `json:"endAt"`
			Self    struct {
				IsAccountConnected bool `json:"isAccountConnected"`
			} `json:"self"`
			Game struct {
				Name        string `json:"name"`
				DisplayName string `json:"displayName"`
			} `json:"game"`
			TimeBasedDrops []struct {
				ID                     string `json:"id"`
				StartAt                string `json:"startAt"`
				EndAt                  string `json:"endAt"`
				RequiredMinutesWatched int    `json:"requiredMinutesWatched"`
				BenefitEdges           []struct {
					Benefit struct {
						Name string `json:"name"`
					} `json:"benefit"`
				} `json:"benefitEdges"`
				Self                   struct {
					HasPreconditionsMet *bool `json:"hasPreconditionsMet"`
					IsClaimed           bool  `json:"isClaimed"`
				} `json:"self"`
			} `json:"timeBasedDrops"`
		} `json:"dropCampaign"`
	} `json:"user"`
}

func (c Client) getClaimedBenefits(ctx context.Context) (map[string]time.Time, error) {
	response, err := c.Client.Do(ctx, gql.Request{
		OperationName: inventoryOperation,
		Variables: map[string]any{
			"fetchRewardCampaigns": false,
		},
		Extensions: persistedQuery(inventoryHash),
	})
	if err != nil {
		return nil, err
	}

	var data struct {
		CurrentUser *struct {
			Inventory struct {
				GameEventDrops []struct {
					Name          string `json:"name"`
					LastAwardedAt string `json:"lastAwardedAt"`
				} `json:"gameEventDrops"`
			} `json:"inventory"`
		} `json:"currentUser"`
	}
	if err := json.Unmarshal(response.Data, &data); err != nil {
		return nil, err
	}

	claimed := make(map[string]time.Time)
	if data.CurrentUser != nil {
		for _, d := range data.CurrentUser.Inventory.GameEventDrops {
			if d.Name != "" && d.LastAwardedAt != "" {
				t, err := time.Parse(time.RFC3339Nano, d.LastAwardedAt)
				if err == nil {
					claimed[strings.ToLower(d.Name)] = t.UTC()
				}
			}
		}
	}
	return claimed, nil
}

func (c Client) GetActiveCampaignGames(ctx context.Context) ([]string, []string, error) {
	if c.Client == nil {
		return nil, nil, fmt.Errorf("graphql client is required")
	}
	if c.UserID == "" {
		return nil, nil, fmt.Errorf("user id is required")
	}

	response, err := c.Client.Do(ctx, gql.Request{
		OperationName: viewerCampaignsOperation,
		Variables: map[string]any{
			"fetchRewardCampaigns": false,
		},
		Extensions: persistedQuery(viewerCampaignsHash),
	})
	if err != nil {
		return nil, nil, err
	}

	var data viewerCampaignsResponse
	if err := json.Unmarshal(response.Data, &data); err != nil {
		return nil, nil, fmt.Errorf("decode campaigns response: %w", err)
	}

	if data.CurrentUser == nil {
		return nil, nil, nil
	}

	claimedBenefits, err := c.getClaimedBenefits(ctx)
	if err != nil {
		if c.Logger != nil {
			c.Logger.Warn("fetch claimed benefits failed, using empty list", slog.String("error", err.Error()))
		}
		claimedBenefits = make(map[string]time.Time)
	}

	seenConnected := make(map[string]bool)
	seenUnconnected := make(map[string]bool)
	var connectedGames []string
	var unconnectedGames []string

	for _, campaign := range data.CurrentUser.DropCampaigns {
		if campaign.Status != "ACTIVE" {
			continue
		}

		detail, err := c.getCampaignDetails(ctx, campaign.ID)
		if err != nil {
			return nil, nil, err
		}
		earnable, isConnected := campaignDetailEarnable(time.Now().UTC(), detail, claimedBenefits)
		if c.Logger != nil {
			var dropSummary []string
			if detail != nil && detail.User != nil && detail.User.DropCampaign != nil {
				for _, d := range detail.User.DropCampaign.TimeBasedDrops {
					benefitName := ""
					if len(d.BenefitEdges) > 0 {
						benefitName = d.BenefitEdges[0].Benefit.Name
					}
					claimed := d.Self.IsClaimed
					if benefitName != "" {
						if claimedAt, ok := claimedBenefits[strings.ToLower(benefitName)]; ok {
							if campaignStartAt, err := time.Parse(time.RFC3339Nano, detail.User.DropCampaign.StartAt); err == nil {
								if claimedAt.After(campaignStartAt.UTC()) {
									claimed = true
								}
							}
						}
					}
					preconditions := "nil"
					if d.Self.HasPreconditionsMet != nil {
						preconditions = fmt.Sprintf("%v", *d.Self.HasPreconditionsMet)
					}
					dropSummary = append(dropSummary, fmt.Sprintf("{min:%d,claimed:%v,preconditions:%s}", d.RequiredMinutesWatched, claimed, preconditions))
				}
			}
			c.Logger.Debug("campaign detail check",
				slog.String("campaign_id", campaign.ID),
				slog.String("game", campaign.Game.DisplayName),
				slog.Bool("earnable", earnable),
				slog.Bool("connected", isConnected),
				slog.String("drops", fmt.Sprintf("%v", dropSummary)),
			)
		}
		if !earnable {
			continue
		}

		name := campaignDetailGameName(detail)
		if name == "" {
			name = campaign.Game.DisplayName
		}
		key := strings.ToLower(name)
		if name != "" {
			if isConnected {
				if !seenConnected[key] {
					seenConnected[key] = true
					connectedGames = append(connectedGames, name)
				}
			} else {
				if !seenUnconnected[key] {
					seenUnconnected[key] = true
					unconnectedGames = append(unconnectedGames, name)
				}
			}
		}
	}

	return connectedGames, unconnectedGames, nil
}

func (c Client) getCampaignDetails(ctx context.Context, campaignID string) (*campaignDetailsResponse, error) {
	response, err := c.Client.Do(ctx, gql.Request{
		OperationName: campaignDetailsOperation,
		Variables: map[string]any{
			"channelLogin": c.UserID,
			"dropID":       campaignID,
		},
		Extensions: persistedQuery(campaignDetailsHash),
	})
	if err != nil {
		return nil, err
	}

	var data campaignDetailsResponse
	if err := json.Unmarshal(response.Data, &data); err != nil {
		return nil, fmt.Errorf("decode campaign details response: %w", err)
	}
	return &data, nil
}

func campaignDetailEarnable(now time.Time, data *campaignDetailsResponse, claimedBenefits map[string]time.Time) (bool, bool) {
	if data == nil || data.User == nil || data.User.DropCampaign == nil {
		return false, false
	}
	campaign := data.User.DropCampaign
	isConnected := campaign.Self.IsAccountConnected

	if !campaignDropActive(now, campaign.Status, campaign.StartAt, campaign.EndAt, "", "") {
		return false, isConnected
	}
	hasEarnableDrop := false
	for _, drop := range campaign.TimeBasedDrops {
		benefitName := ""
		if len(drop.BenefitEdges) > 0 {
			benefitName = drop.BenefitEdges[0].Benefit.Name
		}
		isClaimed := drop.Self.IsClaimed
		if benefitName != "" {
			if claimedAt, ok := claimedBenefits[strings.ToLower(benefitName)]; ok {
				if campaignStartAt, err := time.Parse(time.RFC3339Nano, campaign.StartAt); err == nil {
					if claimedAt.After(campaignStartAt.UTC()) {
						isClaimed = true
					}
				}
			}
		}

		if drop.RequiredMinutesWatched <= 0 || isClaimed {
			continue
		}
		preconditionsMet := true
		if drop.Self.HasPreconditionsMet != nil {
			preconditionsMet = *drop.Self.HasPreconditionsMet
		}
		if !preconditionsMet {
			continue
		}
		if campaignDropActive(now, campaign.Status, campaign.StartAt, campaign.EndAt, drop.StartAt, drop.EndAt) {
			hasEarnableDrop = true
		}
	}
	return hasEarnableDrop, isConnected
}

func campaignDetailGameName(data *campaignDetailsResponse) string {
	if data == nil || data.User == nil || data.User.DropCampaign == nil {
		return ""
	}
	game := data.User.DropCampaign.Game
	if game.DisplayName != "" {
		return game.DisplayName
	}
	return game.Name
}
