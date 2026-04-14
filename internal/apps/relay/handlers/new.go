package handlers

import (
	"context"
	"fmt"
	"sort"
	"strings"

	"github.com/normahq/norma/internal/apps/relay/auth"
	relaytelegram "github.com/normahq/norma/internal/apps/relay/channel/telegram"
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
	CreateSession(ctx context.Context, sessionCtx session.SessionContext, agentName string) error
	GetAgentInfo(agentName string) (string, []string)
	StopSession(locator session.SessionLocator)
	ValidateAgent(agentName string) error
}

// CommandHandler handles relay commands like /new and /close.
type CommandHandler struct {
	ownerStore     *auth.OwnerStore
	channel        *relaytelegram.Adapter
	sessionManager commandSessionManager
	turnDispatcher turnQueue
	messenger      *messenger.Messenger
	agentIDs       []string
}

func BuildAgentWelcomeMessage(agentName, sessionID, agentDesc string, mcpServers []string) string {
	return relaywelcome.BuildAgentWelcomeMessage(agentName, sessionID, agentDesc, mcpServers)
}

type commandHandlerParams struct {
	fx.In

	OwnerStore     *auth.OwnerStore
	Channel        *relaytelegram.Adapter
	SessionManager *session.Manager
	TurnDispatcher *TurnDispatcher
	Messenger      *messenger.Messenger
	NormaCfg       runtimeconfig.RuntimeConfig
}

// NewCommandHandler creates a new relay command handler.
func NewCommandHandler(params commandHandlerParams) *CommandHandler {
	return &CommandHandler{
		ownerStore:     params.OwnerStore,
		channel:        params.Channel,
		sessionManager: params.SessionManager,
		turnDispatcher: params.TurnDispatcher,
		messenger:      params.Messenger,
		agentIDs:       sortedAgentIDs(params.NormaCfg),
	}
}

// Register registers the handler with the registry.
func (h *CommandHandler) Register(registry handlers.RegistryInterface) {
	registry.OnCommand(h.onCommand)
}

func (h *CommandHandler) onCommand(ctx context.Context, event *events.CommandEvent) error {
	commandCtx, ok := h.channel.CommandContextFromEvent(event)
	if !ok {
		return nil
	}

	switch commandCtx.Command {
	case "new":
		return h.onNewCommand(ctx, commandCtx)
	case "close":
		return h.onCloseCommand(ctx, commandCtx)
	case "cancel":
		return h.onCancelCommand(ctx, commandCtx)
	default:
		return nil
	}
}

func (h *CommandHandler) onNewCommand(ctx context.Context, commandCtx relaytelegram.CommandContext) error {
	if !h.ownerStore.HasOwner() || !h.ownerStore.IsOwner(commandCtx.UserID) {
		if err := h.channel.SendPlain(ctx, commandCtx.Locator, "Only the bot owner can use this command."); err != nil {
			return err
		}
		return nil
	}

	if !commandCtx.IsDM {
		if err := h.channel.SendPlain(ctx, commandCtx.Locator, "This command is only available in direct messages."); err != nil {
			return err
		}
		return nil
	}

	agentName := strings.TrimSpace(commandCtx.Args)
	if agentName == "" {
		if err := h.channel.SendPlain(ctx, commandCtx.Locator, h.newCommandUsageMessage()); err != nil {
			return err
		}
		return nil
	}

	log.Info().
		Int64("user_id", commandCtx.UserID).
		Int64("chat_id", commandCtx.ChatID).
		Str("agent", agentName).
		Msg("Creating new topic with agent")

	if err := h.sessionManager.ValidateAgent(agentName); err != nil {
		log.Error().Err(err).Str("agent", agentName).Msg("agent validation failed, not creating topic")
		if sendErr := h.channel.SendPlain(ctx, commandCtx.Locator, fmt.Sprintf("Failed to create agent session: agent %q not available: %v", agentName, err)); sendErr != nil {
			return sendErr
		}
		return nil
	}

	topicLocator, err := h.channel.CreateTopicLocator(ctx, commandCtx.ChatID, fmt.Sprintf("Relay: %s", agentName))
	if err != nil {
		log.Error().Err(err).Str("agent", agentName).Msg("Failed to create topic with agent")
		if sendErr := h.channel.SendPlain(ctx, commandCtx.Locator, fmt.Sprintf("Failed to create agent session: %v", err)); sendErr != nil {
			return sendErr
		}
		return nil
	}
	if err := h.sessionManager.CreateSession(ctx, session.SessionContext{
		Locator: topicLocator,
		UserID:  session.TelegramUserID(commandCtx.UserID),
	}, agentName); err != nil {
		log.Error().Err(err).Str("agent", agentName).Msg("Failed to create agent session after topic creation")
		_ = h.channel.Close(ctx, topicLocator)
		if sendErr := h.channel.SendPlain(ctx, commandCtx.Locator, fmt.Sprintf("Failed to create agent session: %v", err)); sendErr != nil {
			return sendErr
		}
		return nil
	}

	agentDesc, mcpServers := h.sessionManager.GetAgentInfo(agentName)

	welcomeMsg := BuildAgentWelcomeMessage(agentName, topicLocator.SessionID, agentDesc, mcpServers)
	if err := h.channel.SendMarkdown(ctx, topicLocator, welcomeMsg); err != nil {
		log.Error().Err(err).Msg("Failed to send welcome message")
		return err
	}

	return nil
}

func (h *CommandHandler) onCloseCommand(ctx context.Context, commandCtx relaytelegram.CommandContext) error {
	if !h.ownerStore.HasOwner() || !h.ownerStore.IsOwner(commandCtx.UserID) {
		if err := h.channel.SendPlain(ctx, commandCtx.Locator, "Only the bot owner can use this command."); err != nil {
			return err
		}
		return nil
	}

	if !commandCtx.IsDM {
		if err := h.channel.SendPlain(ctx, commandCtx.Locator, "This command is only available in direct messages."); err != nil {
			return err
		}
		return nil
	}

	if strings.TrimSpace(commandCtx.Args) != "" {
		if err := h.channel.SendPlain(ctx, commandCtx.Locator, "Usage: /close"); err != nil {
			return err
		}
		return nil
	}

	if commandCtx.TopicID > 0 {
		if h.turnDispatcher != nil {
			_, _, _ = h.turnDispatcher.CancelSession(commandCtx.Locator, true)
		}
		if err := h.channel.SendPlain(ctx, commandCtx.Locator, "Closing this topic and stopping agent session."); err != nil {
			log.Warn().Err(err).Int64("chat_id", commandCtx.ChatID).Int("topic_id", commandCtx.TopicID).Msg("failed to send /close confirmation")
		}
		if err := h.channel.Close(ctx, commandCtx.Locator); err != nil {
			log.Warn().Err(err).Int64("chat_id", commandCtx.ChatID).Int("topic_id", commandCtx.TopicID).Msg("failed to close topic")
		}
		h.sessionManager.StopSession(commandCtx.Locator)
		return nil
	}

	if h.turnDispatcher != nil {
		_, _, _ = h.turnDispatcher.CancelSession(commandCtx.Locator, true)
	}
	if err := h.channel.SendPlain(ctx, commandCtx.Locator, "Stopping root agent session. It will be recreated on your next message."); err != nil {
		log.Warn().Err(err).Int64("chat_id", commandCtx.ChatID).Msg("failed to send /close root confirmation")
	}
	h.sessionManager.StopSession(commandCtx.Locator)
	return nil
}

func (h *CommandHandler) onCancelCommand(ctx context.Context, commandCtx relaytelegram.CommandContext) error {
	if !h.ownerStore.HasOwner() || !h.ownerStore.IsOwner(commandCtx.UserID) {
		if err := h.channel.SendPlain(ctx, commandCtx.Locator, "Only the bot owner can use this command."); err != nil {
			return err
		}
		return nil
	}

	if strings.TrimSpace(commandCtx.Args) != "" {
		if err := h.channel.SendPlain(ctx, commandCtx.Locator, "Usage: /cancel"); err != nil {
			return err
		}
		return nil
	}

	if h.turnDispatcher == nil {
		if err := h.channel.SendPlain(ctx, commandCtx.Locator, "Cancel is unavailable right now. Please try again."); err != nil {
			return err
		}
		return nil
	}

	hadInFlight, dropped, err := h.turnDispatcher.CancelSession(commandCtx.Locator, true)
	if err != nil {
		log.Warn().Err(err).Str("session_id", commandCtx.Locator.SessionID).Msg("failed to cancel session turns")
		if sendErr := h.channel.SendPlain(ctx, commandCtx.Locator, fmt.Sprintf("Failed to cancel current turn: %v", err)); sendErr != nil {
			return sendErr
		}
		return nil
	}

	if !hadInFlight && dropped == 0 {
		if err := h.channel.SendPlain(ctx, commandCtx.Locator, "No running or queued turns for this session."); err != nil {
			return err
		}
		return nil
	}

	response := "Canceled current turn."
	if !hadInFlight {
		response = "No running turn to cancel."
	}
	if dropped > 0 {
		response = fmt.Sprintf("%s Dropped %d queued message(s).", response, dropped)
	}
	if err := h.channel.SendPlain(ctx, commandCtx.Locator, response); err != nil {
		return err
	}
	return nil
}

func (h *CommandHandler) newCommandUsageMessage() string {
	usage := "Usage: /new <agent_id>"
	if len(h.agentIDs) == 0 {
		return usage + "\n\nNo providers configured under runtime.providers in relay config."
	}
	return usage + "\n\nAvailable agents: " + strings.Join(h.agentIDs, ", ")
}

func sortedAgentIDs(cfg runtimeconfig.RuntimeConfig) []string {
	agentIDs := make([]string, 0, len(cfg.Providers))
	for id := range cfg.Providers {
		trimmedID := strings.TrimSpace(id)
		if trimmedID == "" {
			continue
		}
		agentIDs = append(agentIDs, trimmedID)
	}
	sort.Strings(agentIDs)
	return agentIDs
}
