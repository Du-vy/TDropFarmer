package auth

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/Du-vy/TDropFarmer/internal/store"
	"github.com/Du-vy/TDropFarmer/internal/twitch/profile"
)

const (
	deviceGrantType = "urn:ietf:params:oauth:grant-type:device_code"

	// DefaultClientID is the Twitch Android TV OAuth client ID used by default.
	DefaultClientID = "ue6666qo983tsx6so1t0vnawi233wa"
	// LegacyClientID is the older Twitch Android App OAuth client ID supported for backwards compatibility.
	LegacyClientID = "kd1unb4b3q4t58fwlpcbzcbnm76a8fp"
)

type Endpoints struct {
	Device   string
	Token    string
	Validate string
}

func DefaultEndpoints() Endpoints {
	return Endpoints{
		Device:   "https://id.twitch.tv/oauth2/device",
		Token:    "https://id.twitch.tv/oauth2/token",
		Validate: "https://id.twitch.tv/oauth2/validate",
	}
}

type TokenStore interface {
	Load() (store.Token, error)
	Save(store.Token) error
}

type DeviceFlow struct {
	ClientID          string
	FallbackClientIDs []string
	Scopes            []string
	HTTPClient        *http.Client
	Endpoints         Endpoints
	Store             TokenStore

	PollIntervalOverride time.Duration
}

type DevicePrompt struct {
	UserCode        string
	VerificationURI string
	ExpiresIn       time.Duration
}

type ValidateResult struct {
	ClientID  string   `json:"client_id"`
	Login     string   `json:"login"`
	UserID    string   `json:"user_id"`
	Scopes    []string `json:"scopes"`
	ExpiresIn int      `json:"expires_in"`
}

func (f DeviceFlow) isClientCompatible(tokenClientID string) bool {
	if f.ClientID == "" || tokenClientID == "" {
		return true
	}
	if f.ClientID == tokenClientID {
		return true
	}
	for _, fallback := range f.FallbackClientIDs {
		if tokenClientID == fallback {
			return true
		}
	}
	// Built-in compatibility between official TDropFarmer client IDs
	if (f.ClientID == DefaultClientID || f.ClientID == LegacyClientID) &&
		(tokenClientID == DefaultClientID || tokenClientID == LegacyClientID) {
		return true
	}
	return false
}

func (f DeviceFlow) refreshClientCandidates(tokenClientID string) []string {
	var candidates []string
	add := func(id string) {
		if id == "" {
			return
		}
		for _, existing := range candidates {
			if existing == id {
				return
			}
		}
		candidates = append(candidates, id)
	}

	if tokenClientID != "" && f.isClientCompatible(tokenClientID) {
		add(tokenClientID)
	}
	add(f.ClientID)
	for _, fallback := range f.FallbackClientIDs {
		add(fallback)
	}
	if f.ClientID == DefaultClientID {
		add(LegacyClientID)
	} else if f.ClientID == LegacyClientID {
		add(DefaultClientID)
	}
	return candidates
}

func (f DeviceFlow) Login(ctx context.Context, prompt func(DevicePrompt)) (store.Token, error) {
	if f.Store == nil {
		return store.Token{}, fmt.Errorf("token store is required")
	}
	if f.ClientID == "" {
		return store.Token{}, fmt.Errorf("client_id is required")
	}

	device, err := f.requestDeviceCode(ctx)
	if err != nil {
		return store.Token{}, err
	}
	if prompt != nil {
		prompt(DevicePrompt{
			UserCode:        device.UserCode,
			VerificationURI: device.VerificationURI,
			ExpiresIn:       time.Duration(device.ExpiresIn) * time.Second,
		})
	}

	token, err := f.pollToken(ctx, device)
	if err != nil {
		return store.Token{}, err
	}
	token.ClientID = f.ClientID
	if err := f.Store.Save(token); err != nil {
		return store.Token{}, err
	}
	return token, nil
}

func (f DeviceFlow) ValidToken(ctx context.Context) (store.Token, ValidateResult, error) {
	if f.Store == nil {
		return store.Token{}, ValidateResult{}, fmt.Errorf("token store is required")
	}
	token, err := f.Store.Load()
	if err != nil {
		return store.Token{}, ValidateResult{}, err
	}

	if token.RefreshToken != "" && time.Until(token.ExpiresAt) < time.Minute {
		candidates := f.refreshClientCandidates(token.ClientID)
		var refreshed store.Token
		var refreshErr error
		for _, cid := range candidates {
			refreshed, refreshErr = f.RefreshWithClient(ctx, token.RefreshToken, cid)
			if refreshErr == nil {
				break
			}
		}
		if refreshErr == nil {
			if err := f.Store.Save(refreshed); err != nil {
				return store.Token{}, ValidateResult{}, err
			}
			token = refreshed
		}
	}

	validation, err := f.Validate(ctx, token.AccessToken)
	if err != nil {
		return store.Token{}, ValidateResult{}, err
	}
	if !f.isClientCompatible(validation.ClientID) {
		return store.Token{}, ValidateResult{}, fmt.Errorf("token client ID mismatch: token belongs to %s, config requires %s", validation.ClientID, f.ClientID)
	}

	if token.ClientID != validation.ClientID {
		token.ClientID = validation.ClientID
		_ = f.Store.Save(token)
	}

	return token, validation, nil
}

func (f DeviceFlow) Refresh(ctx context.Context, refreshToken string) (store.Token, error) {
	return f.RefreshWithClient(ctx, refreshToken, f.ClientID)
}

func (f DeviceFlow) RefreshWithClient(ctx context.Context, refreshToken, clientID string) (store.Token, error) {
	if refreshToken == "" {
		return store.Token{}, fmt.Errorf("refresh token is required")
	}
	if clientID == "" {
		clientID = f.ClientID
	}
	values := url.Values{}
	values.Set("client_id", clientID)
	values.Set("grant_type", "refresh_token")
	values.Set("refresh_token", refreshToken)

	var response tokenResponse
	if err := f.postForm(ctx, f.endpoints().Token, values, &response); err != nil {
		return store.Token{}, err
	}
	token := response.toStoreToken(time.Now())
	token.ClientID = clientID
	return token, nil
}

func (f DeviceFlow) Validate(ctx context.Context, accessToken string) (ValidateResult, error) {
	if accessToken == "" {
		return ValidateResult{}, fmt.Errorf("access token is required")
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, f.endpoints().Validate, nil)
	if err != nil {
		return ValidateResult{}, err
	}
	req.Header.Set("Authorization", "OAuth "+accessToken)

	var result ValidateResult
	if err := f.doJSON(req, &result); err != nil {
		return ValidateResult{}, err
	}
	return result, nil
}

func (f DeviceFlow) requestDeviceCode(ctx context.Context) (deviceCodeResponse, error) {
	values := url.Values{}
	values.Set("client_id", f.ClientID)
	if len(f.Scopes) > 0 {
		values.Set("scopes", strings.Join(f.Scopes, " "))
	}

	var response deviceCodeResponse
	if err := f.postForm(ctx, f.endpoints().Device, values, &response); err != nil {
		return deviceCodeResponse{}, err
	}
	if response.DeviceCode == "" || response.UserCode == "" || response.VerificationURI == "" {
		return deviceCodeResponse{}, fmt.Errorf("device-code response is missing required fields")
	}
	if response.Interval <= 0 {
		response.Interval = 5
	}
	return response, nil
}

func (f DeviceFlow) pollToken(ctx context.Context, device deviceCodeResponse) (store.Token, error) {
	deadline := time.Now().Add(time.Duration(device.ExpiresIn) * time.Second)
	interval := time.Duration(device.Interval) * time.Second
	if f.PollIntervalOverride > 0 {
		interval = f.PollIntervalOverride
	}

	for {
		if time.Now().After(deadline) {
			return store.Token{}, fmt.Errorf("device code expired")
		}

		timer := time.NewTimer(interval)
		select {
		case <-ctx.Done():
			timer.Stop()
			return store.Token{}, ctx.Err()
		case <-timer.C:
		}

		values := url.Values{}
		values.Set("client_id", f.ClientID)
		values.Set("device_code", device.DeviceCode)
		values.Set("grant_type", deviceGrantType)

		var response tokenResponse
		err := f.postForm(ctx, f.endpoints().Token, values, &response)
		if err == nil {
			token := response.toStoreToken(time.Now())
			token.ClientID = f.ClientID
			return token, nil
		}

		var oauthErr OAuthError
		if !errors.As(err, &oauthErr) {
			return store.Token{}, err
		}
		switch oauthErr.ErrorCode {
		case "authorization_pending":
			continue
		case "slow_down":
			interval += 5 * time.Second
			continue
		case "expired_token":
			return store.Token{}, fmt.Errorf("device code expired")
		case "access_denied":
			return store.Token{}, fmt.Errorf("device authorization denied")
		default:
			return store.Token{}, oauthErr
		}
	}
}

func (f DeviceFlow) postForm(ctx context.Context, endpoint string, values url.Values, target any) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, strings.NewReader(values.Encode()))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Accept", "application/json")
	return f.doJSON(req, target)
}

func (f DeviceFlow) doJSON(req *http.Request, target any) error {
	profile.Apply(req)
	client := f.HTTPClient
	if client == nil {
		client = &http.Client{Timeout: 20 * time.Second}
	}

	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return err
	}

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		var oauthErr OAuthError
		if err := json.Unmarshal(body, &oauthErr); err == nil && (oauthErr.ErrorCode != "" || oauthErr.Message != "") {
			if oauthErr.ErrorCode == "" {
				oauthErr.ErrorCode = oauthErr.Message
			}
			oauthErr.StatusCode = resp.StatusCode
			return oauthErr
		}
		return fmt.Errorf("twitch oauth request failed: status %d, body %q", resp.StatusCode, string(body))
	}

	if target == nil {
		return nil
	}
	if err := json.Unmarshal(body, target); err != nil {
		return fmt.Errorf("decode twitch oauth response: %w", err)
	}
	return nil
}

func (f DeviceFlow) endpoints() Endpoints {
	endpoints := f.Endpoints
	defaults := DefaultEndpoints()
	if endpoints.Device == "" {
		endpoints.Device = defaults.Device
	}
	if endpoints.Token == "" {
		endpoints.Token = defaults.Token
	}
	if endpoints.Validate == "" {
		endpoints.Validate = defaults.Validate
	}
	return endpoints
}

type OAuthError struct {
	StatusCode int    `json:"-"`
	ErrorCode  string `json:"error"`
	Message    string `json:"message"`
}

func (e OAuthError) Error() string {
	if e.Message != "" {
		return e.Message
	}
	return e.ErrorCode
}

type deviceCodeResponse struct {
	DeviceCode      string `json:"device_code"`
	UserCode        string `json:"user_code"`
	VerificationURI string `json:"verification_uri"`
	ExpiresIn       int    `json:"expires_in"`
	Interval        int    `json:"interval"`
}

type tokenResponse struct {
	AccessToken  string   `json:"access_token"`
	RefreshToken string   `json:"refresh_token"`
	TokenType    string   `json:"token_type"`
	Scopes       []string `json:"scope"`
	ExpiresIn    int      `json:"expires_in"`
}

func (r tokenResponse) toStoreToken(now time.Time) store.Token {
	expiresIn := r.ExpiresIn
	if expiresIn <= 0 {
		expiresIn = 3600
	}
	return store.Token{
		AccessToken:  r.AccessToken,
		RefreshToken: r.RefreshToken,
		TokenType:    r.TokenType,
		Scopes:       r.Scopes,
		ExpiresAt:    now.Add(time.Duration(expiresIn) * time.Second),
		ObtainedAt:   now,
	}
}
