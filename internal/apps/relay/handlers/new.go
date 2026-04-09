package handlers

import (
	"context"
	"fmt"
	"sort"
	"strings"

	"github.com/normahq/norma/internal/apps/relay/auth"
	"github.com/normahq/norma/internal/apps/relay/messenger"
	"github.com/normahq/norma/internal/apps/relay/session"
	relaywelcome "github.com/normahq/norma/internal/apps/relay/welcome"
	runtimeconfig "github.com/normahq/norma/pkg/runtime/appconfig"
	"github.com/rs/zerolog/log"
	"github.com/tgbotkit/runtime/events"
	"github.com/tgbotkit/runtime/handlers"
	"go.uber.org/fx"
)

type commandSessionManager interface {
	CreateTopicSession(ctx context.Context, chatID int64, agentName string) (string, int, error)
	GetAgentInfo(agentName string) (string, []string)
	StopTelegramSession(chatID int64, topicID int)
	CloseTopic(ctx context.Context, chatID int64, topicID int)
}

// CommandHandler handles relay commands like /new and /close.
type CommandHandler struct {
	ownerStore     *auth.OwnerStore
	sessionManager commandSessionManager
	messenger      *messenger.Messenger
	agentIDs       []string
}

func BuildAgentWelcomeMessage(agentName, sessionID, agentDesc string, mcpServers []string) string {
	return relaywelcome.BuildAgentWelcomeMessage(agentName, sessionID, agentDesc, mcpServers)
}

type commandHandlerParams struct {
	fx.In

	OwnerStore     *auth.OwnerStore
	SessionManager *session.Manager
	Messenger      *messenger.Messenger
	NormaCfg       runtimeconfig.NormaConfig
}

// NewCommandHandler creates a new relay command handler.
func NewCommandHandler(params commandHandlerParams) *CommandHandler {
	return &CommandHandler{
		ownerStore:     params.OwnerStore,
		sessionManager: params.SessionManager,
		messenger:      params.Messenger,
		agentIDs:       sortedAgentIDs(params.NormaCfg),
	}
}

// Register registers the handler with the registry.
func (h *CommandHandler) Register(registry handlers.RegistryInterface) {
	registry.OnCommand(h.onCommand)
}

func (h *CommandHandler) onCommand(ctx context.Context, event *events.CommandEvent) error {
	switch event.Command {
	case "new":
		return h.onNewCommand(ctx, event)
	case "close":
		return h.onCloseCommand(ctx, event)
	default:
		return nil
	}
}

func (h *CommandHandler) onNewCommand(ctx context.Context, event *events.CommandEvent) error {
	chatID := event.Message.Chat.Id
	userID := event.Message.From.Id

	if !h.ownerStore.HasOwner() || !h.ownerStore.IsOwner(userID) {
		if err := h.messenger.SendPlain(ctx, chatID, "Only the bot owner can use this command.", 0); err != nil {
			return err
		}
		return nil
	}

	agentName := strings.TrimSpace(event.Args)
	if agentName == "" {
		if err := h.messenger.SendPlain(ctx, chatID, h.newCommandUsageMessage(), 0); err != nil {
			return err
		}
		return nil
	}

	log.Info().
		Int64("user_id", userID).
		Int64("chat_id", chatID).
		Str("agent", agentName).
		Msg("Creating new topic with agent")

	sessionID, topicID, err := h.sessionManager.CreateTopicSession(ctx, chatID, agentName)
	if err != nil {
		log.Error().Err(err).Str("agent", agentName).Msg("Failed to create topic with agent")
		if sendErr := h.messenger.SendPlain(ctx, chatID, fmt.Sprintf("Failed to create agent session: %v", err), 0); sendErr != nil {
			return sendErr
		}
		return nil
	}

	agentDesc, mcpServers := h.sessionManager.GetAgentInfo(agentName)

	welcomeMsg := BuildAgentWelcomeMessage(agentName, sessionID, agentDesc, mcpServers)
	if err := h.messenger.SendMarkdown(ctx, chatID, welcomeMsg, topicID); err != nil {
		log.Error().Err(err).Msg("Failed to send welcome message")
		return err
	}

	return nil
}

func (h *CommandHandler) onCloseCommand(ctx context.Context, event *events.CommandEvent) error {
	chatID := event.Message.Chat.Id
	userID := event.Message.From.Id

	if !h.ownerStore.HasOwner() || !h.ownerStore.IsOwner(userID) {
		if err := h.messenger.SendPlain(ctx, chatID, "Only the bot owner can use this command.", 0); err != nil {
			return err
		}
		return nil
	}

	topicID := 0
	if event.Message.MessageThreadId != nil {
		topicID = *event.Message.MessageThreadId
	}

	if strings.TrimSpace(event.Args) != "" {
		if err := h.messenger.SendPlain(ctx, chatID, "Usage: /close", topicID); err != nil {
			return err
		}
		return nil
	}

	if topicID > 0 {
		if err := h.messenger.SendPlain(ctx, chatID, "Closing this topic and stopping agent session.", topicID); err != nil {
			log.Warn().Err(err).Int64("chat_id", chatID).Int("topic_id", topicID).Msg("failed to send /close confirmation")
		}
		h.sessionManager.CloseTopic(ctx, chatID, topicID)
		h.sessionManager.StopTelegramSession(chatID, topicID)
		return nil
	}

	if err := h.messenger.SendPlain(ctx, chatID, "Stopping root agent session. It will be recreated on your next message.", topicID); err != nil {
		log.Warn().Err(err).Int64("chat_id", chatID).Msg("failed to send /close root confirmation")
	}
	h.sessionManager.StopTelegramSession(chatID, topicID)
	return nil
}

func (h *CommandHandler) newCommandUsageMessage() string {
	usage := "Usage: /new <agent_id>"
	if len(h.agentIDs) == 0 {
		return usage + "\n\nNo agents configured under norma.agents in relay config."
	}
	return usage + "\n\nAvailable agents: " + strings.Join(h.agentIDs, ", ")
}

func sortedAgentIDs(cfg runtimeconfig.NormaConfig) []string {
	agentIDs := make([]string, 0, len(cfg.Agents))
	for id := range cfg.Agents {
		trimmedID := strings.TrimSpace(id)
		if trimmedID == "" {
			continue
		}
		agentIDs = append(agentIDs, trimmedID)
	}
	sort.Strings(agentIDs)
	return agentIDs
}
