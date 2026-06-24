package channelpoints

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/Du-vy/TDropFarmer/internal/twitch/gql"
)

func TestUnsupportedClaimer(t *testing.T) {
	_, err := UnsupportedClaimer{}.ClaimBonus(context.Background(), ClaimableBonus{})
	if !errors.Is(err, ErrBonusClaimUnsupported) {
		t.Fatalf("ClaimBonus error = %v, want ErrBonusClaimUnsupported", err)
	}
}

func TestDryRunResult(t *testing.T) {
	now := time.Now().UTC().Round(time.Second)
	result := DryRunResult(ClaimableBonus{
		ClaimID:       "claim-1",
		ChannelID:     "channel-1",
		StreamerLogin: "streamer",
		Points:        50,
	}, now)
	if !result.DryRun {
		t.Fatalf("DryRun = false, want true")
	}
	if result.Claimed {
		t.Fatalf("Claimed = true, want false")
	}
	if result.Points != 50 {
		t.Fatalf("Points = %d, want 50", result.Points)
	}
	if !result.ClaimedAt.Equal(now) {
		t.Fatalf("ClaimedAt = %s, want %s", result.ClaimedAt, now)
	}
}

func TestDecodeClaimableBonus(t *testing.T) {
	bonus, err := DecodeClaimableBonus([]byte(`{"id":"claim-1","broadcaster_user_id":"channel-1"}`), "streamer", "fallback-channel")
	if err != nil {
		t.Fatalf("DecodeClaimableBonus returned error: %v", err)
	}
	if bonus.ClaimID != "claim-1" {
		t.Fatalf("ClaimID = %q, want claim-1", bonus.ClaimID)
	}
	if bonus.ChannelID != "channel-1" {
		t.Fatalf("ChannelID = %q, want channel-1", bonus.ChannelID)
	}
	if bonus.StreamerLogin != "streamer" {
		t.Fatalf("StreamerLogin = %q, want streamer", bonus.StreamerLogin)
	}
	if bonus.Points != 50 {
		t.Fatalf("Points = %d, want default 50", bonus.Points)
	}
}

func TestDecodeClaimableBonusRequiresClaimID(t *testing.T) {
	if _, err := DecodeClaimableBonus([]byte(`{}`), "streamer", "channel"); err == nil {
		t.Fatalf("DecodeClaimableBonus returned nil error, want missing claim_id error")
	}
}

func TestGraphQLBonusClaimer(t *testing.T) {
	client := &recordingGQLClient{response: gql.Response{Data: json.RawMessage(`{"claimCommunityPoints":{"error":null}}`)}}
	claimer := GraphQLBonusClaimer{Client: client}

	result, err := claimer.ClaimBonus(context.Background(), ClaimableBonus{
		ClaimID:       "claim-1",
		ChannelID:     "channel-1",
		StreamerLogin: "streamer",
		Points:        50,
	})
	if err != nil {
		t.Fatalf("ClaimBonus returned error: %v", err)
	}
	if !result.Claimed {
		t.Fatalf("Claimed = false, want true")
	}
	if client.request.OperationName != claimCommunityPointsOperation {
		t.Fatalf("operation = %q, want %q", client.request.OperationName, claimCommunityPointsOperation)
	}
	input := client.request.Variables["input"].(map[string]any)
	if input["channelID"] != "channel-1" || input["claimID"] != "claim-1" {
		t.Fatalf("input = %#v", input)
	}
	if persistedHash(client.request.Extensions) != claimCommunityPointsHash {
		t.Fatalf("hash = %q, want %q", persistedHash(client.request.Extensions), claimCommunityPointsHash)
	}
}

func TestGraphQLBonusClaimerReturnsResponseError(t *testing.T) {
	client := &recordingGQLClient{response: gql.Response{Data: json.RawMessage(`{
    "claimCommunityPoints": {"error": {"code": "ALREADY_CLAIMED", "message": "already claimed"}}
  }`)}}
	claimer := GraphQLBonusClaimer{Client: client}

	_, err := claimer.ClaimBonus(context.Background(), ClaimableBonus{ClaimID: "claim-1", ChannelID: "channel-1"})
	if err == nil {
		t.Fatalf("ClaimBonus returned nil error, want response error")
	}
}

func TestContextLoader(t *testing.T) {
	client := &recordingGQLClient{response: gql.Response{Data: json.RawMessage(`{
    "community": {
      "channel": {
        "id": "channel-1",
        "login": "streamer",
        "self": {
          "communityPoints": {
            "balance": 1234,
            "availableClaim": {"id": "claim-1"}
          }
        }
      }
    }
  }`)}}
	loader := ContextLoader{Client: client}

	ctx, err := loader.Load(context.Background(), "streamer", "fallback-channel")
	if err != nil {
		t.Fatalf("Load returned error: %v", err)
	}
	if ctx.Balance != 1234 {
		t.Fatalf("Balance = %d, want 1234", ctx.Balance)
	}
	if ctx.AvailableClaim == nil || ctx.AvailableClaim.ClaimID != "claim-1" {
		t.Fatalf("AvailableClaim = %#v, want claim-1", ctx.AvailableClaim)
	}
	if client.request.OperationName != channelPointsContextOperation {
		t.Fatalf("operation = %q, want %q", client.request.OperationName, channelPointsContextOperation)
	}
	if client.request.Variables["channelLogin"] != "streamer" {
		t.Fatalf("channelLogin = %v, want streamer", client.request.Variables["channelLogin"])
	}
	if persistedHash(client.request.Extensions) != channelPointsContextHash {
		t.Fatalf("hash = %q, want %q", persistedHash(client.request.Extensions), channelPointsContextHash)
	}
}

type recordingGQLClient struct {
	request  gql.Request
	response gql.Response
	err      error
}

func (c *recordingGQLClient) Do(_ context.Context, request gql.Request) (gql.Response, error) {
	c.request = request
	return c.response, c.err
}

func persistedHash(extensions map[string]any) string {
	persisted, ok := extensions["persistedQuery"].(map[string]any)
	if !ok {
		return ""
	}
	hash, _ := persisted["sha256Hash"].(string)
	return hash
}
