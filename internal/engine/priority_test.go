package engine

import (
	"reflect"
	"testing"
)

func TestParsePriorities(t *testing.T) {
	prefs := parsePriorities([]string{"streak", "order", "points_ascending"})
	if len(prefs) != 3 {
		t.Fatalf("len = %d, want 3", len(prefs))
	}
	if prefs[0].kind != PriorityStreak || prefs[1].kind != PriorityOrder || prefs[2].kind != PriorityPointsAscending {
		t.Fatalf("prefs = %#v", prefs)
	}
}

func TestRankStreakFirst(t *testing.T) {
	states := []StreamerState{
		{Login: "a", StreakReady: false, Priority: 0},
		{Login: "b", StreakReady: true, Priority: 1},
		{Login: "c", StreakReady: false, Priority: 2},
	}
	prefs := parsePriorities([]string{"streak", "order"})
	ranked := rankStreamers(prefs, states)
	if len(ranked) != 3 {
		t.Fatalf("len = %d, want 3", len(ranked))
	}
	if ranked[0].Login != "b" {
		t.Fatalf("first = %q, want b", ranked[0].Login)
	}
}

func TestRankPointsAscending(t *testing.T) {
	states := []StreamerState{
		{Login: "a", Points: 100},
		{Login: "b", Points: 10},
		{Login: "c", Points: 50},
	}
	prefs := parsePriorities([]string{"points_ascending"})
	ranked := rankStreamers(prefs, states)
	if len(ranked) != 3 {
		t.Fatalf("len = %d, want 3", len(ranked))
	}
	if ranked[0].Login != "b" || ranked[1].Login != "c" || ranked[2].Login != "a" {
		t.Fatalf("ranked = %v, want [b c a]", logins(ranked))
	}
}

func TestRankPointsDescending(t *testing.T) {
	states := []StreamerState{
		{Login: "a", Points: 10},
		{Login: "b", Points: 100},
		{Login: "c", Points: 50},
	}
	prefs := parsePriorities([]string{"points_descending"})
	ranked := rankStreamers(prefs, states)
	if ranked[0].Login != "b" || ranked[1].Login != "c" || ranked[2].Login != "a" {
		t.Fatalf("ranked = %v, want [b c a]", logins(ranked))
	}
}

func TestSelectActive(t *testing.T) {
	states := []StreamerState{
		{Login: "a", StreakReady: true, Priority: 0, Online: true, GameName: "GameA"},
		{Login: "b", StreakReady: false, Priority: 1, Online: true, GameName: "GameB"},
		{Login: "c", StreakReady: false, Priority: 2, Online: true, GameName: "GameC"},
	}
	prefs := parsePriorities([]string{"streak", "order"})
	active := selectActive(prefs, states, nil, false, 2)
	if len(active) != 2 {
		t.Fatalf("len = %d, want 2", len(active))
	}
	if active[0].Login != "a" || active[1].Login != "b" {
		t.Fatalf("active = %v, want [a b]", logins(active))
	}
}

func TestSelectActiveMaxOne(t *testing.T) {
	states := []StreamerState{
		{Login: "a", StreakReady: true, Online: true, GameName: "GameA"},
		{Login: "b", StreakReady: true, Online: true, GameName: "GameB"},
	}
	prefs := parsePriorities([]string{"streak", "order"})
	active := selectActive(prefs, states, nil, false, 1)
	if len(active) != 1 {
		t.Fatalf("len = %d, want 1", len(active))
	}
}

func TestSelectActiveStableSort(t *testing.T) {
	states := []StreamerState{
		{Login: "a", Priority: 0, StreakReady: false},
		{Login: "b", Priority: 1, StreakReady: false},
	}
	prefs := parsePriorities([]string{"order"})
	ranked := rankStreamers(prefs, states)
	if ranked[0].Login != "a" || ranked[1].Login != "b" {
		t.Fatalf("ranked = %v, want [a b]", logins(ranked))
	}
}

func TestSelectActiveFiltersOffline(t *testing.T) {
	states := []StreamerState{
		{Login: "a", Priority: 0, Online: false, GameName: "GameA"},
		{Login: "b", Priority: 1, Online: true, GameName: "GameB"},
		{Login: "c", Priority: 2, Online: false, GameName: "GameC"},
		{Login: "d", Priority: 3, Online: true, GameName: "GameD"},
	}
	prefs := parsePriorities([]string{"order"})
	active := selectActive(prefs, states, nil, false, 3)
	// Only 'b' and 'd' are online, so only they should be selected
	if len(active) != 2 {
		t.Fatalf("len = %d, want 2", len(active))
	}
	if active[0].Login != "b" || active[1].Login != "d" {
		t.Fatalf("active = %v, want [b d]", logins(active))
	}
}

func TestDiffSnapshots(t *testing.T) {
	prev := []StreamerState{
		{Login: "a"},
		{Login: "b"},
	}
	curr := []StreamerState{
		{Login: "b"},
		{Login: "c"},
	}
	added, removed := diffSnapshots(prev, curr)
	if !reflect.DeepEqual(logins(added), []string{"c"}) {
		t.Fatalf("added = %v, want [c]", logins(added))
	}
	if !reflect.DeepEqual(logins(removed), []string{"a"}) {
		t.Fatalf("removed = %v, want [a]", logins(removed))
	}
}

func TestDiffSnapshotsNone(t *testing.T) {
	prev := []StreamerState{{Login: "a"}}
	curr := []StreamerState{{Login: "a"}}
	added, removed := diffSnapshots(prev, curr)
	if len(added) != 0 || len(removed) != 0 {
		t.Fatalf("added=%v removed=%v, want none", added, removed)
	}
}

func TestDiffSnapshotsEmpty(t *testing.T) {
	added, removed := diffSnapshots(nil, nil)
	if len(added) != 0 || len(removed) != 0 {
		t.Fatalf("added=%v removed=%v, want none", added, removed)
	}
}

func TestSelectActiveOnePerCampaign(t *testing.T) {
	// Active games list
	activeGames := []string{"Corepunk", "THE FINALS"}

	states := []StreamerState{
		// Game: Corepunk (dynamic online)
		{Login: "c1", Online: true, GameName: "Corepunk", IsStatic: false, Priority: 1},
		{Login: "c2", Online: true, GameName: "Corepunk", IsStatic: false, Priority: 2},
		// Game: THE FINALS (dynamic online)
		{Login: "f1", Online: true, GameName: "THE FINALS", IsStatic: false, Priority: 3},
		{Login: "f2", Online: true, GameName: "THE FINALS", IsStatic: false, Priority: 4},
		// Game: SMITE 2 (dynamic online but SMITE 2 not in activeGames list)
		{Login: "s1", Online: true, GameName: "SMITE 2", IsStatic: false, Priority: 0},
	}

	prefs := parsePriorities([]string{"order"})

	// With maxChannels = 2, it should pick 1 for Corepunk (c1) and 1 for THE FINALS (f1)
	active := selectActive(prefs, states, activeGames, true, 2)
	if len(active) != 2 {
		t.Fatalf("len = %d, want 2", len(active))
	}

	// Verify it picked c1 and f1
	hasC1 := stateSliceHas(active, "c1")
	hasF1 := stateSliceHas(active, "f1")
	if !hasC1 || !hasF1 {
		t.Fatalf("active = %v, want [c1 f1]", logins(active))
	}
}

func TestSelectActiveStaticAlwaysOnline(t *testing.T) {
	activeGames := []string{"Corepunk"}

	states := []StreamerState{
		// Static online stream (Smite 2)
		{Login: "smite", Online: true, GameName: "SMITE 2", IsStatic: true, Priority: 10},
		// Dynamic online streams (Corepunk)
		{Login: "c1", Online: true, GameName: "Corepunk", IsStatic: false, Priority: 1},
		{Login: "c2", Online: true, GameName: "Corepunk", IsStatic: false, Priority: 2},
	}

	prefs := parsePriorities([]string{"order"})

	// With maxChannels = 2, it must pick the static stream 'smite' (even though SMITE 2 is not in activeGames)
	// and the best dynamic stream 'c1' for Corepunk.
	active := selectActive(prefs, states, activeGames, true, 2)
	if len(active) != 2 {
		t.Fatalf("len = %d, want 2", len(active))
	}

	hasSmite := stateSliceHas(active, "smite")
	hasC1 := stateSliceHas(active, "c1")
	if !hasSmite || !hasC1 {
		t.Fatalf("active = %v, want [smite c1]", logins(active))
	}
}

func logins(states []StreamerState) []string {
	logins := make([]string, len(states))
	for i, state := range states {
		logins[i] = state.Login
	}
	return logins
}
