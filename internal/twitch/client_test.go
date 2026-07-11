package twitch

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/Du-vy/TDropFarmer/internal/twitch/profile"
)

func TestResolveUsers(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/helix/users" {
			http.NotFound(w, r)
			return
		}
		if got := r.Header.Get("Client-Id"); got != "client-id" {
			t.Fatalf("Client-Id = %q, want client-id", got)
		}
		if got := r.Header.Get("Authorization"); got != "Bearer access-token" {
			t.Fatalf("Authorization = %q, want Bearer access-token", got)
		}
		if got := r.Header.Get("User-Agent"); got != profile.MobileAppUserAgent {
			t.Fatalf("User-Agent = %q, want %q", got, profile.MobileAppUserAgent)
		}
		logins := r.URL.Query()["login"]
		if len(logins) != 2 || logins[0] != "one" || logins[1] != "two" {
			t.Fatalf("login query = %#v, want one and two", logins)
		}
		writeJSON(t, w, map[string]any{
			"data": []map[string]any{
				{"id": "1", "login": "one", "display_name": "One"},
				{"id": "2", "login": "two", "display_name": "Two"},
			},
		})
	}))
	defer server.Close()

	client := Client{ClientID: "client-id", AccessToken: "access-token", HelixURL: server.URL + "/helix"}
	users, err := client.ResolveUsers(context.Background(), []string{"One", "two", "one"})
	if err != nil {
		t.Fatalf("ResolveUsers returned error: %v", err)
	}
	if len(users) != 2 {
		t.Fatalf("len(users) = %d, want 2", len(users))
	}
	if users[0].Login != "one" || users[1].Login != "two" {
		t.Fatalf("users = %#v, want one and two", users)
	}
}

func TestResolveUsersError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		writeJSON(t, w, map[string]any{"error": "Unauthorized", "status": 401, "message": "invalid token"})
	}))
	defer server.Close()

	client := Client{ClientID: "client-id", AccessToken: "access-token", HelixURL: server.URL}
	if _, err := client.ResolveUsers(context.Background(), []string{"one"}); err == nil {
		t.Fatalf("ResolveUsers returned nil error, want unauthorized error")
	}
}

func writeJSON(t *testing.T, w http.ResponseWriter, value any) {
	t.Helper()
	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(value); err != nil {
		t.Fatalf("encode json: %v", err)
	}
}
