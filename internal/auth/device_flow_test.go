package auth

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"
	"time"

	"github.com/Du-vy/TDropFarmer/internal/store"
	"github.com/Du-vy/TDropFarmer/internal/twitch/profile"
)

type memoryTokenStore struct {
	token store.Token
	saved bool
}

func (s *memoryTokenStore) Load() (store.Token, error) {
	if !s.saved {
		return store.Token{}, store.ErrTokenNotFound
	}
	return s.token, nil
}

func (s *memoryTokenStore) Save(token store.Token) error {
	s.token = token
	s.saved = true
	return nil
}

func TestDeviceFlowLogin(t *testing.T) {
	tokenPolls := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assertMobileUserAgent(t, r)
		switch r.URL.Path {
		case "/device":
			assertFormValue(t, r, "client_id", "client-id")
			assertFormValue(t, r, "scopes", "user:read:email")
			writeJSON(t, w, map[string]any{
				"device_code":      "device-code",
				"user_code":        "ABCD-EFGH",
				"verification_uri": "https://www.twitch.tv/activate",
				"expires_in":       60,
				"interval":         5,
			})
		case "/token":
			tokenPolls++
			assertFormValue(t, r, "client_id", "client-id")
			assertFormValue(t, r, "device_code", "device-code")
			assertFormValue(t, r, "grant_type", deviceGrantType)
			if tokenPolls == 1 {
				w.WriteHeader(http.StatusBadRequest)
				writeJSON(t, w, map[string]any{"error": "authorization_pending", "message": "pending"})
				return
			}
			writeJSON(t, w, map[string]any{
				"access_token":  "access-token",
				"refresh_token": "refresh-token",
				"token_type":    "bearer",
				"scope":         []string{"user:read:email"},
				"expires_in":    3600,
			})
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	store := &memoryTokenStore{}
	flow := DeviceFlow{
		ClientID: "client-id",
		Scopes:   []string{"user:read:email"},
		Endpoints: Endpoints{
			Device: server.URL + "/device",
			Token:  server.URL + "/token",
		},
		Store:                store,
		PollIntervalOverride: time.Millisecond,
	}

	prompted := false
	token, err := flow.Login(context.Background(), func(prompt DevicePrompt) {
		prompted = true
		if prompt.UserCode != "ABCD-EFGH" {
			t.Fatalf("prompt code = %q, want ABCD-EFGH", prompt.UserCode)
		}
	})
	if err != nil {
		t.Fatalf("Login returned error: %v", err)
	}
	if !prompted {
		t.Fatalf("prompt was not called")
	}
	if token.AccessToken != "access-token" {
		t.Fatalf("access token = %q, want access-token", token.AccessToken)
	}
	if !store.saved {
		t.Fatalf("token was not saved")
	}
}

func TestDeviceFlowValidate(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assertMobileUserAgent(t, r)
		if r.URL.Path != "/validate" {
			http.NotFound(w, r)
			return
		}
		if got := r.Header.Get("Authorization"); got != "OAuth access-token" {
			t.Fatalf("Authorization = %q, want OAuth access-token", got)
		}
		writeJSON(t, w, map[string]any{
			"client_id":  "client-id",
			"login":      "my_user",
			"user_id":    "1234",
			"scopes":     []string{"user:read:email"},
			"expires_in": 3000,
		})
	}))
	defer server.Close()

	flow := DeviceFlow{Endpoints: Endpoints{Validate: server.URL + "/validate"}}
	result, err := flow.Validate(context.Background(), "access-token")
	if err != nil {
		t.Fatalf("Validate returned error: %v", err)
	}
	if result.Login != "my_user" {
		t.Fatalf("login = %q, want my_user", result.Login)
	}
}

func TestDeviceFlowRefresh(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assertMobileUserAgent(t, r)
		assertFormValue(t, r, "client_id", "client-id")
		assertFormValue(t, r, "grant_type", "refresh_token")
		assertFormValue(t, r, "refresh_token", "old-refresh-token")
		writeJSON(t, w, map[string]any{
			"access_token":  "new-access-token",
			"refresh_token": "new-refresh-token",
			"token_type":    "bearer",
			"expires_in":    3600,
		})
	}))
	defer server.Close()

	flow := DeviceFlow{ClientID: "client-id", Endpoints: Endpoints{Token: server.URL}}
	token, err := flow.Refresh(context.Background(), "old-refresh-token")
	if err != nil {
		t.Fatalf("Refresh returned error: %v", err)
	}
	if token.AccessToken != "new-access-token" {
		t.Fatalf("access token = %q, want new-access-token", token.AccessToken)
	}
	if token.ClientID != "client-id" {
		t.Fatalf("token client_id = %q, want client-id", token.ClientID)
	}
}

func TestDeviceFlowValidToken_LegacyCompat(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assertMobileUserAgent(t, r)
		if r.URL.Path != "/validate" {
			http.NotFound(w, r)
			return
		}
		if got := r.Header.Get("Authorization"); got != "OAuth legacy-access-token" {
			t.Fatalf("Authorization = %q, want OAuth legacy-access-token", got)
		}
		writeJSON(t, w, map[string]any{
			"client_id":  LegacyClientID,
			"login":      "legacy_user",
			"user_id":    "5678",
			"scopes":     []string{"user:read:email"},
			"expires_in": 3600,
		})
	}))
	defer server.Close()

	store := &memoryTokenStore{
		saved: true,
		token: store.Token{
			AccessToken:  "legacy-access-token",
			RefreshToken: "legacy-refresh-token",
			TokenType:    "bearer",
			ExpiresAt:    time.Now().Add(2 * time.Hour),
		},
	}

	flow := DeviceFlow{
		ClientID:  DefaultClientID,
		Endpoints: Endpoints{Validate: server.URL + "/validate"},
		Store:     store,
	}

	token, validation, err := flow.ValidToken(context.Background())
	if err != nil {
		t.Fatalf("ValidToken failed for legacy client ID: %v", err)
	}
	if validation.ClientID != LegacyClientID {
		t.Fatalf("validation client_id = %q, want %q", validation.ClientID, LegacyClientID)
	}
	if token.ClientID != LegacyClientID {
		t.Fatalf("token client_id was not updated: got %q, want %q", token.ClientID, LegacyClientID)
	}
	if store.token.ClientID != LegacyClientID {
		t.Fatalf("store token client_id was not saved: got %q, want %q", store.token.ClientID, LegacyClientID)
	}
}

func TestDeviceFlowValidToken_MismatchError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assertMobileUserAgent(t, r)
		if r.URL.Path != "/validate" {
			http.NotFound(w, r)
			return
		}
		writeJSON(t, w, map[string]any{
			"client_id":  "unrelated-client-id",
			"login":      "some_user",
			"user_id":    "9999",
			"scopes":     []string{"user:read:email"},
			"expires_in": 3600,
		})
	}))
	defer server.Close()

	store := &memoryTokenStore{
		saved: true,
		token: store.Token{
			AccessToken: "some-access-token",
			ExpiresAt:   time.Now().Add(time.Hour),
		},
	}

	flow := DeviceFlow{
		ClientID:  DefaultClientID,
		Endpoints: Endpoints{Validate: server.URL + "/validate"},
		Store:     store,
	}

	_, _, err := flow.ValidToken(context.Background())
	if err == nil {
		t.Fatalf("expected mismatch error, got nil")
	}
	expectedMsg := "token client ID mismatch: token belongs to unrelated-client-id, config requires " + DefaultClientID
	if err.Error() != expectedMsg {
		t.Fatalf("error = %q, want %q", err.Error(), expectedMsg)
	}
}

func TestDeviceFlowValidToken_RefreshWithLegacyFallback(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assertMobileUserAgent(t, r)
		switch r.URL.Path {
		case "/token":
			cid := r.PostFormValue("client_id")
			if cid == DefaultClientID {
				w.WriteHeader(http.StatusBadRequest)
				writeJSON(t, w, map[string]any{"error": "invalid_grant", "message": "invalid client for refresh token"})
				return
			}
			if cid == LegacyClientID {
				writeJSON(t, w, map[string]any{
					"access_token":  "refreshed-legacy-access-token",
					"refresh_token": "refreshed-legacy-refresh-token",
					"token_type":    "bearer",
					"expires_in":    3600,
				})
				return
			}
			http.Error(w, "unexpected client_id", http.StatusBadRequest)
		case "/validate":
			if got := r.Header.Get("Authorization"); got != "OAuth refreshed-legacy-access-token" {
				t.Fatalf("Authorization = %q, want OAuth refreshed-legacy-access-token", got)
			}
			writeJSON(t, w, map[string]any{
				"client_id":  LegacyClientID,
				"login":      "legacy_user",
				"user_id":    "5678",
				"scopes":     []string{"user:read:email"},
				"expires_in": 3600,
			})
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	// Token expired 10 minutes ago, no client_id saved originally
	store := &memoryTokenStore{
		saved: true,
		token: store.Token{
			AccessToken:  "old-expired-token",
			RefreshToken: "old-legacy-refresh-token",
			TokenType:    "bearer",
			ExpiresAt:    time.Now().Add(-10 * time.Minute),
		},
	}

	flow := DeviceFlow{
		ClientID: DefaultClientID,
		Endpoints: Endpoints{
			Token:    server.URL + "/token",
			Validate: server.URL + "/validate",
		},
		Store: store,
	}

	token, validation, err := flow.ValidToken(context.Background())
	if err != nil {
		t.Fatalf("ValidToken failed with legacy refresh fallback: %v", err)
	}
	if token.AccessToken != "refreshed-legacy-access-token" {
		t.Fatalf("access token = %q, want refreshed-legacy-access-token", token.AccessToken)
	}
	if token.ClientID != LegacyClientID {
		t.Fatalf("token client_id = %q, want %q", token.ClientID, LegacyClientID)
	}
	if validation.ClientID != LegacyClientID {
		t.Fatalf("validation client_id = %q, want %q", validation.ClientID, LegacyClientID)
	}
	if store.token.ClientID != LegacyClientID {
		t.Fatalf("stored token client_id = %q, want %q", store.token.ClientID, LegacyClientID)
	}
}

func assertMobileUserAgent(t *testing.T, r *http.Request) {
	t.Helper()
	if got := r.Header.Get("User-Agent"); got != profile.UserAgent {
		t.Fatalf("User-Agent = %q, want %q", got, profile.UserAgent)
	}
}

func assertFormValue(t *testing.T, r *http.Request, key string, want string) {
	t.Helper()
	body, err := ioReadForm(r)
	if err != nil {
		t.Fatalf("read form: %v", err)
	}
	if got := body.Get(key); got != want {
		t.Fatalf("form %s = %q, want %q", key, got, want)
	}
}

func ioReadForm(r *http.Request) (url.Values, error) {
	if err := r.ParseForm(); err != nil {
		return nil, err
	}
	return r.PostForm, nil
}

func writeJSON(t *testing.T, w http.ResponseWriter, value any) {
	t.Helper()
	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(value); err != nil {
		t.Fatalf("encode json: %v", err)
	}
}
