package engine

import (
	"context"
	"errors"
	"log/slog"
	"sync"
	"time"

	"github.com/Du-vy/TDropFarmer/internal/config"
	"github.com/Du-vy/TDropFarmer/internal/domain"
	"github.com/Du-vy/TDropFarmer/internal/store"
	"github.com/Du-vy/TDropFarmer/internal/twitch/channelpoints"
)

type Engine struct {
	config    config.Config
	streamers []StreamerState
	logger    *slog.Logger

	priorities  []priorityLevel
	maxChannels int
	tickSeconds int

	activeMu sync.Mutex
	active   []StreamerState

	events    chan Event
	eventsOut chan Event

	bonusClaimer      channelpoints.BonusClaimer
	pointRecorder     PointRecorder

	cancelFunc context.CancelFunc
}

type PointRecorder interface {
	RecordPointGain(store.PointGain) error
}

type Option func(*Engine)

func WithBonusClaimer(claimer channelpoints.BonusClaimer) Option {
	return func(e *Engine) {
		e.bonusClaimer = claimer
	}
}

func WithPointRecorder(recorder PointRecorder) Option {
	return func(e *Engine) {
		e.pointRecorder = recorder
	}
}

func New(cfg config.Config, resolved []domain.Streamer, logger *slog.Logger, opts ...Option) *Engine {
	states := make([]StreamerState, 0, len(resolved))
	for i, streamer := range resolved {
		states = append(states, StreamerState{
			Login:       streamer.Login,
			ChannelID:   streamer.ID,
			DisplayName: streamer.DisplayName,
			Priority:    i,
		})
	}
	states = applyConfigOverrides(states, cfg.Streamers)

	engine := &Engine{
		config:      cfg,
		streamers:   states,
		logger:      logger,
		priorities:  parsePriorities(cfg.Watch.Priorities),
		maxChannels: cfg.Watch.MaxChannels,
		tickSeconds: cfg.Watch.TickSeconds,
		events:      make(chan Event, 32),
		eventsOut:   make(chan Event, 32),
	}
	for _, opt := range opts {
		opt(engine)
	}
	return engine
}

func applyConfigOverrides(states []StreamerState, cfgStreamers []config.StreamerConfig) []StreamerState {
	lookup := make(map[string]config.StreamerConfig, len(cfgStreamers))
	for _, cfg := range cfgStreamers {
		lookup[cfg.Login] = cfg
	}
	for i, state := range states {
		if cfg, ok := lookup[state.Login]; ok {
			if cfg.ClaimDrops != nil {
				_ = *cfg.ClaimDrops
			}
		}
		states[i] = state
	}
	return states
}

func (e *Engine) Events() <-chan Event {
	return e.eventsOut
}

func (e *Engine) Run(ctx context.Context) error {
	ctx, cancel := context.WithCancel(ctx)
	e.cancelFunc = cancel
	defer close(e.eventsOut)

	e.logger.Info("engine started",
		slog.Int("streamers", len(e.streamers)),
		slog.Int("max_channels", e.maxChannels),
		slog.Int("tick_seconds", e.tickSeconds),
	)

	ticker := time.NewTicker(time.Duration(e.tickSeconds) * time.Second)
	defer ticker.Stop()

	e.reschedule()

	for {
		select {
		case <-ctx.Done():
			e.logger.Info("engine shutting down")
			return ctx.Err()
		case event := <-e.events:
			e.handleEvent(ctx, event)
		case <-ticker.C:
			e.reschedule()
		}
	}
}

func (e *Engine) reschedule() {
	active := selectActive(e.priorities, e.streamers, e.maxChannels)

	previous := e.activeSnapshot()
	added, removed := diffSnapshots(previous, active)

	now := time.Now()
	for _, state := range added {
		e.logger.Info("start watching",
			slog.String("login", state.Login),
			slog.String("channel_id", state.ChannelID),
		)
		e.emit(Event{
			Type:      EventOnline,
			Streamer:  state.Login,
			ChannelID: state.ChannelID,
			Time:      now,
		})
	}
	for _, state := range removed {
		e.logger.Info("stop watching",
			slog.String("login", state.Login),
			slog.String("channel_id", state.ChannelID),
		)
		e.emit(Event{
			Type:      EventOffline,
			Streamer:  state.Login,
			ChannelID: state.ChannelID,
			Time:      now,
		})
	}

	for i, state := range active {
		state.Watching = true
		active[i] = state
	}
	for i := range e.streamers {
		if !stateSliceHas(active, e.streamers[i].Login) {
			e.streamers[i].Watching = false
		} else {
			e.streamers[i].Watching = true
		}
	}

	e.activeMu.Lock()
	e.active = active
	e.activeMu.Unlock()
}

func (e *Engine) handleEvent(ctx context.Context, event Event) {
	e.logger.Debug("engine event",
		slog.String("type", string(event.Type)),
		slog.String("streamer", event.Streamer),
	)

	if event.Type == EventBonusAvailable {
		e.handleBonusAvailable(ctx, event)
		return
	}
	if event.Type == EventDropClaimed {
		e.emit(event)
		return
	}
	if event.Type == EventChatMention {
		e.emit(event)
		return
	}

	for i, state := range e.streamers {
		if state.Login != event.Streamer {
			continue
		}
		switch event.Type {
		case EventStreak:
			e.streamers[i].StreakReady = true
			e.logger.Info("streak ready", slog.String("streamer", state.Login))
			e.reschedule()
		case EventPoints:
			if gain, ok := e.pointGainFromEvent(event, state); ok {
				gain = e.applyPointGain(gain)
				if e.pointRecorder != nil {
					if err := e.pointRecorder.RecordPointGain(gain); err != nil {
						e.logger.Warn("persist point gain failed", slog.String("error", err.Error()))
					}
				}
				e.reschedule()
			}
		case EventBalance:
			if balance, ok := balanceFromPayload(event.Payload); ok {
				e.streamers[i].Points = balance
				e.logger.Info("points balance loaded",
					slog.String("streamer", state.Login),
					slog.Int64("balance", balance),
				)
				e.reschedule()
			}
		}
		break
	}
}

func (e *Engine) handleBonusAvailable(ctx context.Context, event Event) {
	bonus, ok := event.Payload.(channelpoints.ClaimableBonus)
	if !ok {
		e.logger.Warn("bonus event has unsupported payload", slog.String("streamer", event.Streamer))
		return
	}
	if bonus.StreamerLogin == "" {
		bonus.StreamerLogin = event.Streamer
	}
	if bonus.ChannelID == "" {
		bonus.ChannelID = event.ChannelID
	}
	if bonus.AvailableAt.IsZero() {
		bonus.AvailableAt = event.Time
	}

	if !e.config.Features.ClaimBonusesEnabled() {
		e.logger.Info("bonus claim skipped because feature is disabled", slog.String("streamer", bonus.StreamerLogin))
		return
	}

	if e.config.Features.DryRunEnabled() {
		result := channelpoints.DryRunResult(bonus, time.Now())
		e.logger.Info("dry-run bonus claim",
			slog.String("streamer", bonus.StreamerLogin),
			slog.String("claim_id", bonus.ClaimID),
			slog.Int64("points", bonus.Points),
		)
		e.emit(Event{Type: EventBonusClaimed, Streamer: bonus.StreamerLogin, ChannelID: bonus.ChannelID, Payload: result, Time: result.ClaimedAt})
		return
	}

	if e.bonusClaimer == nil {
		e.logger.Warn("bonus claim skipped because no claimer is configured", slog.String("streamer", bonus.StreamerLogin))
		return
	}

	result, err := e.bonusClaimer.ClaimBonus(ctx, bonus)
	if err != nil {
		if errors.Is(err, channelpoints.ErrBonusClaimUnsupported) {
			e.logger.Warn("bonus claim is not configured", slog.String("streamer", bonus.StreamerLogin))
			return
		}
		e.logger.Warn("bonus claim failed", slog.String("streamer", bonus.StreamerLogin), slog.String("error", err.Error()))
		return
	}
	if result.Points == 0 {
		result.Points = bonus.Points
	}
	if result.ChannelID == "" {
		result.ChannelID = bonus.ChannelID
	}
	if result.StreamerLogin == "" {
		result.StreamerLogin = bonus.StreamerLogin
	}
	if result.ClaimedAt.IsZero() {
		result.ClaimedAt = time.Now().UTC()
	}

	if result.Claimed && result.Points != 0 {
		gain := e.applyPointGain(store.PointGain{
			Login:     result.StreamerLogin,
			ChannelID: result.ChannelID,
			Amount:    result.Points,
			Reason:    "bonus_claim",
			Time:      result.ClaimedAt,
		})
		if e.pointRecorder != nil {
			if err := e.pointRecorder.RecordPointGain(gain); err != nil {
				e.logger.Warn("persist bonus claim failed", slog.String("error", err.Error()))
			}
		}
	}

	e.logger.Info("bonus claimed",
		slog.String("streamer", result.StreamerLogin),
		slog.String("claim_id", result.ClaimID),
		slog.Int64("points", result.Points),
	)
	e.emit(Event{Type: EventBonusClaimed, Streamer: result.StreamerLogin, ChannelID: result.ChannelID, Payload: result, Time: result.ClaimedAt})
}

func (e *Engine) pointGainFromEvent(event Event, state StreamerState) (store.PointGain, bool) {
	gain := store.PointGain{
		Login:     state.Login,
		ChannelID: state.ChannelID,
		Reason:    "event_points",
		Time:      event.Time,
	}
	switch payload := event.Payload.(type) {
	case int64:
		gain.Amount = payload
	case int:
		gain.Amount = int64(payload)
	case store.PointGain:
		gain = payload
		if gain.Login == "" {
			gain.Login = state.Login
		}
		if gain.ChannelID == "" {
			gain.ChannelID = state.ChannelID
		}
		if gain.Reason == "" {
			gain.Reason = "event_points"
		}
	default:
		return store.PointGain{}, false
	}
	if gain.Time.IsZero() {
		gain.Time = time.Now().UTC()
	}
	return gain, true
}

func (e *Engine) applyPointGain(gain store.PointGain) store.PointGain {
	for i, state := range e.streamers {
		if state.Login != gain.Login {
			continue
		}
		if gain.ChannelID == "" {
			gain.ChannelID = state.ChannelID
		}
		e.streamers[i].Points += gain.Amount
		e.logger.Info("points updated",
			slog.String("streamer", state.Login),
			slog.String("reason", gain.Reason),
			slog.Int64("gained", gain.Amount),
			slog.Int64("total", e.streamers[i].Points),
		)
		break
	}
	return gain
}

func balanceFromPayload(payload any) (int64, bool) {
	switch value := payload.(type) {
	case int64:
		return value, true
	case int:
		return int64(value), true
	case uint64:
		if value > uint64(^uint64(0)>>1) {
			return 0, false
		}
		return int64(value), true
	default:
		return 0, false
	}
}

func (e *Engine) activeSnapshot() []StreamerState {
	e.activeMu.Lock()
	defer e.activeMu.Unlock()
	snapshot := make([]StreamerState, len(e.active))
	copy(snapshot, e.active)
	return snapshot
}

func (e *Engine) emit(event Event) {
	select {
	case e.eventsOut <- event:
	default:
	}
}

func (e *Engine) SendEvent(event Event) {
	select {
	case e.events <- event:
	default:
		e.logger.Warn("engine event channel full", slog.String("type", string(event.Type)))
	}
}

func (e *Engine) PointsForStreamer(login string) int64 {
	for _, state := range e.streamers {
		if state.Login == login {
			return state.Points
		}
	}
	return 0
}

func (e *Engine) ActiveStreamers() []string {
	e.activeMu.Lock()
	defer e.activeMu.Unlock()
	logins := make([]string, 0, len(e.active))
	for _, state := range e.active {
		if state.Watching {
			logins = append(logins, state.Login)
		}
	}
	return logins
}
