package gql

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
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

func writeJSON(t *testing.T, w http.ResponseWriter, value any) {
	t.Helper()
	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(value); err != nil {
		t.Fatalf("encode json: %v", err)
	}
}
