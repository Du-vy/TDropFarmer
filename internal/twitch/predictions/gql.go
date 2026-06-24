package predictions

import (
	"context"
	"fmt"
	"time"

	"github.com/Du-vy/TDropFarmer/internal/config"
	"github.com/Du-vy/TDropFarmer/internal/twitch/gql"
)

const (
	makePredictionOperation = "MakePrediction"
	makePredictionHash      = "b44682ecc88358817009f20e69d75081b1e58825bb40aa53d5dbadcc17c881d8"
)

type GQLClient interface {
	Do(context.Context, gql.Request) (gql.Response, error)
}

type PlaceResult struct {
	Prediction Prediction
	Decision   Decision
	Placed     bool
	DryRun     bool
	PlacedAt   time.Time
	Error      error
}

type PredictionClaimer struct {
	Client GQLClient
}

func (c PredictionClaimer) Place(ctx context.Context, prediction Prediction, decision Decision) (PlaceResult, error) {
	if c.Client == nil {
		return PlaceResult{}, fmt.Errorf("graphql client is required")
	}
	if decision.ID == "" {
		return PlaceResult{}, fmt.Errorf("prediction outcome id is required")
	}
	if decision.Amount <= 0 {
		return PlaceResult{}, fmt.Errorf("prediction amount must be positive")
	}

	_, err := c.Client.Do(ctx, gql.Request{
		OperationName: makePredictionOperation,
		Variables: map[string]any{
			"input": map[string]any{
				"eventID":       prediction.EventID,
				"outcomeID":     decision.ID,
				"points":        decision.Amount,
				"transactionID": randomTransactionID(),
			},
		},
		Extensions: persistedQuery(makePredictionHash),
	})
	if err != nil {
		return PlaceResult{}, err
	}

	return PlaceResult{
		Prediction: prediction,
		Decision:   decision,
		Placed:     true,
		PlacedAt:   time.Now().UTC(),
	}, nil
}

func (c PredictionClaimer) CanPlace(cfg config.Config, streamerCfg config.StreamerConfig, currentPoints int64) bool {
	globalPreds := cfg.Features.PredictionsEnabled()
	if !globalPreds {
		return false
	}
	if streamerCfg.Predictions != nil && !*streamerCfg.Predictions {
		return false
	}
	if currentPoints < int64(cfg.Predictions.MinimumPoints) {
		return false
	}
	return true
}

func persistedQuery(hash string) map[string]any {
	return map[string]any{
		"persistedQuery": map[string]any{
			"version":    1,
			"sha256Hash": hash,
		},
	}
}

func randomTransactionID() string {
	return fmt.Sprintf("%08x-%04x-%04x", randUint32(), randUint16(), randUint16())
}

func randUint32() uint32 { return uint32(time.Now().UnixNano()>>16) & 0xFFFFFFFF }
func randUint16() uint16 { return uint16(time.Now().UnixNano()) }
