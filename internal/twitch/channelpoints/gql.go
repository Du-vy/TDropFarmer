package channelpoints

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/Du-vy/TDropFarmer/internal/twitch/gql"
)

const (
	claimCommunityPointsOperation = "ClaimCommunityPoints"
	claimCommunityPointsHash      = "46aaeebe02c99afdf4fc97c7c0cba964124bf6b0af229395f1f6d1feed05b3d0"

	channelPointsContextOperation = "ChannelPointsContext"
	channelPointsContextHash      = "1530a003a7d374b0380b79db0be0534f30ff46e61cffa2bc0e2468a909fbc024"
)

type GQLClient interface {
	Do(context.Context, gql.Request) (gql.Response, error)
}

type GraphQLBonusClaimer struct {
	Client GQLClient
}

func (c GraphQLBonusClaimer) ClaimBonus(ctx context.Context, bonus ClaimableBonus) (ClaimResult, error) {
	if c.Client == nil {
		return ClaimResult{}, fmt.Errorf("graphql client is required")
	}
	if bonus.ChannelID == "" {
		return ClaimResult{}, fmt.Errorf("bonus channel id is required")
	}
	if bonus.ClaimID == "" {
		return ClaimResult{}, fmt.Errorf("bonus claim id is required")
	}

	response, err := c.Client.Do(ctx, gql.Request{
		OperationName: claimCommunityPointsOperation,
		Variables: map[string]any{
			"input": map[string]any{
				"channelID": bonus.ChannelID,
				"claimID":   bonus.ClaimID,
			},
		},
		Extensions: persistedQuery(claimCommunityPointsHash),
	})
	if err != nil {
		return ClaimResult{}, err
	}
	if err := decodeClaimError(response.Data); err != nil {
		return ClaimResult{}, err
	}

	points := bonus.Points
	if points == 0 {
		points = 50
	}
	return ClaimResult{
		ClaimID:       bonus.ClaimID,
		ChannelID:     bonus.ChannelID,
		StreamerLogin: bonus.StreamerLogin,
		Points:        points,
		Claimed:       true,
		ClaimedAt:     time.Now().UTC(),
	}, nil
}

func decodeClaimError(data json.RawMessage) error {
	if len(data) == 0 {
		return nil
	}
	var response struct {
		ClaimCommunityPoints *struct {
			Error *struct {
				Code    string `json:"code"`
				Message string `json:"message"`
			} `json:"error"`
		} `json:"claimCommunityPoints"`
	}
	if err := json.Unmarshal(data, &response); err != nil {
		return nil
	}
	if response.ClaimCommunityPoints != nil && response.ClaimCommunityPoints.Error != nil {
		claimErr := response.ClaimCommunityPoints.Error
		if claimErr.Message != "" {
			return fmt.Errorf("claim community points failed: %s", claimErr.Message)
		}
		return fmt.Errorf("claim community points failed: %s", claimErr.Code)
	}
	return nil
}

type ContextLoader struct {
	Client GQLClient
}

type Context struct {
	ChannelID      string
	ChannelLogin   string
	Balance        int64
	AvailableClaim *ClaimableBonus
}

func (l ContextLoader) Load(ctx context.Context, channelLogin string, fallbackChannelID string) (Context, error) {
	if l.Client == nil {
		return Context{}, fmt.Errorf("graphql client is required")
	}
	if channelLogin == "" {
		return Context{}, fmt.Errorf("channel login is required")
	}

	response, err := l.Client.Do(ctx, gql.Request{
		OperationName: channelPointsContextOperation,
		Variables: map[string]any{
			"channelLogin": channelLogin,
		},
		Extensions: persistedQuery(channelPointsContextHash),
	})
	if err != nil {
		return Context{}, err
	}

	var data channelPointsContextData
	if err := json.Unmarshal(response.Data, &data); err != nil {
		return Context{}, fmt.Errorf("decode channel points context: %w", err)
	}
	if data.Community == nil {
		return Context{}, fmt.Errorf("channel points context missing community")
	}

	channel := data.Community.Channel
	channelID := firstNonEmpty(channel.ID, fallbackChannelID)
	result := Context{
		ChannelID:    channelID,
		ChannelLogin: firstNonEmpty(channel.Login, channelLogin),
		Balance:      channel.Self.CommunityPoints.Balance,
	}

	claim := channel.Self.CommunityPoints.AvailableClaim
	if claim != nil && claim.ID != "" {
		result.AvailableClaim = &ClaimableBonus{
			ClaimID:       claim.ID,
			ChannelID:     channelID,
			StreamerLogin: result.ChannelLogin,
			Points:        50,
			AvailableAt:   time.Now().UTC(),
		}
	}

	return result, nil
}

func persistedQuery(hash string) map[string]any {
	return map[string]any{
		"persistedQuery": map[string]any{
			"version":    1,
			"sha256Hash": hash,
		},
	}
}

type channelPointsContextData struct {
	Community *struct {
		Channel struct {
			ID    string `json:"id"`
			Login string `json:"login"`
			Self  struct {
				CommunityPoints struct {
					Balance        int64 `json:"balance"`
					AvailableClaim *struct {
						ID string `json:"id"`
					} `json:"availableClaim"`
				} `json:"communityPoints"`
			} `json:"self"`
		} `json:"channel"`
	} `json:"community"`
}
