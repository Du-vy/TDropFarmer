package predictions

import (
	"testing"
	"time"

	"github.com/Du-vy/TDropFarmer/internal/config"
)

func twoOutcomes() []Outcome {
	return []Outcome{
		{ID: "outcome-a", Title: "A", Color: "BLUE", TotalUsers: 100, TotalPoints: 1000, TopPoints: 500, PercentageUsers: 66.6, Odds: 1.5, OddsPercentage: 66.6},
		{ID: "outcome-b", Title: "B", Color: "PINK", TotalUsers: 50, TotalPoints: 500, TopPoints: 300, PercentageUsers: 33.3, Odds: 3.0, OddsPercentage: 33.3},
	}
}

func activePrediction() Prediction {
	return Prediction{
		EventID:       "evt-1",
		Title:         "Test Prediction",
		ChannelID:     "ch-1",
		StreamerLogin: "streamer",
		Status:        "ACTIVE",
		Outcomes:      twoOutcomes(),
		TotalUsers:    150,
		TotalPoints:   1500,
		TimerSeconds:  120,
	}
}

func defaultPredictionConfig() config.PredictionConfig {
	return config.PredictionConfig{
		Strategy:      "smart",
		Percentage:    5,
		PercentageGap: 20,
		MaxPoints:     50000,
		MinimumPoints: 1000,
		DelayMode:     "from_end",
		DelaySeconds:  6,
		StealthMode:   false,
	}
}

func TestMostVotedStrategy(t *testing.T) {
	cfg := defaultPredictionConfig()
	cfg.Strategy = "most_voted"
	decision := activePrediction().Calculate(cfg, 50000)
	if decision.Choice != 0 {
		t.Fatalf("choice = %d, want 0 (outcome-a has more users)", decision.Choice)
	}
	if decision.ID != "outcome-a" {
		t.Fatalf("id = %q, want outcome-a", decision.ID)
	}
}

func TestHighOddsStrategy(t *testing.T) {
	cfg := defaultPredictionConfig()
	cfg.Strategy = "high_odds"
	decision := activePrediction().Calculate(cfg, 50000)
	if decision.Choice != 1 {
		t.Fatalf("choice = %d, want 1 (outcome-b has higher odds)", decision.Choice)
	}
}

func TestSmartStrategy(t *testing.T) {
	cfg := defaultPredictionConfig()
	cfg.Strategy = "smart"
	cfg.PercentageGap = 50
	decision := activePrediction().Calculate(cfg, 50000)
	if decision.Choice != 1 {
		t.Fatalf("choice = %d, want 1 (diff 33 < gap 50 -> use odds, outcome-b has higher odds)", decision.Choice)
	}
}

func TestSmartStrategyLowGapFallsToOdds(t *testing.T) {
	cfg := defaultPredictionConfig()
	cfg.Strategy = "smart"
	cfg.PercentageGap = 40
	decision := activePrediction().Calculate(cfg, 50000)
	if decision.Choice != 1 {
		t.Fatalf("choice = %d, want 1 (diff 33 < gap 40 -> use odds, outcome-b has higher odds)", decision.Choice)
	}
}

func TestFixedOutcome(t *testing.T) {
	cfg := defaultPredictionConfig()
	cfg.Strategy = "fixed_outcome_2"
	decision := activePrediction().Calculate(cfg, 50000)
	if decision.Choice != 1 {
		t.Fatalf("choice = %d, want 1", decision.Choice)
	}
}

func TestAmountCalculated(t *testing.T) {
	cfg := defaultPredictionConfig()
	cfg.Strategy = "most_voted"
	cfg.Percentage = 10
	decision := activePrediction().Calculate(cfg, 10000)
	if decision.Amount != 1000 {
		t.Fatalf("amount = %d, want 1000 (10%% of 10000)", decision.Amount)
	}
}

func TestAmountCappedByMax(t *testing.T) {
	cfg := defaultPredictionConfig()
	cfg.Strategy = "most_voted"
	cfg.Percentage = 50
	cfg.MaxPoints = 200
	decision := activePrediction().Calculate(cfg, 1000)
	if decision.Amount != 200 {
		t.Fatalf("amount = %d, want 200 (capped by max_points)", decision.Amount)
	}
}

func TestMinimumPointsRequired(t *testing.T) {
	cfg := defaultPredictionConfig()
	cfg.Strategy = "most_voted"
	cfg.MinimumPoints = 20000
	decision := activePrediction().Calculate(cfg, 10000)
	if decision.Choice != -1 {
		t.Fatalf("choice = %d, want -1 (below minimum points)", decision.Choice)
	}
}

func TestFilterSkip(t *testing.T) {
	cfg := defaultPredictionConfig()
	cfg.FilterCondition = &config.FilterCondition{By: "total_users", Where: "gt", Value: 200}
	skip, _ := activePrediction().ShouldSkip(cfg)
	if !skip {
		t.Fatalf("skip = false, want true (150 not > 200, skip bet)")
	}
}

func TestFilterDoesNotSkipWhenConditionMet(t *testing.T) {
	cfg := defaultPredictionConfig()
	cfg.FilterCondition = &config.FilterCondition{By: "total_users", Where: "lt", Value: 200}
	skip, _ := activePrediction().ShouldSkip(cfg)
	if skip {
		t.Fatalf("skip = true, want false (150 < 200 met, so don't skip)")
	}
}

func TestFilterSkipsWhenConditionNotMet(t *testing.T) {
	cfg := defaultPredictionConfig()
	cfg.FilterCondition = &config.FilterCondition{By: "total_points", Where: "lt", Value: 100}
	skip, _ := activePrediction().ShouldSkip(cfg)
	if !skip {
		t.Fatalf("skip = false, want true (1500 < 100 false, LT: skip when NOT less)")
	}
}

func TestDelayFromEnd(t *testing.T) {
	cfg := defaultPredictionConfig()
	cfg.DelayMode = "from_end"
	cfg.DelaySeconds = 10
	p := activePrediction()
	p.TimerSeconds = 120
	delay := p.PredictDelay(cfg)
	if delay != 110*time.Second {
		t.Fatalf("delay = %s, want 110s (120-10)", delay)
	}
}

func TestDelayFromStart(t *testing.T) {
	cfg := defaultPredictionConfig()
	cfg.DelayMode = "from_start"
	cfg.DelaySeconds = 10
	delay := activePrediction().PredictDelay(cfg)
	if delay != 10*time.Second {
		t.Fatalf("delay = %s, want 10s", delay)
	}
}

func TestDelayPercentage(t *testing.T) {
	cfg := defaultPredictionConfig()
	cfg.DelayMode = "percentage"
	cfg.DelaySeconds = 50
	p := activePrediction()
	p.TimerSeconds = 100
	delay := p.PredictDelay(cfg)
	if delay != 50*time.Second {
		t.Fatalf("delay = %s, want 50s (50%% of 100s)", delay)
	}
}
