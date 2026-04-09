package handlers

import (
	"context"
	"fmt"
	"strings"
	"sync"

	"github.com/normahq/norma/internal/apps/relay/auth"
	relaytelegram "github.com/normahq/norma/internal/apps/relay/channel/telegram"
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
	channel        *relaytelegram.Adapter
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
	Channel            *relaytelegram.Adapter
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
		channel:        deps.Channel,
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

	if _, err := h.sessionManager.GetSession(h.channel.RootLocator(chatID)); err == nil {
		return nil
	}

	if err := h.ensureRootSession(ctx, ownerID, chatID); err != nil {
		return err
	}
	return nil
}

func (h *RelayHandler) onMessage(ctx context.Context, event *events.MessageEvent) error {
	messageCtx, ok := h.channel.MessageContextFromEvent(event)
	if !ok {
		return nil
	}

	ownerID := h.getOwnerID()
	chatID := h.getChatID()

	if ownerID == 0 {
		return nil
	}

	if messageCtx.UserID != ownerID {
		return nil
	}

	if chatID == 0 {
		h.setChatID(messageCtx.ChatID)
		log.Info().Int64("chat_id", messageCtx.ChatID).Msg("Chat ID set from message")
	}

	if messageCtx.HasCommand {
		return nil
	}

	text := messageCtx.Text
	if text == "" {
		return nil
	}

	locator := messageCtx.Locator
	topicID := messageCtx.TopicID
	transportUserID := relaysession.TelegramUserID(messageCtx.UserID)

	log.Info().Int64("user_id", ownerID).Int("topic_id", topicID).Msg("Relaying message to agent")

	var ts *relaysession.TopicSession
	var err error

	if topicID == 0 {
		if h.rootAgentName == "" {
			_ = h.channel.SendPlain(ctx, locator, "Relay root agent is not configured (`relay.root_agent`). Please close this chat and restart relay.")
			return nil
		}

		existingSession, _ := h.sessionManager.GetSession(locator)
		if existingSession == nil {
			agentDesc, mcpServers := h.sessionManager.GetAgentInfo(h.rootAgentName)
			spinningMsg := BuildAgentWelcomeMessage(h.rootAgentName, "", agentDesc, mcpServers)
			_ = h.channel.SendMarkdown(ctx, locator, spinningMsg)
		}
		ts, err = h.sessionManager.EnsureSession(ctx, relaysession.SessionContext{
			Locator: locator,
			UserID:  transportUserID,
		}, h.rootAgentName)
		if err != nil {
			log.Error().Err(err).Str("agent", h.rootAgentName).Msg("failed to ensure root session")
			_ = h.channel.SendPlain(ctx, locator, fmt.Sprintf("Failed to start root session: %v.\n\nPlease close this chat and start again.", err))
			return nil
		}
	} else {
		ts, err = h.sessionManager.GetSession(locator)
		if err != nil {
			_ = h.channel.SendPlain(ctx, locator, "Restoring agent session...")
			ts, err = h.sessionManager.RestoreSession(ctx, relaysession.SessionContext{
				Locator: locator,
				UserID:  transportUserID,
			})
			if err != nil {
				log.Warn().Err(err).Int("topic_id", topicID).Msg("failed to restore topic session")
				_ = h.channel.SendPlain(ctx, locator, fmt.Sprintf("Failed to restore this session: %v.\n\nPlease close this chat topic and create a new session with /new <agent_name>.", err))
				return nil
			}
			agentDesc, mcpServers := h.sessionManager.GetAgentInfo(ts.GetAgentName())
			welcomeMsg := BuildAgentWelcomeMessage(ts.GetAgentName(), ts.GetSessionID(), agentDesc, mcpServers)
			_ = h.channel.SendMarkdown(ctx, locator, welcomeMsg)
		}
	}

	if err := h.runTurn(ctx, text, ts.GetRunner(), ts.GetUserID(), ts.GetSessionID(), locator, messageCtx.MessageID); err != nil {
		log.Error().Err(err).Int("topic_id", topicID).Msg("Agent execution failed")
		errText := fmt.Sprintf("Agent execution failed: %v.\n\nPlease close this chat and start a new session.", err)
		if topicID > 0 {
			errText = fmt.Sprintf("Agent execution failed: %v.\n\nPlease close this chat topic and create a new session with /new <agent_name>.", err)
		}
		if sendErr := h.channel.SendPlain(ctx, locator, errText); sendErr != nil {
			log.Warn().Err(sendErr).Int("topic_id", topicID).Msg("failed to send relay error message")
		}
	}

	return nil
}

func (h *RelayHandler) onForumTopicLifecycle(_ context.Context, event *events.MessageEvent) error {
	lifecycle, ok := h.channel.TopicLifecycleFromEvent(event)
	if !ok {
		return nil
	}

	chatID := lifecycle.ChatID
	boundChatID := h.getChatID()
	if boundChatID != 0 && chatID != boundChatID {
		return nil
	}

	topicID := lifecycle.TopicID
	if topicID <= 0 {
		h.logger.Debug().
			Int64("chat_id", chatID).
			Str("event_type", string(lifecycle.Type)).
			Msg("ignoring forum topic lifecycle event without topic id")
		return nil
	}

	evt := h.logger.Info().
		Int64("chat_id", chatID).
		Int("topic_id", topicID).
		Int("message_id", lifecycle.MessageID).
		Str("event_type", string(lifecycle.Type))
	if lifecycle.UserID != 0 {
		evt = evt.Int64("user_id", lifecycle.UserID)
	}

	switch lifecycle.Type {
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

func (h *RelayHandler) runTurn(ctx context.Context, text string, r *runner.Runner, userID string, sessionID string, locator relaysession.SessionLocator, messageID int) error {
	address, ok, err := locator.TelegramAddress()
	if err != nil {
		return fmt.Errorf("decode telegram locator: %w", err)
	}
	if !ok {
		return fmt.Errorf("unsupported channel type %q", locator.ChannelType)
	}

	chatID := address.ChatID
	topicID := address.TopicID
	userContent := genai.NewContentFromText(text, genai.RoleUser)
	draftID := messageID + 1

	runCtx := zerolog.Ctx(ctx).With().
		Int64("chat_id", chatID).
		Int("topic_id", topicID).
		Str("session_id", sessionID).
		Str("transport_user_id", userID).
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
					if sendErr := h.channel.SendDraftPlain(ctx, locator, draftID, thinkingStages[thinkingIdx%len(thinkingStages)]); sendErr != nil {
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
		if sendErr := h.channel.SendMarkdown(ctx, locator, s); sendErr != nil {
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

	if err := h.ensureRootSession(ctx, owner.UserID, owner.ChatID); err != nil {
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

func (h *RelayHandler) ensureRootSession(ctx context.Context, ownerID int64, chatID int64) error {
	agentName := h.rootAgentName
	if agentName == "" {
		return fmt.Errorf("relay.root_agent is required")
	}

	locator := h.channel.RootLocator(chatID)
	agentDesc, mcpServers := h.sessionManager.GetAgentInfo(agentName)
	spinningMsg := BuildAgentWelcomeMessage(agentName, "", agentDesc, mcpServers)
	if err := h.channel.SendMarkdown(ctx, locator, spinningMsg); err != nil {
		h.logger.Warn().Err(err).Msg("failed to send spinning up message")
	}

	ts, err := h.sessionManager.EnsureSession(ctx, relaysession.SessionContext{
		Locator: locator,
		UserID:  relaysession.TelegramUserID(ownerID),
	}, agentName)
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
