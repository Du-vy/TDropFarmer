package channelpoints

import (
	"context"
	"errors"
	"time"
)

var ErrBonusClaimUnsupported = errors.New("channel-points bonus claim is not configured")

type ClaimableBonus struct {
	ClaimID       string
	ChannelID     string
	StreamerLogin string
	Points        int64
	AvailableAt   time.Time
}

type ClaimResult struct {
	ClaimID       string
	ChannelID     string
	StreamerLogin string
	Points        int64
	Claimed       bool
	DryRun        bool
	ClaimedAt     time.Time
}

type BonusClaimer interface {
	ClaimBonus(context.Context, ClaimableBonus) (ClaimResult, error)
}

type UnsupportedClaimer struct{}

func (UnsupportedClaimer) ClaimBonus(context.Context, ClaimableBonus) (ClaimResult, error) {
	return ClaimResult{}, ErrBonusClaimUnsupported
}

func DryRunResult(bonus ClaimableBonus, now time.Time) ClaimResult {
	if now.IsZero() {
		now = time.Now().UTC()
	}
	return ClaimResult{
		ClaimID:       bonus.ClaimID,
		ChannelID:     bonus.ChannelID,
		StreamerLogin: bonus.StreamerLogin,
		Points:        bonus.Points,
		Claimed:       false,
		DryRun:        true,
		ClaimedAt:     now.UTC(),
	}
}
