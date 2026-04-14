package handlers

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"

	"github.com/normahq/norma/internal/apps/relay/auth"
	relaytelegram "github.com/normahq/norma/internal/apps/relay/channel/telegram"
	"github.com/normahq/norma/internal/apps/relay/messenger"
	"github.com/normahq/norma/internal/apps/relay/runtimecfg"
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
	turnDispatcher turnQueue
	messenger      *messenger.Messenger
	configLoader   runtimeConfigLoader
	tgClient       client.ClientWithResponsesInterface
	authToken      string
	rootAgentName  string
	normaCfg       runtimeconfig.RuntimeConfig
	logger         zerolog.Logger

	mu          sync.RWMutex
	ownerID     int64
	chatID      int64
	botUsername string
}

type runtimeConfigLoader interface {
	Load() (runtimecfg.Snapshot, error)
}

type relayHandlerDeps struct {
	fx.In

	LC                 fx.Lifecycle
	OwnerStore         *auth.OwnerStore
	Channel            *relaytelegram.Adapter
	SessionManager     *relaysession.Manager
	TurnDispatcher     *TurnDispatcher
	Messenger          *messenger.Messenger
	RuntimeConfig      *runtimecfg.Loader
	TGClient           client.ClientWithResponsesInterface
	AuthToken          string `name:"relay_auth_token"`
	RootAgentName      string `name:"relay_root_agent"`
	NormaCfg           runtimeconfig.RuntimeConfig
	Logger             zerolog.Logger
	InternalMCPManager *InternalMCPManager `optional:"true"`
}

func NewRelayHandler(deps relayHandlerDeps) (*RelayHandler, error) {
	h := &RelayHandler{
		ownerStore:     deps.OwnerStore,
		channel:        deps.Channel,
		sessionManager: deps.SessionManager,
		turnDispatcher: deps.TurnDispatcher,
		messenger:      deps.Messenger,
		configLoader:   deps.RuntimeConfig,
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

// ActivateOwner binds owner/chat for relay traffic and bootstraps the root session.
func (h *RelayHandler) ActivateOwner(ctx context.Context, ownerID, chatID int64) error {
	h.SetOwner(ownerID, chatID)
	return h.bootstrapRootSession(ctx, ownerID, chatID)
}

func (h *RelayHandler) bootstrapRootSession(ctx context.Context, ownerID, chatID int64) error {
	if err := h.refreshRuntimeConfig(); err != nil {
		return fmt.Errorf("refresh runtime config: %w", err)
	}

	rootAgentName := h.getRootAgentName()
	if rootAgentName == "" {
		return fmt.Errorf("relay root agent is not configured")
	}

	locator := relaysession.NewTelegramSessionLocator(chatID, 0)
	transportUserID := relaysession.TelegramUserID(ownerID)

	ts, err := h.sessionManager.EnsureSession(ctx, relaysession.SessionContext{
		Locator: locator,
		UserID:  transportUserID,
	}, rootAgentName)
	if err != nil {
		return fmt.Errorf("create root session: %w", err)
	}

	agentDesc, mcpServers := h.sessionManager.GetAgentInfo(rootAgentName)
	welcomeMsg := BuildAgentWelcomeMessage(rootAgentName, ts.GetSessionID(), agentDesc, mcpServers)
	_ = h.channel.SendMarkdown(ctx, locator, welcomeMsg)

	h.logger.Info().
		Int64("owner_id", ownerID).
		Int64("chat_id", chatID).
		Str("agent", rootAgentName).
		Msg("root session bootstrapped")
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
		existingSession, _ := h.sessionManager.GetSession(locator)
		if existingSession == nil {
			if err := h.refreshRuntimeConfig(); err != nil {
				h.logger.Error().Err(err).Int64("chat_id", locatorChatID(locator)).Msg("failed to refresh runtime config before root session creation")
				_ = h.channel.SendPlain(ctx, locator, "Failed to reload relay config for root session creation. Please check config and try again.")
				return nil
			}
			rootAgentName := h.getRootAgentName()
			if rootAgentName == "" {
				_ = h.channel.SendPlain(ctx, locator, "Relay root agent is not configured (`relay.root_agent`). Please close this chat and restart relay.")
				return nil
			}
			agentDesc, mcpServers := h.sessionManager.GetAgentInfo(rootAgentName)
			spinningMsg := BuildAgentWelcomeMessage(rootAgentName, "", agentDesc, mcpServers)
			_ = h.channel.SendMarkdown(ctx, locator, spinningMsg)
		}
		rootAgentName := h.getRootAgentName()
		if rootAgentName == "" {
			_ = h.channel.SendPlain(ctx, locator, "Relay root agent is not configured (`relay.root_agent`). Please close this chat and restart relay.")
			return nil
		}
		ts, err = h.sessionManager.EnsureSession(ctx, relaysession.SessionContext{
			Locator: locator,
			UserID:  transportUserID,
		}, rootAgentName)
		if err != nil {
			log.Error().Err(err).Str("agent", rootAgentName).Msg("failed to ensure root session")
			_ = h.channel.SendPlain(ctx, locator, fmt.Sprintf("Failed to start root session: %v.\n\nPlease close this chat and start again.", err))
			return nil
		}
	} else {
		welcomeSent := false
		ts, err = h.sessionManager.GetSession(locator)
		if err != nil {
			_ = h.channel.SendPlain(ctx, locator, "Restoring agent session...")
			ts, err = h.sessionManager.RestoreSession(ctx, relaysession.SessionContext{
				Locator: locator,
				UserID:  transportUserID,
			})
			if err != nil {
				if errors.Is(err, relaysession.ErrNoPersistedSession) {
					if refreshErr := h.refreshRuntimeConfig(); refreshErr != nil {
						h.logger.Error().Err(refreshErr).Int64("chat_id", locatorChatID(locator)).Msg("failed to refresh runtime config before topic session creation")
						_ = h.channel.SendPlain(ctx, locator, "Failed to reload relay config for topic session creation. Please check config and try again.")
						return nil
					}
					rootAgentName := h.getRootAgentName()
					if rootAgentName == "" {
						_ = h.channel.SendPlain(ctx, locator, "Relay root agent is not configured (`relay.root_agent`). Please close this chat and restart relay.")
						return nil
					}
					agentDesc, mcpServers := h.sessionManager.GetAgentInfo(rootAgentName)
					startMsg := BuildAgentWelcomeMessage(rootAgentName, locator.SessionID, agentDesc, mcpServers)
					_ = h.channel.SendMarkdown(ctx, locator, startMsg)
					welcomeSent = true

					ts, err = h.sessionManager.EnsureSession(ctx, relaysession.SessionContext{
						Locator: locator,
						UserID:  transportUserID,
					}, rootAgentName)
					if err != nil {
						log.Error().Err(err).Str("agent", rootAgentName).Int("topic_id", topicID).Msg("failed to create topic session")
						_ = h.channel.SendPlain(ctx, locator, fmt.Sprintf("Failed to start topic session: %v.\n\nPlease close this chat topic and create a new session with /new <agent_name>.", err))
						return nil
					}
				} else {
					log.Warn().Err(err).Int("topic_id", topicID).Msg("failed to restore topic session")
					_ = h.channel.SendPlain(ctx, locator, fmt.Sprintf("Failed to restore this session: %v.\n\nPlease close this chat topic and create a new session with /new <agent_name>.", err))
					return nil
				}
			}
			if ts != nil && !welcomeSent {
				agentDesc, mcpServers := h.sessionManager.GetAgentInfo(ts.GetAgentName())
				welcomeMsg := BuildAgentWelcomeMessage(ts.GetAgentName(), ts.GetSessionID(), agentDesc, mcpServers)
				_ = h.channel.SendMarkdown(ctx, locator, welcomeMsg)
			}
		}
	}

	if h.turnDispatcher == nil {
		if err := h.runTurnTask(ctx, text, ts.GetRunner(), ts.GetUserID(), ts.GetSessionID(), locator, messageCtx.MessageID, topicID, messageCtx.AllowProgressHints); err != nil {
			log.Error().Err(err).Int("topic_id", topicID).Msg("Agent execution failed")
		}
		return nil
	}

	if err := h.enqueueTurn(ctx, text, ts, locator, messageCtx.MessageID, topicID, messageCtx.AllowProgressHints); err != nil {
		if errors.Is(err, ErrTurnQueueFull) {
			_ = h.channel.SendPlain(ctx, locator, fmt.Sprintf("Session is busy and queue is full (%d). Please wait or use /cancel.", perSessionQueueLimit))
			return nil
		}

		log.Error().Err(err).Int("topic_id", topicID).Msg("failed to enqueue relay turn")
		_ = h.channel.SendPlain(ctx, locator, "Failed to queue your message for processing. Please try again.")
	}

	return nil
}

func (h *RelayHandler) enqueueTurn(
	ctx context.Context,
	text string,
	ts *relaysession.TopicSession,
	locator relaysession.SessionLocator,
	messageID int,
	topicID int,
	allowProgressHints bool,
) error {
	if ts == nil {
		return fmt.Errorf("topic session is required")
	}

	position, err := h.turnDispatcher.Enqueue(TurnTask{
		SessionID: ts.GetSessionID(),
		Run: func(runCtx context.Context) error {
			if _, getErr := h.sessionManager.GetSession(locator); getErr != nil {
				h.logger.Debug().
					Str("session_id", locator.SessionID).
					Str("address_key", locator.AddressKey).
					Msg("dropping queued turn for inactive session")
				return nil
			}
			return h.runTurnTask(runCtx, text, ts.GetRunner(), ts.GetUserID(), ts.GetSessionID(), locator, messageID, topicID, allowProgressHints)
		},
	})
	if err != nil {
		return err
	}

	if position > 0 {
		queuedMsg := fmt.Sprintf("Message queued (position %d). I will process it after the current turn.", position)
		if sendErr := h.channel.SendPlain(ctx, locator, queuedMsg); sendErr != nil {
			h.logger.Warn().
				Err(sendErr).
				Str("session_id", ts.GetSessionID()).
				Int("position", position).
				Msg("failed to send queued message notice")
		}
	}

	return nil
}

func (h *RelayHandler) runTurnTask(
	ctx context.Context,
	text string,
	r *runner.Runner,
	userID string,
	sessionID string,
	locator relaysession.SessionLocator,
	messageID int,
	topicID int,
	allowProgressHints bool,
) error {
	err := h.runTurn(ctx, text, r, userID, sessionID, locator, messageID, allowProgressHints)
	if err == nil {
		return nil
	}
	if errors.Is(err, context.Canceled) {
		h.logger.Info().
			Str("session_id", sessionID).
			Int("topic_id", topicID).
			Msg("relay turn canceled")
		return nil
	}
	if _, getErr := h.sessionManager.GetSession(locator); getErr != nil {
		h.logger.Debug().
			Str("session_id", sessionID).
			Int("topic_id", topicID).
			Msg("suppressing relay turn error for inactive session")
		return nil
	}

	log.Error().Err(err).Int("topic_id", topicID).Msg("agent execution failed")
	errText := fmt.Sprintf("Agent execution failed: %v.\n\nPlease close this chat and start a new session.", err)
	if topicID > 0 {
		errText = fmt.Sprintf("Agent execution failed: %v.\n\nPlease close this chat topic and create a new session with /new <agent_name>.", err)
	}
	if sendErr := h.channel.SendPlain(context.Background(), locator, errText); sendErr != nil {
		log.Warn().Err(sendErr).Int("topic_id", topicID).Msg("failed to send relay error message")
	}
	return err
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

func (h *RelayHandler) runTurn(
	ctx context.Context,
	text string,
	r *runner.Runner,
	userID string,
	sessionID string,
	locator relaysession.SessionLocator,
	messageID int,
	allowProgressHints bool,
) error {
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
					if allowProgressHints {
						if sendErr := h.channel.SendDraftPlain(ctx, locator, draftID, thinkingStages[thinkingIdx%len(thinkingStages)]); sendErr != nil {
							log.Warn().Err(sendErr).Int("topic_id", topicID).Msg("failed to send thinking draft")
						}
						if sendErr := h.channel.SendTyping(ctx, locator); sendErr != nil {
							log.Warn().Err(sendErr).Int("topic_id", topicID).Msg("failed to send typing chat action")
						}
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

	if err := h.bootstrapRootSession(ctx, owner.UserID, owner.ChatID); err != nil {
		h.logger.Error().Err(err).Int64("owner_id", owner.UserID).Msg("failed to bootstrap root session during startup")
		if sendErr := h.messenger.SendPlain(ctx, owner.UserID, fmt.Sprintf("Failed to start root session: %v.\nPlease check relay configuration.", err), 0); sendErr != nil {
			h.logger.Warn().Err(sendErr).Msg("failed to send root session failure message")
		}
		return nil
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

func (h *RelayHandler) getRootAgentName() string {
	h.mu.RLock()
	defer h.mu.RUnlock()
	return h.rootAgentName
}

func (h *RelayHandler) setRootAgentName(agentName string) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.rootAgentName = strings.TrimSpace(agentName)
}

func (h *RelayHandler) refreshRuntimeConfig() error {
	if h.configLoader == nil {
		return nil
	}
	snapshot, err := h.configLoader.Load()
	if err != nil {
		return err
	}
	if err := h.sessionManager.ApplyRuntimeConfig(snapshot.Runtime, snapshot.Relay); err != nil {
		return err
	}
	h.setRootAgentName(snapshot.Relay.RootAgent)
	return nil
}

func locatorChatID(locator relaysession.SessionLocator) int64 {
	address, ok, err := locator.TelegramAddress()
	if err != nil || !ok {
		return 0
	}
	return address.ChatID
}
