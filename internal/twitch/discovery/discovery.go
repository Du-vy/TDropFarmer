package discovery

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"regexp"
	"strings"

	"github.com/Du-vy/TDropFarmer/internal/domain"
	"github.com/Du-vy/TDropFarmer/internal/twitch/gql"
)

const (
	gameDirectoryOperation = "DirectoryPage_Game"
	gameDirectoryHash      = "cb5dc816e139dcb8a118f14b4b677d59abc224a4b016c4bc2bb00a47fe0ddec4"

	gameRedirectOperation = "DirectoryGameRedirect"
	gameRedirectHash      = "1f0300090caceec51f33c5e20647aceff9017f740f223c3c532ba6fa59f6b6cc"
)

type GQLClient interface {
	Do(context.Context, gql.Request) (gql.Response, error)
}

type Client struct {
	Client GQLClient
	Logger *slog.Logger
}

var (
	nonAlphaNum = regexp.MustCompile(`[^\w]+`)
	dashes      = regexp.MustCompile(`-+`)
)

// Slugify converts the game name into a slug, usable for the GQL API.
func Slugify(name string) string {
	s := strings.ToLower(name)
	s = strings.ReplaceAll(s, "'", "")
	s = nonAlphaNum.ReplaceAllString(s, "-")
	s = dashes.ReplaceAllString(s, "-")
	return strings.Trim(s, "-")
}

func (c Client) GetLiveStreams(ctx context.Context, gameName string, limit int) ([]domain.Streamer, error) {
	if c.Client == nil {
		return nil, fmt.Errorf("graphql client is required")
	}

	slug := Slugify(gameName)
	if slug == "" {
		return nil, fmt.Errorf("invalid game name %q resulting in empty slug", gameName)
	}
	if resolved, err := c.resolveGameSlug(ctx, gameName); err == nil && resolved != "" {
		slug = resolved
	}

	response, err := c.Client.Do(ctx, gql.Request{
		OperationName: gameDirectoryOperation,
		Variables: map[string]any{
			"limit":              limit,
			"slug":               slug,
			"imageWidth":         50,
			"includeCostreaming": false,
			"sortTypeIsRecency":  false,
			"options": map[string]any{
				"broadcasterLanguages": []string{},
				"freeformTags":         nil,
				"includeRestricted":    []string{"SUB_ONLY_LIVE"},
				"recommendationsContext": map[string]any{
					"platform": "web",
				},
				"sort":          "RELEVANCE",
				"systemFilters": []string{"DROPS_ENABLED"},
				"tags":          []string{},
				"requestID":     "JIRA-VXP-2397",
			},
		},
		Extensions: map[string]any{
			"persistedQuery": map[string]any{
				"version":    1,
				"sha256Hash": gameDirectoryHash,
			},
		},
	})
	if err != nil {
		return nil, fmt.Errorf("game directory request: %w", err)
	}

	var data gameDirectoryResponse
	if err := json.Unmarshal(response.Data, &data); err != nil {
		return nil, fmt.Errorf("decode game directory response: %w", err)
	}

	if data.Game == nil {
		if c.Logger != nil {
			c.Logger.Info("game discovery found no directory entry",
				slog.String("game", gameName),
				slog.String("slug", slug),
			)
		}
		return nil, nil
	}

	var streamers []domain.Streamer
	for _, edge := range data.Game.Streams.Edges {
		if edge.Node.Broadcaster == nil {
			continue
		}
		var gameID, gameName string
		if edge.Node.Game != nil {
			gameID = edge.Node.Game.ID
			gameName = edge.Node.Game.Name
		}
		streamers = append(streamers, domain.Streamer{
			ID:          edge.Node.Broadcaster.ID,
			Login:       edge.Node.Broadcaster.Login,
			DisplayName: edge.Node.Broadcaster.DisplayName,
			GameID:      gameID,
			GameName:    gameName,
			Title:       edge.Node.Title,
			BroadcastID: edge.Node.ID,
		})
	}

	if len(streamers) == 0 && c.Logger != nil {
		c.Logger.Info("game discovery found no drop-enabled streams",
			slog.String("game", gameName),
			slog.String("slug", slug),
		)
	}

	return streamers, nil
}

func (c Client) resolveGameSlug(ctx context.Context, gameName string) (string, error) {
	response, err := c.Client.Do(ctx, gql.Request{
		OperationName: gameRedirectOperation,
		Variables: map[string]any{
			"name": gameName,
		},
		Extensions: map[string]any{
			"persistedQuery": map[string]any{
				"version":    1,
				"sha256Hash": gameRedirectHash,
			},
		},
	})
	if err != nil {
		return "", err
	}

	var data gameRedirectResponse
	if err := json.Unmarshal(response.Data, &data); err != nil {
		return "", fmt.Errorf("decode game redirect response: %w", err)
	}
	if data.Game == nil {
		return "", nil
	}
	return data.Game.Slug, nil
}

type gameDirectoryResponse struct {
	Game *struct {
		Streams struct {
			Edges []struct {
				Node struct {
					ID          string `json:"id"`
					Title       string `json:"title"`
					Broadcaster *struct {
						ID          string `json:"id"`
						Login       string `json:"login"`
						DisplayName string `json:"displayName"`
					} `json:"broadcaster"`
					Game *struct {
						ID   string `json:"id"`
						Name string `json:"name"`
					} `json:"game"`
				} `json:"node"`
			} `json:"edges"`
		} `json:"streams"`
	} `json:"game"`
}

type gameRedirectResponse struct {
	Game *struct {
		Slug string `json:"slug"`
	} `json:"game"`
}
