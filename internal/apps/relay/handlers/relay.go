package handlers

import (
	"context"
	"fmt"
	"strings"
	"sync"

	"github.com/normahq/norma/internal/apps/relay/auth"
	"github.com/normahq/norma/internal/apps/relay/messenger"
	relaysession "github.com/normahq/norma/internal/apps/relay/session"
	runtimeconfig "github.com/normahq/norma/pkg/runtime/appconfig"
	"github.com/rs/zerolog"
	"github.com/rs/zerolog/log"
	"github.com/tgbotkit/client"
	"github.com/tgbotkit/runtime/events"
	"github.com/tgbotkit/runtime/handlers"
	"github.com/tgbotkit/runtime/messagetype"
	"go.uber.org/fx"
	"google.golang.org/adk/agent"
	"google.golang.org/adk/runner"
	"google.golang.org/genai"
)

// RelayHandler handles bidirectional message relay between owner and agent.
type RelayHandler struct {
	ownerStore     *auth.OwnerStore
	sessionManager *relaysession.Manager
	messenger      *messenger.Messenger
	tgClient       client.ClientWithResponsesInterface
	authToken      string
	rootAgentName  string
	normaCfg       runtimeconfig.NormaConfig
	logger         zerolog.Logger

	mu          sync.RWMutex
	ownerID     int64
	chatID      int64
	botUsername string
}

type relayHandlerDeps struct {
	fx.In

	LC                 fx.Lifecycle
	OwnerStore         *auth.OwnerStore
	SessionManager     *relaysession.Manager
	Messenger          *messenger.Messenger
	TGClient           client.ClientWithResponsesInterface
	AuthToken          string `name:"relay_auth_token"`
	RootAgentName      string `name:"relay_root_agent"`
	NormaCfg           runtimeconfig.NormaConfig
	Logger             zerolog.Logger
	InternalMCPManager *InternalMCPManager `optional:"true"`
}

func NewRelayHandler(deps relayHandlerDeps) (*RelayHandler, error) {
	h := &RelayHandler{
		ownerStore:     deps.OwnerStore,
		sessionManager: deps.SessionManager,
		messenger:      deps.Messenger,
		tgClient:       deps.TGClient,
		authToken:      strings.TrimSpace(deps.AuthToken),
		rootAgentName:  strings.TrimSpace(deps.RootAgentName),
		normaCfg:       deps.NormaCfg,
		logger:         deps.Logger.With().Str("component", "relay.handler").Logger(),
	}

	deps.LC.Append(fx.Hook{
		OnStart: func(ctx context.Context) error {
			return h.onStart(ctx)
		},
	})

	return h, nil
}

// Register registers the handler with the registry.
func (h *RelayHandler) Register(registry handlers.RegistryInterface) {
	registry.OnMessage(h.onMessage)
	registry.OnMessageType(messagetype.ForumTopicCreated, h.onForumTopicLifecycle)
	registry.OnMessageType(messagetype.ForumTopicEdited, h.onForumTopicLifecycle)
	registry.OnMessageType(messagetype.ForumTopicClosed, h.onForumTopicLifecycle)
	registry.OnMessageType(messagetype.ForumTopicReopened, h.onForumTopicLifecycle)
}

// SetOwner binds the handler to the owner. Pass chatID=0 when the chat
// is not yet known (it will be set from the first incoming message).
func (h *RelayHandler) SetOwner(ownerID, chatID int64) {
	h.mu.Lock()
	defer h.mu.Unlock()

	log.Info().Int64("owner_id", ownerID).Int64("chat_id", chatID).Msg("Setting owner for relay")

	h.ownerID = ownerID
	if chatID != 0 {
		h.chatID = chatID
	}
}

// SendToOwner sends a message from the agent to the owner.
func (h *RelayHandler) SendToOwner(ctx context.Context, msg string) error {
	chatID := h.getChatID()
	if chatID == 0 {
		return fmt.Errorf("owner not set")
	}

	if err := h.messenger.SendPlain(ctx, chatID, msg, 0); err != nil {
		return fmt.Errorf("sending message: %w", err)
	}
	return nil
}

// ActivateOwner binds owner/chat and ensures root session is running.
// If root session already exists, this is a no-op.
func (h *RelayHandler) ActivateOwner(ctx context.Context, ownerID, chatID int64) error {
	h.SetOwner(ownerID, chatID)

	if _, err := h.sessionManager.GetTelegramSession(chatID, 0); err == nil {
		return nil
	}

	if err := h.ensureRootSession(ctx, chatID); err != nil {
		return err
	}
	return nil
}

func (h *RelayHandler) onMessage(ctx context.Context, event *events.MessageEvent) error {
	if event == nil || event.Message == nil || event.Message.From == nil {
		return nil
	}

	ownerID := h.getOwnerID()
	chatID := h.getChatID()

	if ownerID == 0 {
		return nil
	}

	if event.Message.From.Id != ownerID {
		return nil
	}

	if chatID == 0 {
		h.setChatID(event.Message.Chat.Id)
		log.Info().Int64("chat_id", event.Message.Chat.Id).Msg("Chat ID set from message")
		chatID = event.Message.Chat.Id
	}

	if event.Message.Text == nil {
		return nil
	}
	if hasCommandEntity(event.Message) {
		return nil
	}

	text := *event.Message.Text
	if text == "" {
		return nil
	}

	var topicID int
	if event.Message.MessageThreadId != nil {
		topicID = *event.Message.MessageThreadId
	}

	log.Info().Int64("user_id", ownerID).Int("topic_id", topicID).Msg("Relaying message to agent")

	var ts *relaysession.TopicSession
	var err error

	if topicID == 0 {
		// Main root session: always create/ensure from current relay config.
		if h.rootAgentName == "" {
			_ = h.messenger.SendPlain(ctx, chatID, "Relay root agent is not configured (`relay.root_agent`). Please close this chat and restart relay.", topicID)
			return nil
		}

		// Check if session already exists to avoid sending spinning message again
		existingSession, _ := h.sessionManager.GetTelegramSession(chatID, topicID)
		if existingSession == nil {
			// Send spinning message for on-demand creation
			agentDesc, mcpServers := h.sessionManager.GetAgentInfo(h.rootAgentName)
			spinningMsg := BuildAgentWelcomeMessage(h.rootAgentName, "", agentDesc, mcpServers)
			_ = h.messenger.SendMarkdown(ctx, chatID, spinningMsg, topicID)
		}
		ts, err = h.sessionManager.EnsureTelegramSession(ctx, chatID, topicID, h.rootAgentName)
		if err != nil {
			log.Error().Err(err).Str("agent", h.rootAgentName).Msg("failed to ensure root session")
			_ = h.messenger.SendPlain(
				ctx,
				chatID,
				fmt.Sprintf("Failed to start root session: %v.\n\nPlease close this chat and start again.", err),
				topicID,
			)
			return nil
		}
	} else {
		// Subagent topic: try in-memory first, then lazy restore from persisted metadata.
		ts, err = h.sessionManager.GetTelegramSession(chatID, topicID)
		if err != nil {
			_ = h.messenger.SendPlain(ctx, chatID, "Restoring agent session...", topicID)
			ts, err = h.sessionManager.RestoreTelegramSession(ctx, chatID, topicID)
			if err != nil {
				log.Warn().Err(err).Int("topic_id", topicID).Msg("failed to restore topic session")
				_ = h.messenger.SendPlain(
					ctx,
					chatID,
					fmt.Sprintf("Failed to restore this session: %v.\n\nPlease close this chat topic and create a new session with /new <agent_name>.", err),
					topicID,
				)
				return nil
			}
			// Send welcome message with agent info after restore
			agentDesc, mcpServers := h.sessionManager.GetAgentInfo(ts.GetAgentName())
			sessionID := ts.GetSessionID()
			welcomeMsg := BuildAgentWelcomeMessage(ts.GetAgentName(), sessionID, agentDesc, mcpServers)
			_ = h.messenger.SendMarkdown(ctx, chatID, welcomeMsg, topicID)
		}
	}

	// Run turn
	if err := h.runTurn(ctx, text, ts.GetRunner(), ts.GetSessionID(), chatID, topicID, event.Message.MessageId); err != nil {
		log.Error().Err(err).Int("topic_id", topicID).Msg("Agent execution failed")
		errText := fmt.Sprintf("Agent execution failed: %v.\n\nPlease close this chat and start a new session.", err)
		if topicID > 0 {
			errText = fmt.Sprintf("Agent execution failed: %v.\n\nPlease close this chat topic and create a new session with /new <agent_name>.", err)
		}
		if sendErr := h.messenger.SendPlain(ctx, chatID, errText, topicID); sendErr != nil {
			log.Warn().Err(sendErr).Int("topic_id", topicID).Msg("failed to send relay error message")
		}
	}

	return nil
}

func (h *RelayHandler) onForumTopicLifecycle(_ context.Context, event *events.MessageEvent) error {
	if event == nil || event.Message == nil {
		return nil
	}

	chatID := event.Message.Chat.Id
	boundChatID := h.getChatID()
	if boundChatID != 0 && chatID != boundChatID {
		return nil
	}

	topicID := 0
	if event.Message.MessageThreadId != nil {
		topicID = *event.Message.MessageThreadId
	}
	if topicID <= 0 {
		h.logger.Debug().
			Int64("chat_id", chatID).
			Str("event_type", string(event.Type)).
			Msg("ignoring forum topic lifecycle event without topic id")
		return nil
	}

	evt := h.logger.Info().
		Int64("chat_id", chatID).
		Int("topic_id", topicID).
		Int("message_id", event.Message.MessageId).
		Str("event_type", string(event.Type))
	if event.Message.From != nil {
		evt = evt.Int64("user_id", event.Message.From.Id)
	}

	switch event.Type {
	case messagetype.ForumTopicCreated:
		evt.Msg("forum topic created")
	case messagetype.ForumTopicEdited:
		evt.Msg("forum topic edited")
	case messagetype.ForumTopicClosed:
		evt.Msg("forum topic closed")
	case messagetype.ForumTopicReopened:
		evt.Msg("forum topic reopened")
	default:
		evt.Msg("forum topic lifecycle event")
	}

	return nil
}

func (h *RelayHandler) runTurn(ctx context.Context, text string, r *runner.Runner, sessionID string, chatID int64, topicID, messageID int) error {
	userContent := genai.NewContentFromText(text, genai.RoleUser)
	userID := fmt.Sprintf("relay-%d-%d", chatID, topicID)
	draftID := messageID + 1

	runCtx := zerolog.Ctx(ctx).With().
		Int64("chat_id", chatID).
		Int("topic_id", topicID).
		Str("session_id", sessionID).
		Str("relay_user_id", userID).
		Logger().
		WithContext(ctx)

	var result strings.Builder
	thinkingStages := []string{"Thinking.", "Thinking..", "Thinking..."}
	thinkingIdx := 0

	for ev, err := range r.Run(runCtx, userID, sessionID, userContent, agent.RunConfig{}) {
		if err != nil {
			return fmt.Errorf("agent run: %w", err)
		}
		if ev == nil {
			continue
		}
		if ev.Content != nil {
			for _, part := range ev.Content.Parts {
				if part == nil {
					continue
				}
				if part.Thought {
					if sendErr := h.messenger.SendDraftPlain(ctx, chatID, draftID, thinkingStages[thinkingIdx%len(thinkingStages)], topicID); sendErr != nil {
						log.Warn().Err(sendErr).Int("topic_id", topicID).Msg("failed to send thinking draft")
					}
					thinkingIdx++
					continue
				}
				if part.Text != "" {
					result.WriteString(part.Text)
				}
			}
		}
		if ev.TurnComplete {
			break
		}
	}

	if s := result.String(); strings.TrimSpace(s) != "" {
		if sendErr := h.messenger.SendMarkdown(ctx, chatID, s, topicID); sendErr != nil {
			log.Warn().Err(sendErr).Int("topic_id", topicID).Msg("failed to send relay response")
		}
	}

	return nil
}

func (h *RelayHandler) getOwnerID() int64 {
	h.mu.RLock()
	defer h.mu.RUnlock()
	return h.ownerID
}

func (h *RelayHandler) getChatID() int64 {
	h.mu.RLock()
	defer h.mu.RUnlock()
	return h.chatID
}

func (h *RelayHandler) setChatID(chatID int64) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.chatID = chatID
}

func hasCommandEntity(msg *client.Message) bool {
	if msg == nil || msg.Entities == nil {
		return false
	}
	for _, entity := range *msg.Entities {
		if entity.Type == "bot_command" {
			return true
		}
	}
	return false
}

func (h *RelayHandler) onStart(ctx context.Context) error {
	h.initializeBotUsername(ctx)

	if !h.ownerStore.HasOwner() {
		return nil
	}
	owner := h.ownerStore.GetOwner()
	if owner == nil {
		return nil
	}
	if owner.ChatID == 0 {
		return fmt.Errorf("owner.chat_id is required for relay startup; run /start to bind owner chat")
	}

	h.SetOwner(owner.UserID, owner.ChatID)

	// Precreate root session synchronously.
	if err := h.ensureRootSession(ctx, owner.ChatID); err != nil {
		return err
	}

	if err := h.messenger.SendPlain(ctx, owner.UserID, "Boss, I'm online and ready to work.", 0); err != nil {
		h.logger.Warn().Err(err).Int64("owner_id", owner.UserID).Msg("failed to send startup ready message to owner")
		return nil
	}
	h.logger.Info().Int64("owner_id", owner.UserID).Msg("startup ready message sent to owner")
	return nil
}

func (h *RelayHandler) initializeBotUsername(ctx context.Context) {
	if h.tgClient == nil {
		return
	}

	meResp, err := h.tgClient.GetMeWithResponse(ctx)
	if err != nil {
		h.logger.Warn().Err(err).Msg("getMe failed; bot username unavailable")
		return
	}
	if meResp.JSON200 == nil || meResp.JSON200.Result.Username == nil {
		h.logger.Warn().Str("status", meResp.Status()).Msg("getMe response missing username")
		return
	}

	username := strings.TrimSpace(*meResp.JSON200.Result.Username)
	if username == "" {
		h.logger.Warn().Msg("getMe returned empty username")
		return
	}

	h.mu.Lock()
	h.botUsername = username
	h.mu.Unlock()

	if h.authToken != "" {
		deeplink := fmt.Sprintf("https://t.me/%s?start=%s", username, h.authToken)
		h.logger.Info().Str("bot_username", username).Str("start_deeplink", deeplink).Msg("relay start deeplink ready")
		return
	}
	h.logger.Info().Str("bot_username", username).Msg("relay bot username loaded")
}

func (h *RelayHandler) ensureRootSession(ctx context.Context, chatID int64) error {
	agentName := h.rootAgentName
	if agentName == "" {
		return fmt.Errorf("relay.root_agent is required")
	}

	agentDesc, mcpServers := h.sessionManager.GetAgentInfo(agentName)
	spinningMsg := BuildAgentWelcomeMessage(agentName, "", agentDesc, mcpServers)
	if err := h.messenger.SendMarkdown(ctx, chatID, spinningMsg, 0); err != nil {
		h.logger.Warn().Err(err).Msg("failed to send spinning up message")
	}

	ts, err := h.sessionManager.EnsureTelegramSession(ctx, chatID, 0, agentName)
	if err != nil {
		return fmt.Errorf("precreate root session: %w", err)
	}

	h.logger.Info().
		Int64("chat_id", chatID).
		Str("agent", agentName).
		Str("session_id", ts.GetSessionID()).
		Msg("root session precreated")
	return nil
}
