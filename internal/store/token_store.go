package store

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"
)

var ErrTokenNotFound = errors.New("token not found")

type Token struct {
	AccessToken  string    `json:"access_token"`
	RefreshToken string    `json:"refresh_token,omitempty"`
	TokenType    string    `json:"token_type"`
	Scopes       []string  `json:"scopes,omitempty"`
	ExpiresAt    time.Time `json:"expires_at"`
	ObtainedAt   time.Time `json:"obtained_at"`
}

type TokenStore struct {
	path string
}

func NewTokenStore(dataDir string) TokenStore {
	return TokenStore{path: filepath.Join(dataDir, "token.json")}
}

func (s TokenStore) Load() (Token, error) {
	data, err := os.ReadFile(s.path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return Token{}, ErrTokenNotFound
		}
		return Token{}, fmt.Errorf("read token file: %w", err)
	}

	var token Token
	if err := json.Unmarshal(data, &token); err != nil {
		return Token{}, fmt.Errorf("parse token file: %w", err)
	}
	return token, nil
}

func (s TokenStore) Save(token Token) error {
	if token.AccessToken == "" {
		return fmt.Errorf("access token must not be empty")
	}
	if err := os.MkdirAll(filepath.Dir(s.path), 0o700); err != nil {
		return fmt.Errorf("create token directory: %w", err)
	}

	data, err := json.MarshalIndent(token, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal token: %w", err)
	}
	data = append(data, '\n')

	tmp, err := os.CreateTemp(filepath.Dir(s.path), ".token-*.tmp")
	if err != nil {
		return fmt.Errorf("create temporary token file: %w", err)
	}
	tmpPath := tmp.Name()
	cleanup := true
	defer func() {
		if cleanup {
			_ = os.Remove(tmpPath)
		}
	}()

	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("write temporary token file: %w", err)
	}
	if err := tmp.Chmod(0o600); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("chmod temporary token file: %w", err)
	}
	if err := tmp.Sync(); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("sync temporary token file: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("close temporary token file: %w", err)
	}

	if err := replaceFile(tmpPath, s.path); err != nil {
		return err
	}
	cleanup = false
	return nil
}

func replaceFile(tmpPath, targetPath string) error {
	backupPath := targetPath + ".bak"
	_ = os.Remove(backupPath)

	if _, err := os.Stat(targetPath); err == nil {
		if err := os.Rename(targetPath, backupPath); err != nil {
			return fmt.Errorf("backup existing token file: %w", err)
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("stat token file: %w", err)
	}

	if err := os.Rename(tmpPath, targetPath); err != nil {
		if _, statErr := os.Stat(backupPath); statErr == nil {
			_ = os.Rename(backupPath, targetPath)
		}
		return fmt.Errorf("replace token file: %w", err)
	}

	_ = os.Remove(backupPath)
	return nil
}
