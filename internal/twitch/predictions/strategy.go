package predictions

import (
	"math/rand/v2"
	"time"

	"github.com/Du-vy/TDropFarmer/internal/config"
)

type Strategy string

const (
	StrategyMostVoted  Strategy = "most_voted"
	StrategyHighOdds   Strategy = "high_odds"
	StrategyPercentage Strategy = "percentage"
	StrategySmartMoney Strategy = "smart_money"
	StrategySmart      Strategy = "smart"

	StrategyFixed1 Strategy = "fixed_outcome_1"
	StrategyFixed2 Strategy = "fixed_outcome_2"
	StrategyFixed3 Strategy = "fixed_outcome_3"
	StrategyFixed4 Strategy = "fixed_outcome_4"
	StrategyFixed5 Strategy = "fixed_outcome_5"
	StrategyFixed6 Strategy = "fixed_outcome_6"
	StrategyFixed7 Strategy = "fixed_outcome_7"
	StrategyFixed8 Strategy = "fixed_outcome_8"
)

type DelayMode string

const (
	DelayFromStart  DelayMode = "from_start"
	DelayFromEnd    DelayMode = "from_end"
	DelayPercentage DelayMode = "percentage"
)

type FilterBy string

const (
	FilterPercentageUsers FilterBy = "percentage_users"
	FilterOddsPercentage  FilterBy = "odds_percentage"
	FilterOdds            FilterBy = "odds"
	FilterDecisionUsers   FilterBy = "decision_users"
	FilterDecisionPoints  FilterBy = "decision_points"
	FilterTopPoints       FilterBy = "top_points"
	FilterTotalUsers      FilterBy = "total_users"
	FilterTotalPoints     FilterBy = "total_points"
)

type FilterWhere string

const (
	WhereGT  FilterWhere = "gt"
	WhereLT  FilterWhere = "lt"
	WhereGTE FilterWhere = "gte"
	WhereLTE FilterWhere = "lte"
)

type Outcome struct {
	ID              string  `json:"id"`
	Title           string  `json:"title"`
	Color           string  `json:"color"`
	TotalUsers      int64   `json:"total_users"`
	TotalPoints     int64   `json:"total_points"`
	TopPoints       int64   `json:"top_points"`
	PercentageUsers float64 `json:"percentage_users"`
	Odds            float64 `json:"odds"`
	OddsPercentage  float64 `json:"odds_percentage"`
}

type Prediction struct {
	EventID       string    `json:"event_id"`
	Title         string    `json:"title"`
	ChannelID     string    `json:"channel_id"`
	StreamerLogin string    `json:"streamer_login"`
	Status        string    `json:"status"`
	Outcomes      []Outcome `json:"outcomes"`
	TotalUsers    int64     `json:"total_users"`
	TotalPoints   int64     `json:"total_points"`
	TimerSeconds  int       `json:"timer_seconds"`
}

type Decision struct {
	Choice int    `json:"choice"`
	Amount int64  `json:"amount"`
	ID     string `json:"id"`
}

func (p Prediction) Calculate(cfg config.PredictionConfig, currentPoints int64) Decision {
	d := Decision{Choice: -1}

	if len(p.Outcomes) == 0 || p.Status != "ACTIVE" {
		return d
	}
	if currentPoints < int64(cfg.MinimumPoints) {
		return d
	}

	switch Strategy(cfg.Strategy) {
	case StrategyMostVoted:
		d.Choice = indexOfMax(p.Outcomes, func(o Outcome) float64 { return float64(o.TotalUsers) })
	case StrategyHighOdds:
		d.Choice = indexOfMax(p.Outcomes, func(o Outcome) float64 { return o.Odds })
	case StrategyPercentage:
		d.Choice = indexOfMax(p.Outcomes, func(o Outcome) float64 { return o.OddsPercentage })
	case StrategySmartMoney:
		d.Choice = indexOfMax(p.Outcomes, func(o Outcome) float64 { return float64(o.TopPoints) })
	case StrategySmart:
		if len(p.Outcomes) >= 2 {
			diff := abs(p.Outcomes[0].PercentageUsers - p.Outcomes[1].PercentageUsers)
			if diff < float64(cfg.PercentageGap) {
				d.Choice = indexOfMax(p.Outcomes, func(o Outcome) float64 { return o.Odds })
			} else {
				d.Choice = indexOfMax(p.Outcomes, func(o Outcome) float64 { return float64(o.TotalUsers) })
			}
		} else if len(p.Outcomes) == 1 {
			d.Choice = 0
		}
	case StrategyFixed1:
		d.Choice = fixedChoice(0, len(p.Outcomes))
	case StrategyFixed2:
		d.Choice = fixedChoice(1, len(p.Outcomes))
	case StrategyFixed3:
		d.Choice = fixedChoice(2, len(p.Outcomes))
	case StrategyFixed4:
		d.Choice = fixedChoice(3, len(p.Outcomes))
	case StrategyFixed5:
		d.Choice = fixedChoice(4, len(p.Outcomes))
	case StrategyFixed6:
		d.Choice = fixedChoice(5, len(p.Outcomes))
	case StrategyFixed7:
		d.Choice = fixedChoice(6, len(p.Outcomes))
	case StrategyFixed8:
		d.Choice = fixedChoice(7, len(p.Outcomes))
	}

	if d.Choice < 0 || d.Choice >= len(p.Outcomes) {
		return d
	}

	outcome := p.Outcomes[d.Choice]
	d.ID = outcome.ID
	d.Amount = int64(float64(currentPoints) * (float64(cfg.Percentage) / 100.0))
	if d.Amount > int64(cfg.MaxPoints) {
		d.Amount = int64(cfg.MaxPoints)
	}
	if cfg.StealthMode && d.Amount >= outcome.TopPoints && outcome.TopPoints > 0 {
		d.Amount = outcome.TopPoints - int64(1+rand.IntN(5))
		if d.Amount < 10 {
			d.Amount = 10
		}
	}
	return d
}

func (p Prediction) ShouldSkip(cfg config.PredictionConfig) (bool, float64) {
	if cfg.FilterCondition == nil {
		return false, 0
	}

	filter := cfg.FilterCondition
	key := filter.By
	where := filter.Where
	value := filter.Value

	resolvedKey := key
	if key == "decision_users" {
		resolvedKey = "total_users"
	}
	if key == "decision_points" {
		resolvedKey = "total_points"
	}

	var comparedValue float64
	if isSumFilter(key) {
		for _, o := range p.Outcomes {
			comparedValue += outcomeValue(o, resolvedKey)
		}
	} else {
		choice := indexOfMax(p.Outcomes, func(o Outcome) float64 { return float64(o.TotalUsers) })
		if choice >= 0 && choice < len(p.Outcomes) {
			comparedValue = outcomeValue(p.Outcomes[choice], resolvedKey)
		}
	}

	switch FilterWhere(where) {
	case WhereGT:
		return comparedValue <= value, comparedValue
	case WhereLT:
		return comparedValue >= value, comparedValue
	case WhereGTE:
		return comparedValue < value, comparedValue
	case WhereLTE:
		return comparedValue > value, comparedValue
	}
	return false, comparedValue
}

func (p Prediction) PredictDelay(cfg config.PredictionConfig) time.Duration {
	total := time.Duration(p.TimerSeconds) * time.Second
	delaySeconds := time.Duration(cfg.DelaySeconds) * time.Second

	switch DelayMode(cfg.DelayMode) {
	case DelayFromStart:
		return delaySeconds
	case DelayFromEnd:
		remaining := total - delaySeconds
		if remaining < 0 {
			return 0
		}
		return remaining
	case DelayPercentage:
		frac := float64(cfg.DelaySeconds) / 100.0
		remaining := time.Duration(float64(total) * frac)
		if remaining < 0 {
			return 0
		}
		return remaining
	default:
		return 0
	}
}

func isSumFilter(key string) bool {
	return key == "total_users" || key == "total_points"
}

func outcomeValue(o Outcome, key string) float64 {
	switch key {
	case "percentage_users":
		return o.PercentageUsers
	case "odds_percentage":
		return o.OddsPercentage
	case "odds":
		return o.Odds
	case "top_points":
		return float64(o.TopPoints)
	case "total_users":
		return float64(o.TotalUsers)
	case "total_points":
		return float64(o.TotalPoints)
	case "decision_users":
		return float64(o.TotalUsers)
	case "decision_points":
		return float64(o.TotalPoints)
	}
	return 0
}

func indexOfMax(outcomes []Outcome, value func(Outcome) float64) int {
	if len(outcomes) == 0 {
		return -1
	}
	best := 0
	bestValue := value(outcomes[0])
	for i := 1; i < len(outcomes); i++ {
		v := value(outcomes[i])
		if v > bestValue {
			best = i
			bestValue = v
		}
	}
	return best
}

func fixedChoice(n int, outcomeCount int) int {
	if n < outcomeCount {
		return n
	}
	return 0
}

func abs(x float64) float64 {
	if x < 0 {
		return -x
	}
	return x
}
