package main

import (
	"context"
	"fmt"
	"strings"

	"github.com/normahq/norma/internal/apps/relay/tgbotkit"
	"github.com/rs/zerolog/log"
	"github.com/tgbotkit/client"
)

type telegramMeClient interface {
	GetMeWithResponse(ctx context.Context, reqEditors ...client.RequestEditorFn) (*client.GetMeResponse, error)
}

type botIdentity struct {
	name     string
	username string
}

func logRelayStartup(ctx context.Context, botToken, ownerToken string) {
	identity, err := loadBotIdentityFromToken(ctx, botToken)
	if err != nil {
		log.Warn().Err(err).Msg("failed to load bot identity for startup log")
	}

	event := log.Info().
		Str("auth_url", buildAuthURL(identity.username, ownerToken))
	if identity.name != "" {
		event = event.Str("bot_name", identity.name)
	}
	if identity.username != "" {
		event = event.Str("bot_username", identity.username)
	}
	event.Msg("Relay bot started. Press Ctrl+C to stop.")
}

func loadBotIdentityFromToken(ctx context.Context, botToken string) (botIdentity, error) {
	tgClient, err := tgbotkit.NewClient(tgbotkit.Config{
		Token: strings.TrimSpace(botToken),
	})
	if err != nil {
		return botIdentity{}, fmt.Errorf("create Telegram client: %w", err)
	}
	return loadBotIdentity(ctx, tgClient)
}

func loadBotIdentity(ctx context.Context, tgClient telegramMeClient) (botIdentity, error) {
	resp, err := tgClient.GetMeWithResponse(ctx)
	if err != nil {
		return botIdentity{}, fmt.Errorf("get bot info: %w", err)
	}
	if resp == nil || resp.JSON200 == nil {
		return botIdentity{}, fmt.Errorf("getMe response missing result")
	}

	identity := botIdentity{
		name: strings.TrimSpace(resp.JSON200.Result.FirstName),
	}
	if resp.JSON200.Result.Username != nil {
		identity.username = strings.TrimSpace(*resp.JSON200.Result.Username)
	}
	if identity.username == "" {
		return botIdentity{}, fmt.Errorf("getMe response missing username")
	}

	return identity, nil
}

func buildAuthURL(botUsername, ownerToken string) string {
	username := strings.TrimSpace(botUsername)
	if username == "" {
		username = "<bot_username>"
	}
	return fmt.Sprintf("https://t.me/%s?start=%s", username, ownerToken)
}
