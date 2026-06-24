package store

import (
	"errors"
	"testing"
	"time"
)

func TestTokenStoreSaveLoad(t *testing.T) {
	store := NewTokenStore(t.TempDir())
	want := Token{
		AccessToken:  "access-token",
		RefreshToken: "refresh-token",
		TokenType:    "bearer",
		Scopes:       []string{"user:read:email"},
		ExpiresAt:    time.Now().Add(time.Hour).UTC().Round(time.Second),
		ObtainedAt:   time.Now().UTC().Round(time.Second),
	}

	if err := store.Save(want); err != nil {
		t.Fatalf("Save returned error: %v", err)
	}

	got, err := store.Load()
	if err != nil {
		t.Fatalf("Load returned error: %v", err)
	}
	if got.AccessToken != want.AccessToken {
		t.Fatalf("access token = %q, want %q", got.AccessToken, want.AccessToken)
	}
	if got.RefreshToken != want.RefreshToken {
		t.Fatalf("refresh token = %q, want %q", got.RefreshToken, want.RefreshToken)
	}
	if !got.ExpiresAt.Equal(want.ExpiresAt) {
		t.Fatalf("expires at = %s, want %s", got.ExpiresAt, want.ExpiresAt)
	}
}

func TestTokenStoreMissing(t *testing.T) {
	store := NewTokenStore(t.TempDir())
	if _, err := store.Load(); !errors.Is(err, ErrTokenNotFound) {
		t.Fatalf("Load error = %v, want ErrTokenNotFound", err)
	}
}
