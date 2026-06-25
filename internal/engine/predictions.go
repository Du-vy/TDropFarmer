package engine

import (
	"context"
	"log/slog"
	"time"

	"github.com/Du-vy/TDropFarmer/internal/config"
	"github.com/Du-vy/TDropFarmer/internal/twitch/predictions"
)

type predictionAdapter struct {
	inner  *predictions.EngineHandler
	config config.Config
	logger *slog.Logger
	points func(string) int64
	emit   func(Event)
}

type PredictionPlacedPayload struct {
	Title   string
	Outcome string
	Amount  int64
	DryRun  bool
}

func NewPredictionAdapter(cfg config.Config, claimer *predictions.PredictionClaimer, logger *slog.Logger, points func(string) int64, emit func(Event)) PredictionHandler {
	publish := func(ctx context.Context, p predictions.Prediction, d predictions.Decision, dryRun bool) {
		outcomeTitle := ""
		if d.Choice >= 0 && d.Choice < len(p.Outcomes) {
			outcomeTitle = p.Outcomes[d.Choice].Title
		}
		emit(Event{
			Type:      EventPredictionPlaced,
			Streamer:  p.StreamerLogin,
			ChannelID: p.ChannelID,
			Payload: PredictionPlacedPayload{
				Title:   p.Title,
				Outcome: outcomeTitle,
				Amount:  d.Amount,
				DryRun:  dryRun,
			},
			Time: time.Now().UTC(),
		})
	}
	inner := predictions.NewEngineHandler(cfg, claimer, logger, publish, points)
	return &predictionAdapter{
		inner:  inner,
		config: cfg,
		logger: logger,
		points: points,
		emit:   emit,
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

	// Also emit prediction result event for notification layer
	a.emit(Event{
		Type:      EventPredictionResult,
		Streamer:  event.StreamerLogin,
		ChannelID: event.ChannelID,
		Payload: PredictionResultPayload{
			Prediction: event,
			Result:     result,
		},
		Time: time.Now().UTC(),
	})
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
