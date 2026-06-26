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
		{Login: "a", StreakReady: true, Priority: 0, Online: true},
		{Login: "b", StreakReady: false, Priority: 1, Online: true},
		{Login: "c", StreakReady: false, Priority: 2, Online: true},
	}
	prefs := parsePriorities([]string{"streak", "order"})
	active := selectActive(prefs, states, 2)
	if len(active) != 2 {
		t.Fatalf("len = %d, want 2", len(active))
	}
	if active[0].Login != "a" || active[1].Login != "b" {
		t.Fatalf("active = %v, want [a b]", logins(active))
	}
}

func TestSelectActiveMaxOne(t *testing.T) {
	states := []StreamerState{
		{Login: "a", StreakReady: true, Online: true},
		{Login: "b", StreakReady: true, Online: true},
	}
	prefs := parsePriorities([]string{"streak", "order"})
	active := selectActive(prefs, states, 1)
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
		{Login: "a", Priority: 0, Online: false},
		{Login: "b", Priority: 1, Online: true},
		{Login: "c", Priority: 2, Online: false},
		{Login: "d", Priority: 3, Online: true},
	}
	prefs := parsePriorities([]string{"order"})
	active := selectActive(prefs, states, 3)
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

func logins(states []StreamerState) []string {
	logins := make([]string, len(states))
	for i, state := range states {
		logins[i] = state.Login
	}
	return logins
}
