package twitch

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestGetAllStreamsBatchesRequests(t *testing.T) {
	var batchSizes []int
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ids := r.URL.Query()["user_id"]
		batchSizes = append(batchSizes, len(ids))
		data := make([]map[string]any, 0, len(ids))
		for _, id := range ids {
			data = append(data, map[string]any{"id": "stream-" + id, "user_id": id})
		}
		writeJSON(t, w, map[string]any{"data": data})
	}))
	defer server.Close()

	userIDs := make([]string, 150)
	for i := range userIDs {
		userIDs[i] = fmt.Sprintf("%d", i)
	}

	client := Client{ClientID: "client-id", AccessToken: "access-token", HelixURL: server.URL}
	streams, err := client.GetAllStreams(context.Background(), userIDs)
	if err != nil {
		t.Fatalf("GetAllStreams returned error: %v", err)
	}
	if len(streams) != 150 {
		t.Fatalf("len(streams) = %d, want 150", len(streams))
	}
	if len(batchSizes) != 2 || batchSizes[0] != 100 || batchSizes[1] != 50 {
		t.Fatalf("batch sizes = %v, want [100 50]", batchSizes)
	}
}

func TestGetAllStreamsFailsOnAnyBatchError(t *testing.T) {
	requests := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests++
		if requests > 1 {
			w.WriteHeader(http.StatusTooManyRequests)
			writeJSON(t, w, map[string]any{"error": "Too Many Requests", "status": 429, "message": "rate limited"})
			return
		}
		ids := r.URL.Query()["user_id"]
		data := make([]map[string]any, 0, len(ids))
		for _, id := range ids {
			data = append(data, map[string]any{"id": "stream-" + id, "user_id": id})
		}
		writeJSON(t, w, map[string]any{"data": data})
	}))
	defer server.Close()

	userIDs := make([]string, 150)
	for i := range userIDs {
		userIDs[i] = fmt.Sprintf("%d", i)
	}

	client := Client{ClientID: "client-id", AccessToken: "access-token", HelixURL: server.URL}
	streams, err := client.GetAllStreams(context.Background(), userIDs)
	if err == nil {
		t.Fatalf("GetAllStreams returned nil error, want batch failure")
	}
	if streams != nil {
		t.Fatalf("GetAllStreams returned partial result %d streams, want nil", len(streams))
	}
}
