package handlers

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/normahq/norma/internal/apps/relay/auth"
	"github.com/normahq/norma/internal/apps/relay/messenger"
	"github.com/rs/zerolog/log"
	"github.com/tgbotkit/client"
	"github.com/tgbotkit/runtime/events"
	"github.com/tgbotkit/runtime/handlers"
	"go.uber.org/fx"
)

// StartHandler handles /start command for owner authentication and invite consumption.
type StartHandler struct {
	ownerStore        *auth.OwnerStore
	inviteStore       *auth.InviteStore
	collaboratorStore *auth.CollaboratorStore
	messenger         *messenger.Messenger
	authToken         string
	relayHandler      relayOwnerActivator
}

type relayOwnerActivator interface {
	ActivateOwner(ctx context.Context, ownerID, chatID int64) error
}

// StartHandlerParams provides dependencies for StartHandler.
type StartHandlerParams struct {
	fx.In

	OwnerStore        *auth.OwnerStore
	InviteStore       *auth.InviteStore
	CollaboratorStore *auth.CollaboratorStore
	Messenger         *messenger.Messenger
	AuthToken         string `name:"relay_auth_token"`
}

// NewStartHandler creates a new start handler.
func NewStartHandler(params StartHandlerParams) *StartHandler {
	return &StartHandler{
		ownerStore:        params.OwnerStore,
		inviteStore:       params.InviteStore,
		collaboratorStore: params.CollaboratorStore,
		messenger:         params.Messenger,
		authToken:         params.AuthToken,
	}
}

// SetRelayHandler sets the relay handler (needed for circular dependency).
func (h *StartHandler) SetRelayHandler(rh relayOwnerActivator) {
	h.relayHandler = rh
}

// Register registers the handler with the registry.
func (h *StartHandler) Register(registry handlers.RegistryInterface) {
	registry.OnCommand(h.onCommand)
}

func (h *StartHandler) onCommand(ctx context.Context, event *events.CommandEvent) error {
	if event.Command != "start" {
		return nil
	}

	if event.Message.Chat.Type != "private" {
		return nil
	}

	chatID := event.Message.Chat.Id
	userIDStr := fmt.Sprintf("%d", event.Message.From.Id)
	userID := event.Message.From.Id

	log.Debug().
		Int64("user_id", userID).
		Int64("chat_id", chatID).
		Msg("Start command received")

	// Check if this is an invite token (not the owner token)
	token := strings.TrimSpace(event.Args)
	if token != "" && token != h.authToken && !strings.HasPrefix(token, "?") && !strings.Contains(token, "=") {
		// Try to consume invite
		invite, err := h.inviteStore.GetInvite(ctx, token)
		if err != nil {
			log.Warn().Err(err).Str("token", token).Msg("failed to get invite")
			if err := h.messenger.SendPlain(ctx, chatID, "Failed to process invite. Please try again.", 0); err != nil {
				return err
			}
			return nil
		}

		if invite == nil {
			if err := h.messenger.SendPlain(ctx, chatID, "This invite link is invalid or has expired.", 0); err != nil {
				return err
			}
			return nil
		}

		// Invite is valid - add user as collaborator
		info := extractUserInfo(event.Message.From)
		collaborator := auth.Collaborator{
			UserID:    userIDStr,
			Username:  info.username,
			FirstName: info.firstName,
			AddedBy:   invite.CreatedBy,
			AddedAt:   time.Now(),
		}
		if err := h.collaboratorStore.AddCollaborator(ctx, collaborator); err != nil {
			log.Error().Err(err).Msg("failed to add collaborator from invite")
			if err := h.messenger.SendPlain(ctx, chatID, "Failed to complete registration. Please try again.", 0); err != nil {
				return err
			}
			return nil
		}

		log.Info().Str("user_id", userIDStr).Str("invited_by", invite.CreatedBy).Msg("User registered as collaborator via invite")

		if err := h.messenger.SendPlain(ctx, chatID, "Welcome! You are now a bot collaborator.", 0); err != nil {
			return err
		}
		return nil
	}

	// Continue with normal owner authentication flow
	authToken, malformed := parseStartAuthArg(event.Args)

	if h.ownerStore.HasOwner() {
		if h.ownerStore.IsOwner(userID) {
			// Persist chatID for existing owner
			if err := h.ownerStore.UpdateChatID(chatID); err != nil {
				log.Warn().Err(err).Msg("failed to update owner chatID")
			}
			startErr := h.activateRelay(ctx, userID, chatID)
			if startErr == nil {
				log.Info().Int64("user_id", userID).Msg("relay re-activated for existing owner")
			}
			if err := h.messenger.SendPlain(ctx, chatID, h.ownerAlreadyRegisteredMessage(startErr), 0); err != nil {
				return err
			}
			return nil
		}
		if err := h.messenger.SendPlain(ctx, chatID, "Bot owner is already registered. Only the owner can use this bot.", 0); err != nil {
			return err
		}
		return nil
	}

	if malformed {
		log.Warn().
			Int64("user_id", userID).
			Int64("chat_id", chatID).
			Msg("Malformed /start auth argument")
		if err := h.messenger.SendPlain(ctx, chatID, malformedStartFormatMessage(), 0); err != nil {
			return err
		}
		return nil
	}

	if authToken == "" {
		if err := h.sendWelcomeMessage(ctx, chatID); err != nil {
			return err
		}
		return nil
	}

	if authToken != h.authToken {
		log.Warn().
			Int64("user_id", userID).
			Int64("chat_id", chatID).
			Msg("Invalid auth token provided")
		if err := h.messenger.SendPlain(ctx, chatID, "Invalid authentication token. Please try again.", 0); err != nil {
			return err
		}
		return nil
	}

	info := extractUserInfo(event.Message.From)

	var hasTopicsEnabled bool
	if event.Message.Chat.IsForum != nil {
		hasTopicsEnabled = *event.Message.Chat.IsForum
	}

	registered, err := h.ownerStore.RegisterOwner(userID, chatID, info.username, info.firstName, info.lastName, hasTopicsEnabled)
	if err != nil {
		log.Error().Err(err).Int64("user_id", userID).Msg("Failed to register owner")
		if sendErr := h.messenger.SendPlain(ctx, chatID, "Failed to register owner. Please try again.", 0); sendErr != nil {
			return sendErr
		}
		return nil
	}

	if !registered {
		if err := h.messenger.SendPlain(ctx, chatID, "Owner is already registered.", 0); err != nil {
			return err
		}
		return nil
	}

	log.Info().
		Int64("user_id", userID).
		Str("username", info.username).
		Msg("Owner registered successfully")

	startErr := h.activateRelay(ctx, userID, chatID)
	if err := h.sendOwnerRegisteredMessage(ctx, chatID, info.firstName, startErr); err != nil {
		return err
	}
	return nil
}

func parseStartAuthArg(raw string) (string, bool) {
	authToken := strings.TrimSpace(raw)
	if authToken == "" {
		return "", false
	}
	if strings.HasPrefix(authToken, "?") || strings.Contains(authToken, "=") {
		return "", true
	}
	return authToken, false
}

func malformedStartFormatMessage() string {
	return "Invalid /start format. Use /start <your_owner_token>.\n\nIf using a link, use https://t.me/<bot_username>?start=<your_owner_token>"
}

type userInfo struct {
	username  string
	firstName string
	lastName  string
}

func extractUserInfo(from *client.User) userInfo {
	info := userInfo{
		firstName: from.FirstName,
	}
	if from.Username != nil {
		info.username = *from.Username
	}
	if from.LastName != nil {
		info.lastName = *from.LastName
	}
	return info
}

func (h *StartHandler) sendWelcomeMessage(ctx context.Context, chatID int64) error {
	return h.messenger.SendPlain(ctx, chatID, "Welcome to Norma Relay Bot!\n\nTo authenticate, send /start <your_owner_token>", 0)
}

func (h *StartHandler) sendOwnerRegisteredMessage(ctx context.Context, chatID int64, firstName string, startErr error) error {
	name := firstName
	if name == "" {
		name = "Owner"
	}

	text := fmt.Sprintf("Congratulations, %s! You are now registered as the bot owner.", name)
	if startErr != nil {
		text += "\n\n" + relayStartFailureMessage(startErr)
		return h.messenger.SendPlain(ctx, chatID, text, 0)
	}
	text += "\n\nRelay mode is active."
	return h.messenger.SendPlain(ctx, chatID, text, 0)
}

func (h *StartHandler) ownerAlreadyRegisteredMessage(startErr error) string {
	msg := "You are already registered as the bot owner."
	if startErr != nil {
		msg += "\n\n" + relayStartFailureMessage(startErr)
		return msg
	}
	msg += " Relay mode is active."
	return msg
}

func (h *StartHandler) activateRelay(ctx context.Context, ownerID, chatID int64) error {
	if h.relayHandler == nil {
		log.Warn().Msg("relay handler is nil; skipping root session activation")
		return nil
	}
	if err := h.relayHandler.ActivateOwner(ctx, ownerID, chatID); err != nil {
		log.Warn().
			Err(err).
			Int64("owner_id", ownerID).
			Int64("chat_id", chatID).
			Msg("failed to start root session during /start")
		return err
	}
	return nil
}

func relayStartFailureMessage(err error) string {
	return fmt.Sprintf(
		"Failed to start root agent session: %v.\nPlease verify relay root-agent configuration, then send /start again or restart relay.",
		err,
	)
}
