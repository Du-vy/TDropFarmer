package engine

import (
	"slices"
	"sort"
)

type Priority string

const (
	PriorityStreak           Priority = "streak"
	PriorityOrder            Priority = "order"
	PriorityPointsAscending  Priority = "points_ascending"
	PriorityPointsDescending Priority = "points_descending"
)

type priorityLevel struct {
	kind Priority
	rank int
}

func parsePriorities(values []string) []priorityLevel {
	list := make([]priorityLevel, 0, len(values))
	for i, value := range values {
		list = append(list, priorityLevel{kind: Priority(value), rank: i})
	}
	return list
}

func rankStreamers(prefs []priorityLevel, states []StreamerState) []StreamerState {
	ordered := make([]StreamerState, len(states))
	copy(ordered, states)

	sort.SliceStable(ordered, func(i, j int) bool {
		return byPriority(prefs, ordered[i], ordered[j])
	})
	return ordered
}

func byPriority(prefs []priorityLevel, a, b StreamerState) bool {
	for _, pref := range prefs {
		switch pref.kind {
		case PriorityStreak:
			if a.StreakReady != b.StreakReady {
				return a.StreakReady
			}
		case PriorityPointsAscending:
			if a.Points != b.Points {
				return a.Points < b.Points
			}
		case PriorityPointsDescending:
			if a.Points != b.Points {
				return a.Points > b.Points
			}
		case PriorityOrder:
			if a.Priority != b.Priority {
				return a.Priority < b.Priority
			}
		}
	}
	return false
}

func selectActive(prefs []priorityLevel, states []StreamerState, maxChannels int) []StreamerState {
	ranked := rankStreamers(prefs, states)
	n := maxChannels
	if n > len(ranked) {
		n = len(ranked)
	}
	return ranked[:n]
}

type snapshot struct {
	index  int
	state  StreamerState
	active bool
}

func diffSnapshots(previous, current []StreamerState) (added, removed []StreamerState) {
	prevMap := make(map[string]snapshot, len(previous))
	for i, state := range previous {
		prevMap[state.Login] = snapshot{index: i, state: state, active: true}
	}

	currMap := make(map[string]snapshot, len(current))
	for i, state := range current {
		currMap[state.Login] = snapshot{index: i, state: state, active: true}
	}

	for login, curr := range currMap {
		if _, ok := prevMap[login]; !ok {
			added = append(added, curr.state)
		}
	}
	for login, prev := range prevMap {
		if _, ok := currMap[login]; !ok {
			removed = append(removed, prev.state)
		}
	}

	if added == nil {
		added = []StreamerState{}
	}
	if removed == nil {
		removed = []StreamerState{}
	}
	return added, removed
}

func stateSliceIndex(states []StreamerState, login string) int {
	return slices.IndexFunc(states, func(s StreamerState) bool {
		return s.Login == login
	})
}

func stateSliceHas(states []StreamerState, login string) bool {
	return slices.ContainsFunc(states, func(s StreamerState) bool {
		return s.Login == login
	})
}
