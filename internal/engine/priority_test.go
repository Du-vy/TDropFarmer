package engine

import (
	"reflect"
	"testing"
)

func TestRankStreakFirst(t *testing.T) {
	states := []StreamerState{
		{Login: "a", StreakReady: false, Priority: 0},
		{Login: "b", StreakReady: true, Priority: 1},
		{Login: "c", StreakReady: false, Priority: 2},
	}
	ranked := rankStreamers(states)
	if len(ranked) != 3 {
		t.Fatalf("len = %d, want 3", len(ranked))
	}
	if ranked[0].Login != "b" {
		t.Fatalf("first = %q, want b", ranked[0].Login)
	}
}
func TestSelectActive(t *testing.T) {
	states := []StreamerState{
		{Login: "a", StreakReady: true, Priority: 0, Online: true, GameName: "GameA"},
		{Login: "b", StreakReady: false, Priority: 1, Online: true, GameName: "GameB"},
		{Login: "c", StreakReady: false, Priority: 2, Online: true, GameName: "GameC"},
	}
	active := selectActive(states, nil, false)
	if len(active) != 1 {
		t.Fatalf("len = %d, want 1", len(active))
	}
	if active[0].Login != "a" {
		t.Fatalf("active = %v, want [a]", logins(active))
	}
}

func TestSelectActiveStableSort(t *testing.T) {
	states := []StreamerState{
		{Login: "a", Priority: 0, StreakReady: false},
		{Login: "b", Priority: 1, StreakReady: false},
	}
	ranked := rankStreamers(states)
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
	active := selectActive(states, nil, false)
	// Only 'b' and 'd' are online, and with limit 1, it should choose 'b' (higher priority)
	if len(active) != 1 {
		t.Fatalf("len = %d, want 1", len(active))
	}
	if active[0].Login != "b" {
		t.Fatalf("active = %v, want [b]", logins(active))
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

	// Since limit is hardcoded to 1 dynamic stream, it should only pick the best dynamic stream (c1)
	active := selectActive(states, activeGames, true)
	if len(active) != 1 {
		t.Fatalf("len = %d, want 1", len(active))
	}

	// Verify it picked c1
	hasC1 := stateSliceHas(active, "c1")
	if !hasC1 {
		t.Fatalf("active = %v, want [c1]", logins(active))
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

	// It must pick the static stream 'smite' (even though SMITE 2 is not in activeGames)
	// and the best dynamic stream 'c1' for Corepunk.
	active := selectActive(states, activeGames, true)
	if len(active) != 2 {
		t.Fatalf("len = %d, want 2", len(active))
	}

	hasSmite := stateSliceHas(active, "smite")
	hasC1 := stateSliceHas(active, "c1")
	if !hasSmite || !hasC1 {
		t.Fatalf("active = %v, want [smite c1]", logins(active))
	}
}

func TestSelectActiveDynamicCampaignsLimit(t *testing.T) {
	activeGames := []string{"Corepunk", "THE FINALS", "Albion Online"}

	states := []StreamerState{
		// Static online stream (not in active games list, or matches, shouldn't count towards dynamic limit)
		{Login: "smite", Online: true, GameName: "SMITE 2", IsStatic: true, Priority: 10},
		// Dynamic streams for 3 different active games
		{Login: "c1", Online: true, GameName: "Corepunk", IsStatic: false, Priority: 1},
		{Login: "f1", Online: true, GameName: "THE FINALS", IsStatic: false, Priority: 2},
		{Login: "a1", Online: true, GameName: "Albion Online", IsStatic: false, Priority: 3},
	}

	// With limit hardcoded to 1, it should pick the static stream 'smite' AND 1 dynamic stream (c1)
	active := selectActive(states, activeGames, true)
	if len(active) != 2 {
		t.Fatalf("len = %d, want 2 (1 static + 1 dynamic)", len(active))
	}

	hasSmite := stateSliceHas(active, "smite")
	hasC1 := stateSliceHas(active, "c1")
	hasF1 := stateSliceHas(active, "f1")
	hasA1 := stateSliceHas(active, "a1")

	if !hasSmite || !hasC1 {
		t.Fatalf("active = %v, want [smite c1]", logins(active))
	}
	if hasF1 || hasA1 {
		t.Fatalf("active = %v, should not contain f1 or a1 due to dynamic limit of 1", logins(active))
	}
}

func TestSelectActivePrioritizesActiveGameOrderAcrossDynamicGames(t *testing.T) {
	activeGames := []string{"Corepunk", "THE FINALS"}
	states := []StreamerState{
		{Login: "finals", Online: true, GameName: "THE FINALS", IsStatic: false, Priority: 0},
		{Login: "corepunk", Online: true, GameName: "Corepunk", IsStatic: false, Priority: 10},
	}

	active := selectActive(states, activeGames, true)

	if len(active) != 1 {
		t.Fatalf("len = %d, want 1", len(active))
	}
	if active[0].Login != "corepunk" {
		t.Fatalf("active = %v, want [corepunk]", logins(active))
	}
}

func logins(states []StreamerState) []string {
	logins := make([]string, len(states))
	for i, state := range states {
		logins[i] = state.Login
	}
	return logins
}
