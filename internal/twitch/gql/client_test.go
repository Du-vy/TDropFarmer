package gql

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/Du-vy/TDropFarmer/internal/twitch/profile"
)

func TestDo(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			t.Fatalf("method = %s, want POST", r.Method)
		}
		if got := r.Header.Get("Client-Id"); got != "client-id" {
			t.Fatalf("Client-Id = %q, want client-id", got)
		}
		if got := r.Header.Get("Authorization"); got != "OAuth access-token" {
			t.Fatalf("Authorization = %q, want OAuth access-token", got)
		}
		if got := r.Header.Get("User-Agent"); got != profile.MobileAppUserAgent {
			t.Fatalf("User-Agent = %q, want %q", got, profile.MobileAppUserAgent)
		}
		var request Request
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			t.Fatalf("decode request: %v", err)
		}
		if request.Query != "query Test { viewer { id } }" {
			t.Fatalf("query = %q", request.Query)
		}
		writeJSON(t, w, map[string]any{"data": map[string]any{"viewer": map[string]any{"id": "1"}}})
	}))
	defer server.Close()

	client := Client{ClientID: "client-id", AccessToken: "access-token", Endpoint: server.URL}
	response, err := client.Do(context.Background(), Request{Query: "query Test { viewer { id } }"})
	if err != nil {
		t.Fatalf("Do returned error: %v", err)
	}
	if len(response.Data) == 0 {
		t.Fatalf("response data is empty")
	}
}

func TestDoGraphQLError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		writeJSON(t, w, map[string]any{"errors": []map[string]any{{"message": "bad query"}}})
	}))
	defer server.Close()

	client := Client{Endpoint: server.URL}
	if _, err := client.Do(context.Background(), Request{Query: "query Bad { bad }"}); err == nil {
		t.Fatalf("Do returned nil error, want graphql error")
	}
}

func TestIsPersistedQueryNotFound(t *testing.T) {
	tests := []struct {
		name string
		err  error
		want bool
	}{
		{name: "message", err: Error{Message: "PersistedQueryNotFound"}, want: true},
		{name: "extension code", err: Error{Message: "query missing", Extra: map[string]any{"code": "PERSISTED_QUERY_NOT_FOUND"}}, want: true},
		{name: "wrapped", err: fmt.Errorf("directory request: %w", Error{Message: "PersistedQueryNotFound"}), want: true},
		{name: "other graphql error", err: Error{Message: "service error"}, want: false},
		{name: "transport error", err: fmt.Errorf("request timeout"), want: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := IsPersistedQueryNotFound(tt.err); got != tt.want {
				t.Fatalf("IsPersistedQueryNotFound(%v) = %v, want %v", tt.err, got, tt.want)
			}
		})
	}
}

func TestDoRetriesOn429RespectsRetryAfter(t *testing.T) {
	attempts := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		attempts++
		if attempts <= 2 {
			w.Header().Set("Retry-After", "3")
			w.WriteHeader(http.StatusTooManyRequests)
			return
		}
		writeJSON(t, w, map[string]any{"data": map[string]any{"ok": true}})
	}))
	defer server.Close()

	var delays []time.Duration
	client := Client{
		Endpoint: server.URL,
		sleep: func(ctx context.Context, d time.Duration) error {
			delays = append(delays, d)
			return nil
		},
	}
	response, err := client.Do(context.Background(), Request{Query: "query T { t }"})
	if err != nil {
		t.Fatalf("Do returned error: %v", err)
	}
	if len(response.Data) == 0 {
		t.Fatalf("response data is empty")
	}
	if attempts != 3 {
		t.Fatalf("attempts = %d, want 3", attempts)
	}
	// First delay: max(base 2s, Retry-After 3s) = 3s. Second: max(4s, 3s) = 4s.
	want := []time.Duration{3 * time.Second, 4 * time.Second}
	if len(delays) != len(want) || delays[0] != want[0] || delays[1] != want[1] {
		t.Fatalf("delays = %v, want %v", delays, want)
	}
}

func TestDoRetriesOnServerError(t *testing.T) {
	attempts := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		attempts++
		if attempts == 1 {
			w.WriteHeader(http.StatusBadGateway)
			return
		}
		writeJSON(t, w, map[string]any{"data": map[string]any{"ok": true}})
	}))
	defer server.Close()

	client := Client{Endpoint: server.URL, sleep: noSleep}
	if _, err := client.Do(context.Background(), Request{Query: "query T { t }"}); err != nil {
		t.Fatalf("Do returned error: %v", err)
	}
	if attempts != 2 {
		t.Fatalf("attempts = %d, want 2", attempts)
	}
}

func TestDoDoesNotRetryClientError(t *testing.T) {
	attempts := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		attempts++
		w.WriteHeader(http.StatusUnauthorized)
	}))
	defer server.Close()

	client := Client{Endpoint: server.URL, sleep: noSleep}
	if _, err := client.Do(context.Background(), Request{Query: "query T { t }"}); err == nil {
		t.Fatalf("Do returned nil error, want status error")
	}
	if attempts != 1 {
		t.Fatalf("attempts = %d, want 1", attempts)
	}
}

func TestDoRetriesTransientServiceError(t *testing.T) {
	attempts := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		attempts++
		if attempts == 1 {
			writeJSON(t, w, map[string]any{"errors": []map[string]any{{"message": "service error"}}})
			return
		}
		writeJSON(t, w, map[string]any{"data": map[string]any{"ok": true}})
	}))
	defer server.Close()

	client := Client{Endpoint: server.URL, sleep: noSleep}
	if _, err := client.Do(context.Background(), Request{Query: "query T { t }"}); err != nil {
		t.Fatalf("Do returned error: %v", err)
	}
	if attempts != 2 {
		t.Fatalf("attempts = %d, want 2", attempts)
	}
}

func TestDoDoesNotRetryPersistedQueryNotFound(t *testing.T) {
	attempts := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		attempts++
		writeJSON(t, w, map[string]any{"errors": []map[string]any{{"message": "PersistedQueryNotFound"}}})
	}))
	defer server.Close()

	client := Client{Endpoint: server.URL, sleep: noSleep}
	_, err := client.Do(context.Background(), Request{OperationName: "Inventory", Extensions: map[string]any{"persistedQuery": map[string]any{}}})
	if err == nil {
		t.Fatalf("Do returned nil error, want PersistedQueryNotFound")
	}
	if !IsPersistedQueryNotFound(err) {
		t.Fatalf("error %v is not detected as PersistedQueryNotFound", err)
	}
	if attempts != 1 {
		t.Fatalf("attempts = %d, want 1", attempts)
	}
}

func TestDoStopsAfterMaxAttempts(t *testing.T) {
	attempts := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		attempts++
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer server.Close()

	client := Client{Endpoint: server.URL, MaxAttempts: 2, sleep: noSleep}
	if _, err := client.Do(context.Background(), Request{Query: "query T { t }"}); err == nil {
		t.Fatalf("Do returned nil error, want status error")
	}
	if attempts != 2 {
		t.Fatalf("attempts = %d, want 2", attempts)
	}
}

func TestRateLimiterEnforcesWindow(t *testing.T) {
	limiter := NewRateLimiter(2, 200*time.Millisecond)
	start := time.Now()
	for i := 0; i < 3; i++ {
		if err := limiter.Wait(context.Background()); err != nil {
			t.Fatalf("Wait returned error: %v", err)
		}
	}
	if elapsed := time.Since(start); elapsed < 150*time.Millisecond {
		t.Fatalf("third Wait returned after %v, want the window (~200ms) to have passed", elapsed)
	}
}

func TestRateLimiterNilIsNoop(t *testing.T) {
	var limiter *RateLimiter
	if err := limiter.Wait(context.Background()); err != nil {
		t.Fatalf("nil limiter Wait returned error: %v", err)
	}
}

func noSleep(ctx context.Context, d time.Duration) error {
	return nil
}

func writeJSON(t *testing.T, w http.ResponseWriter, value any) {
	t.Helper()
	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(value); err != nil {
		t.Fatalf("encode json: %v", err)
	}
}
