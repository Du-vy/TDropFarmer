package inventory

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"regexp"
	"strings"
	"time"

	"github.com/Du-vy/TDropFarmer/internal/twitch/gql"
)

const (
	inventoryOperation = "Inventory"
	inventoryHash      = "8337eb8541b314040b0edde0c09c5c7a2783ba1960aa9edfbf3bac16d0fec404"

	claimDropOperation = "DropsPage_ClaimDropRewards"
	claimDropHash      = "a455deea71bdc9015b78eb49f4acfbce8baa7ccbedd28e549bb025bd0f751930"

	viewerCampaignsOperation = "ViewerDropsDashboard"
	viewerCampaignsHash      = "d9cae7761dafab85908c85e6683cb4201b449e66ac3bb5e894f15ff12aeafaa7"

	campaignDetailsOperation = "DropCampaignDetails"
	campaignDetailsHash      = "039277bf98f3130929262cc7c6efd9c141ca3749cb6dca442fc8ead9a53f77c1"

	// Leave room for discovery, scheduling, and watch telemetry delays.
	dropCompletionSafetyBuffer = 10 * time.Minute

	// Pause between per-campaign detail requests so a long campaign list does
	// not burst dozens of GQL calls at once.
	campaignDetailsRequestDelay = 250 * time.Millisecond
)

type GQLClient interface {
	Do(context.Context, gql.Request) (gql.Response, error)
}

type Client struct {
	Client       GQLClient
	UserID       string
	Logger       *slog.Logger
	IgnoredGames []string
}

func (c Client) isIgnored(gameName string) bool {
	for _, ig := range c.IgnoredGames {
		if strings.EqualFold(ig, gameName) {
			return true
		}
	}
	return false
}

type Drop struct {
	ID              string
	Name            string
	CampaignID      string
	CampaignName    string
	GameName        string
	GameImageURL    string
	ImageURL        string
	RequiredMinutes int
	CurrentMinutes  int
	DropInstanceID  string
	IsClaimed       bool
	IsClaimable     bool
	IsEarnable      bool
	// EndsAt is the earliest known deadline (campaign or drop end). Zero when
	// Twitch did not report one.
	EndsAt time.Time
}

// Completable reports whether the drop's remaining watch time still fits
// before its deadline, keeping the completion safety buffer. Drops without a
// known deadline or without remaining watch time are always completable.
func (d Drop) Completable(now time.Time) bool {
	if d.EndsAt.IsZero() {
		return true
	}
	remaining := d.RequiredMinutes - d.CurrentMinutes
	if remaining <= 0 {
		return true
	}
	required := time.Duration(remaining)*time.Minute + dropCompletionSafetyBuffer
	return d.EndsAt.Sub(now) >= required
}

var dimsRegex = regexp.MustCompile(`-\d+x\d+(\.(?:jpg|png|gif|jpeg|webp))$`)

func cleanTwitchImageURL(url string) string {
	return dimsRegex.ReplaceAllString(url, "$1")
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
		if c.isIgnored(campaign.Game.Name) {
			continue
		}
		gameImageURL := cleanTwitchImageURL(campaign.Game.BoxArtURL)

		for _, td := range campaign.TimeBasedDrops {
			var dropInstanceID string
			if td.Self.DropInstanceID != nil {
				dropInstanceID = *td.Self.DropInstanceID
			}

			isClaimable := !td.Self.IsClaimed && dropInstanceID != ""
			preconditionsMet := td.Self.HasPreconditionsMet != nil && *td.Self.HasPreconditionsMet
			isEarnable := !td.Self.IsClaimed && td.RequiredMinutesWatched > 0 && td.RequiredSubs == 0 && preconditionsMet && campaignDropActive(now, campaign.Status, campaign.StartAt, campaign.EndAt, td.StartAt, td.EndAt)

			var imageURL string
			if len(td.BenefitEdges) > 0 {
				imageURL = td.BenefitEdges[0].Benefit.ImageAssetURL
			}

			endsAt, _ := earliestDropDeadline(campaign.EndAt, td.EndAt)
			drop := Drop{
				ID:              td.ID,
				Name:            td.Name,
				CampaignID:      campaign.ID,
				CampaignName:    campaign.Name,
				GameName:        campaign.Game.Name,
				GameImageURL:    gameImageURL,
				ImageURL:        imageURL,
				RequiredMinutes: td.RequiredMinutesWatched,
				CurrentMinutes:  td.Self.CurrentMinutesWatched,
				DropInstanceID:  dropInstanceID,
				IsClaimed:       td.Self.IsClaimed,
				IsClaimable:     isClaimable,
				EndsAt:          endsAt,
			}
			// A drop whose remaining minutes no longer fit before the deadline
			// is a wasted watch target, even if its window is still open.
			drop.IsEarnable = isEarnable && drop.Completable(now)
			drops = append(drops, drop)
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
					ID        string `json:"id"`
					Name      string `json:"name"`
					Slug      string `json:"slug"`
					BoxArtURL string `json:"boxArtURL"`
				} `json:"game"`
				TimeBasedDrops []struct {
					ID                     string `json:"id"`
					Name                   string `json:"name"`
					StartAt                string `json:"startAt"`
					EndAt                  string `json:"endAt"`
					RequiredMinutesWatched int    `json:"requiredMinutesWatched"`
					RequiredSubs           int    `json:"requiredSubs"`
					BenefitEdges           []struct {
						Benefit struct {
							ID            string `json:"id"`
							Name          string `json:"name"`
							ImageAssetURL string `json:"imageAssetURL"`
						} `json:"benefit"`
					} `json:"benefitEdges"`
					Self struct {
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
				BoxArtURL   string `json:"boxArtURL"`
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
				BoxArtURL   string `json:"boxArtURL"`
			} `json:"game"`
			TimeBasedDrops []struct {
				ID                     string `json:"id"`
				StartAt                string `json:"startAt"`
				EndAt                  string `json:"endAt"`
				RequiredMinutesWatched int    `json:"requiredMinutesWatched"`
				RequiredSubs           int    `json:"requiredSubs"`
				BenefitEdges           []struct {
					Benefit struct {
						Name          string `json:"name"`
						ImageAssetURL string `json:"imageAssetURL"`
					} `json:"benefit"`
				} `json:"benefitEdges"`
				Self struct {
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

func (c Client) GetActiveCampaignGames(ctx context.Context) ([]string, []string, []Drop, error) {
	if c.Client == nil {
		return nil, nil, nil, fmt.Errorf("graphql client is required")
	}
	if c.UserID == "" {
		return nil, nil, nil, fmt.Errorf("user id is required")
	}

	response, err := c.Client.Do(ctx, gql.Request{
		OperationName: viewerCampaignsOperation,
		Variables: map[string]any{
			"fetchRewardCampaigns": false,
		},
		Extensions: persistedQuery(viewerCampaignsHash),
	})
	if err != nil {
		return nil, nil, nil, err
	}

	var data viewerCampaignsResponse
	if err := json.Unmarshal(response.Data, &data); err != nil {
		return nil, nil, nil, fmt.Errorf("decode campaigns response: %w", err)
	}

	if data.CurrentUser == nil {
		return nil, nil, nil, nil
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
	var allCampaignDrops []Drop

	firstDetail := true
	for _, campaign := range data.CurrentUser.DropCampaigns {
		if campaign.Status != "ACTIVE" {
			continue
		}
		if c.isIgnored(campaign.Game.DisplayName) {
			continue
		}

		if !firstDetail {
			select {
			case <-ctx.Done():
				return nil, nil, nil, ctx.Err()
			case <-time.After(campaignDetailsRequestDelay):
			}
		}
		firstDetail = false

		detail, err := c.getCampaignDetails(ctx, campaign.ID)
		if err != nil {
			if ctx.Err() != nil {
				return nil, nil, nil, ctx.Err()
			}
			// One flaky campaign must not abort the whole scan; skip it and
			// keep collecting the rest.
			if c.Logger != nil {
				c.Logger.Warn("fetch campaign details failed; skipping campaign",
					slog.String("campaign_id", campaign.ID),
					slog.String("game", campaign.Game.DisplayName),
					slog.String("error", err.Error()),
				)
			}
			continue
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

		if detail != nil && detail.User != nil && detail.User.DropCampaign != nil {
			cCampaign := detail.User.DropCampaign
			gameImageURL := cleanTwitchImageURL(campaign.Game.BoxArtURL)
			now := time.Now().UTC()
			for _, td := range cCampaign.TimeBasedDrops {
				if td.RequiredMinutesWatched <= 0 || td.RequiredSubs > 0 {
					continue
				}
				endsAt, _ := earliestDropDeadline(cCampaign.EndAt, td.EndAt)
				// Skip drops that can no longer be completed in the remaining
				// window; the campaign stays if a sibling drop is still viable.
				if !(Drop{RequiredMinutes: td.RequiredMinutesWatched, EndsAt: endsAt}).Completable(now) {
					continue
				}
				var imageURL string
				if len(td.BenefitEdges) > 0 {
					imageURL = td.BenefitEdges[0].Benefit.ImageAssetURL
				}
				isClaimed := td.Self.IsClaimed
				benefitName := ""
				if len(td.BenefitEdges) > 0 {
					benefitName = td.BenefitEdges[0].Benefit.Name
				}
				if benefitName != "" {
					if claimedAt, ok := claimedBenefits[strings.ToLower(benefitName)]; ok {
						if campaignStartAt, err := time.Parse(time.RFC3339Nano, cCampaign.StartAt); err == nil {
							if claimedAt.After(campaignStartAt.UTC()) {
								isClaimed = true
							}
						}
					}
				}
				name := benefitName
				if name == "" {
					name = td.ID
				}
				allCampaignDrops = append(allCampaignDrops, Drop{
					ID:              td.ID,
					Name:            name,
					CampaignID:      cCampaign.ID,
					CampaignName:    cCampaign.Name,
					GameName:        cCampaign.Game.DisplayName,
					GameImageURL:    gameImageURL,
					ImageURL:        imageURL,
					RequiredMinutes: td.RequiredMinutesWatched,
					CurrentMinutes:  0,
					IsClaimed:       isClaimed,
					IsClaimable:     false,
					IsEarnable:      true,
					EndsAt:          endsAt,
				})
			}
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

	return connectedGames, unconnectedGames, allCampaignDrops, nil
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

		if drop.RequiredMinutesWatched <= 0 || drop.RequiredSubs > 0 || isClaimed {
			continue
		}
		preconditionsMet := true
		if drop.Self.HasPreconditionsMet != nil {
			preconditionsMet = *drop.Self.HasPreconditionsMet
		}
		if !preconditionsMet {
			continue
		}
		isActive := campaignDropActive(now, campaign.Status, campaign.StartAt, campaign.EndAt, drop.StartAt, drop.EndAt)
		if isActive && campaignDropCompletable(now, campaign.EndAt, drop.EndAt, drop.RequiredMinutesWatched) {
			hasEarnableDrop = true
		}
	}
	return hasEarnableDrop, isConnected
}

func campaignDropCompletable(now time.Time, campaignEnd, dropEnd string, requiredMinutes int) bool {
	deadline, hasDeadline := earliestDropDeadline(campaignEnd, dropEnd)
	if !hasDeadline {
		return true
	}
	required := time.Duration(requiredMinutes)*time.Minute + dropCompletionSafetyBuffer
	return deadline.Sub(now) >= required
}

func earliestDropDeadline(campaignEnd, dropEnd string) (time.Time, bool) {
	var deadline time.Time
	hasDeadline := false
	for _, value := range []string{campaignEnd, dropEnd} {
		if value == "" {
			continue
		}
		parsed, err := parseTwitchTime(value)
		if err != nil {
			continue
		}
		if !hasDeadline || parsed.Before(deadline) {
			deadline = parsed
			hasDeadline = true
		}
	}
	return deadline, hasDeadline
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
