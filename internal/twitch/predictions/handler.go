package predictions

import (
	"context"
	"log/slog"
	"sync"
	"time"

	"github.com/Du-vy/TDropFarmer/internal/config"
)

type EventPublisher interface {
	SendEvent(any)
}

type EngineHandler struct {
	config  config.Config
	claimer *PredictionClaimer
	logger  *slog.Logger
	publish func(context.Context, Prediction, Decision, bool)
	points  func(string) int64

	pendingMu sync.Mutex
	pending   map[string]*pendingPrediction
}

type pendingPrediction struct {
	prediction Prediction
	deadline   time.Time
	timer      *time.Timer
}

func NewEngineHandler(cfg config.Config, claimer *PredictionClaimer, logger *slog.Logger, publish func(context.Context, Prediction, Decision, bool), points func(string) int64) *EngineHandler {
	return &EngineHandler{
		config:  cfg,
		claimer: claimer,
		logger:  logger,
		publish: publish,
		points:  points,
		pending: make(map[string]*pendingPrediction),
	}
}

func (h *EngineHandler) HandlePrediction(ctx context.Context, prediction Prediction) {
	if !h.config.Features.PredictionsEnabled() {
		h.logger.Info("prediction skipped because predictions are disabled",
			slog.String("event_id", prediction.EventID),
			slog.String("streamer", prediction.StreamerLogin),
		)
		return
	}

	if prediction.Status != "ACTIVE" {
		h.logger.Debug("prediction is not active",
			slog.String("event_id", prediction.EventID),
			slog.String("status", prediction.Status),
		)
		h.removePending(prediction.EventID)
		return
	}

	streamerCfg := h.streamerConfig(prediction.StreamerLogin)
	if streamerCfg.Predictions != nil && !*streamerCfg.Predictions {
		h.logger.Info("prediction skipped because per-streamer predictions are disabled",
			slog.String("event_id", prediction.EventID),
			slog.String("streamer", prediction.StreamerLogin),
		)
		return
	}

	delay := prediction.PredictDelay(getPredictionConfig(h.config, streamerCfg))
	h.logger.Info("prediction received, scheduling",
		slog.String("event_id", prediction.EventID),
		slog.String("streamer", prediction.StreamerLogin),
		slog.String("title", prediction.Title),
		slog.Duration("delay", delay),
		slog.Int("timer", prediction.TimerSeconds),
	)

	h.removePending(prediction.EventID)

	pending := &pendingPrediction{
		prediction: prediction,
		deadline:   time.Now().Add(delay),
	}
	pending.timer = time.AfterFunc(delay, func() {
		h.executePrediction(context.Background(), pending)
	})

	h.pendingMu.Lock()
	h.pending[prediction.EventID] = pending
	h.pendingMu.Unlock()
}

func (h *EngineHandler) HandlePredictionResult(ctx context.Context, prediction Prediction, result PredictionResult) {
	h.removePending(prediction.EventID)
	h.logger.Info("prediction result",
		slog.String("event_id", prediction.EventID),
		slog.String("result", string(result.Type)),
		slog.Int64("won", result.PointsWon),
	)
}

func (h *EngineHandler) Shutdown() {
	h.pendingMu.Lock()
	defer h.pendingMu.Unlock()
	for id, p := range h.pending {
		if p.timer != nil {
			p.timer.Stop()
		}
		delete(h.pending, id)
	}
}

func (h *EngineHandler) executePrediction(ctx context.Context, pending *pendingPrediction) {
	p := pending.prediction
	cfg := getPredictionConfig(h.config, h.streamerConfig(p.StreamerLogin))
	currentPoints := h.points(p.StreamerLogin)

	decision := p.Calculate(cfg, currentPoints)
	if decision.Choice < 0 || decision.Amount <= 0 {
		h.logger.Info("prediction skipped: no valid decision",
			slog.String("event_id", p.EventID),
			slog.String("streamer", p.StreamerLogin),
			slog.Int("choice", decision.Choice),
			slog.Int64("amount", decision.Amount),
		)
		h.removePending(p.EventID)
		return
	}

	if skip, comparedValue := p.ShouldSkip(cfg); skip {
		h.logger.Info("prediction skipped by filter",
			slog.String("event_id", p.EventID),
			slog.String("streamer", p.StreamerLogin),
			slog.Float64("compared_value", comparedValue),
		)
		h.removePending(p.EventID)
		return
	}

	if h.config.Features.DryRunEnabled() {
		h.logger.Info("dry-run prediction",
			slog.String("event_id", p.EventID),
			slog.String("streamer", p.StreamerLogin),
			slog.Int("choice", decision.Choice),
			slog.Int64("amount", decision.Amount),
		)
		h.removePending(p.EventID)
		h.publish(ctx, p, decision, true)
		return
	}

	h.logger.Info("placing prediction",
		slog.String("event_id", p.EventID),
		slog.String("streamer", p.StreamerLogin),
		slog.Int("choice", decision.Choice),
		slog.String("outcome_id", decision.ID),
		slog.Int64("amount", decision.Amount),
	)

	if h.claimer == nil {
		h.logger.Warn("prediction claimer is not configured")
		h.removePending(p.EventID)
		return
	}

	_, err := h.claimer.Place(ctx, p, decision)
	if err != nil {
		h.logger.Warn("prediction placement failed",
			slog.String("event_id", p.EventID),
			slog.String("error", err.Error()),
		)
	}
	h.removePending(p.EventID)
	h.publish(ctx, p, decision, false)
}

func (h *EngineHandler) removePending(eventID string) {
	h.pendingMu.Lock()
	defer h.pendingMu.Unlock()
	if p, ok := h.pending[eventID]; ok {
		if p.timer != nil {
			p.timer.Stop()
		}
		delete(h.pending, eventID)
	}
}

func (h *EngineHandler) streamerConfig(login string) config.StreamerConfig {
	for _, s := range h.config.Streamers {
		if s.Login == login {
			return s
		}
	}
	return config.StreamerConfig{}
}

type PredictionResult struct {
	EventID   string               `json:"event_id"`
	Type      PredictionResultType `json:"type"`
	PointsWon int64                `json:"points_won"`
}

type PredictionResultType string

const (
	PredictionWin    PredictionResultType = "WIN"
	PredictionLose   PredictionResultType = "LOSE"
	PredictionRefund PredictionResultType = "REFUND"
)

func getPredictionConfig(global config.Config, streamer config.StreamerConfig) config.PredictionConfig {
	if streamer.PredictionSettings != nil {
		cfg := *streamer.PredictionSettings
		if cfg.Strategy == "" {
			cfg.Strategy = global.Predictions.Strategy
		}
		if cfg.Percentage == 0 {
			cfg.Percentage = global.Predictions.Percentage
		}
		if cfg.PercentageGap == 0 {
			cfg.PercentageGap = global.Predictions.PercentageGap
		}
		if cfg.MaxPoints == 0 {
			cfg.MaxPoints = global.Predictions.MaxPoints
		}
		if cfg.MinimumPoints == 0 {
			cfg.MinimumPoints = global.Predictions.MinimumPoints
		}
		if cfg.DelayMode == "" {
			cfg.DelayMode = global.Predictions.DelayMode
		}
		if cfg.DelaySeconds == 0 {
			cfg.DelaySeconds = global.Predictions.DelaySeconds
		}
		return cfg
	}
	return global.Predictions
}
