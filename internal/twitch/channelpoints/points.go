package channelpoints

import (
	"encoding/json"
	"fmt"
	"time"
)

type claimableBonusPayload struct {
	ClaimID              string    `json:"claim_id"`
	ID                   string    `json:"id"`
	ChannelID            string    `json:"channel_id"`
	BroadcasterUserID    string    `json:"broadcaster_user_id"`
	StreamerLogin        string    `json:"streamer_login"`
	BroadcasterUserLogin string    `json:"broadcaster_user_login"`
	Points               int64     `json:"points"`
	AvailableAt          time.Time `json:"available_at"`
}

func DecodeClaimableBonus(data []byte, fallbackLogin string, fallbackChannelID string) (ClaimableBonus, error) {
	var payload claimableBonusPayload
	if err := json.Unmarshal(data, &payload); err != nil {
		return ClaimableBonus{}, fmt.Errorf("decode claimable bonus: %w", err)
	}

	bonus := ClaimableBonus{
		ClaimID:       firstNonEmpty(payload.ClaimID, payload.ID),
		ChannelID:     firstNonEmpty(payload.ChannelID, payload.BroadcasterUserID, fallbackChannelID),
		StreamerLogin: firstNonEmpty(payload.StreamerLogin, payload.BroadcasterUserLogin, fallbackLogin),
		Points:        payload.Points,
		AvailableAt:   payload.AvailableAt,
	}
	if bonus.ClaimID == "" {
		return ClaimableBonus{}, fmt.Errorf("claimable bonus missing claim_id")
	}
	if bonus.Points == 0 {
		bonus.Points = 50
	}
	if bonus.AvailableAt.IsZero() {
		bonus.AvailableAt = time.Now().UTC()
	}
	return bonus, nil
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if value != "" {
			return value
		}
	}
	return ""
}
