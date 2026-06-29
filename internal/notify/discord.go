package notify

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"time"
)

type DiscordNotifier struct {
	WebhookURL string
	HTTPClient *http.Client
}

type EmbedField struct {
	Name   string `json:"name"`
	Value  string `json:"value"`
	Inline bool   `json:"inline,omitempty"`
}

type EmbedMedia struct {
	URL string `json:"url"`
}

type EmbedAuthor struct {
	Name    string `json:"name"`
	IconURL string `json:"icon_url,omitempty"`
}

type EmbedFooter struct {
	Text    string `json:"text"`
	IconURL string `json:"icon_url,omitempty"`
}

type Embed struct {
	Title       string        `json:"title,omitempty"`
	Description string        `json:"description,omitempty"`
	Color       int           `json:"color,omitempty"`
	Fields      []EmbedField  `json:"fields,omitempty"`
	Thumbnail   *EmbedMedia   `json:"thumbnail,omitempty"`
	Image       *EmbedMedia   `json:"image,omitempty"`
	Author      *EmbedAuthor  `json:"author,omitempty"`
	Footer      *EmbedFooter  `json:"footer,omitempty"`
	Timestamp   string        `json:"timestamp,omitempty"`
}

type WebhookPayload struct {
	Content  string  `json:"content,omitempty"`
	Embeds   []Embed `json:"embeds,omitempty"`
	Username string  `json:"username,omitempty"`
}

func NewDiscord(webhookURL string) *DiscordNotifier {
	return &DiscordNotifier{
		WebhookURL: webhookURL,
		HTTPClient: &http.Client{Timeout: 10 * time.Second},
	}
}

func (n *DiscordNotifier) Send(ctx context.Context, payload WebhookPayload) error {
	if n.WebhookURL == "" {
		return fmt.Errorf("discord webhook url is empty")
	}

	body, err := json.Marshal(payload)
	if err != nil {
		return err
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, n.WebhookURL, bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")

	client := n.HTTPClient
	if client == nil {
		client = &http.Client{Timeout: 10 * time.Second}
	}

	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("discord webhook returned status %d", resp.StatusCode)
	}

	return nil
}
