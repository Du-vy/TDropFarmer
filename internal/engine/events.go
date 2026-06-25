package engine

import "time"

type EventType string

const (
	EventOnline           EventType = "online"
	EventOffline          EventType = "offline"
	EventPoints           EventType = "points"
	EventBalance          EventType = "balance"
	EventStreak           EventType = "streak"
	EventBonusAvailable   EventType = "bonus_available"
	EventBonusClaimed     EventType = "bonus_claimed"
	EventPredictionStart  EventType = "prediction_start"
	EventPredictionPlaced EventType = "prediction_placed"
	EventPredictionResult EventType = "prediction_result"
	EventDropClaimed      EventType = "drop_claimed"
)

type Event struct {
	Type      EventType
	Streamer  string
	ChannelID string
	Payload   any
	Time      time.Time
}

type StreamerState struct {
	Login       string
	ChannelID   string
	DisplayName string
	Online      bool
	Points      int64
	StreakReady bool
	Watching    bool
	Priority    int
}
