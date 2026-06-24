package engine

import (
	"context"
	"log/slog"

	"github.com/Du-vy/TDropFarmer/internal/config"
	"github.com/Du-vy/TDropFarmer/internal/twitch/predictions"
)

type predictionAdapter struct {
	inner  *predictions.EngineHandler
	config config.Config
	logger *slog.Logger
	points func(string) int64
}

func NewPredictionAdapter(cfg config.Config, claimer *predictions.PredictionClaimer, logger *slog.Logger, points func(string) int64) PredictionHandler {
	inner := predictions.NewEngineHandler(cfg, claimer, logger, nil, points)
	return &predictionAdapter{
		inner:  inner,
		config: cfg,
		logger: logger,
		points: points,
	}
}

func (a *predictionAdapter) PredictionEnabled() bool {
	return a.config.Features.PredictionsEnabled()
}

func (a *predictionAdapter) HandlePredictionStarted(_ context.Context, event PredictionEvent) error {
	pred := predictions.Prediction{
		EventID:       event.EventID,
		Title:         event.Title,
		ChannelID:     event.ChannelID,
		StreamerLogin: event.StreamerLogin,
		Status:        event.Status,
		Outcomes:      convertOutcomes(event.Outcomes),
		TimerSeconds:  event.TimerSeconds,
	}
	a.inner.HandlePrediction(context.Background(), pred)
	return nil
}

func (a *predictionAdapter) HandlePredictionResult(_ context.Context, event PredictionEvent, result PredictionResultEvent) error {
	pred := predictions.Prediction{
		EventID:       event.EventID,
		Title:         event.Title,
		ChannelID:     event.ChannelID,
		StreamerLogin: event.StreamerLogin,
		Status:        event.Status,
		Outcomes:      convertOutcomes(event.Outcomes),
		TimerSeconds:  event.TimerSeconds,
	}
	predResult := predictions.PredictionResult{
		EventID:   result.EventID,
		Type:      predictions.PredictionResultType(result.Type),
		PointsWon: result.PointsWon,
	}
	a.inner.HandlePredictionResult(context.Background(), pred, predResult)
	return nil
}

func convertOutcomes(outcomes []PredictionOutcome) []predictions.Outcome {
	result := make([]predictions.Outcome, len(outcomes))
	for i, o := range outcomes {
		result[i] = predictions.Outcome{
			ID:              o.ID,
			Title:           o.Title,
			Color:           o.Color,
			TotalUsers:      o.TotalUsers,
			TotalPoints:     o.TotalPoints,
			TopPoints:       o.TopPoints,
			PercentageUsers: o.PercentageUsers,
			Odds:            o.Odds,
			OddsPercentage:  o.OddsPercentage,
		}
	}
	return result
}
