package session

import (
	"context"
	"testing"

	"github.com/normahq/norma/internal/apps/relay/auth"
	relaystate "github.com/normahq/norma/internal/apps/relay/state"
	"github.com/normahq/norma/internal/apps/relaymcp"
	"github.com/rs/zerolog"
)

func TestRelayMCPListAgents_IncludesPersistedSessions(t *testing.T) {
	store := &fakeSessionStore{
		listRecords: []relaystate.SessionRecord{
			{
				SessionID:    "tg-7-8",
				ChannelType:  relaystate.ChannelTypeTelegram,
				AddressKey:   "7:8",
				AddressJSON:  `{"chat_id":7,"topic_id":8}`,
				AgentName:    "opencode",
				WorkspaceDir: "/tmp/persisted",
				BranchName:   "norma/relay/tg-7-8",
				Status:       relaystate.SessionStatusActive,
			},
		},
	}

	manager := &Manager{
		logger:       zerolog.Nop(),
		sessionStore: store,
		sessions:     map[string]*TopicSession{},
	}
	svc := &relayMCPServer{manager: manager, logger: zerolog.Nop()}

	agents, err := svc.ListAgents(context.Background())
	if err != nil {
		t.Fatalf("ListAgents() error = %v", err)
	}
	if len(agents) != 1 {
		t.Fatalf("ListAgents() len = %d, want 1", len(agents))
	}
	if agents[0].SessionID != "tg-7-8" || agents[0].Status != sessionStatusPersisted {
		t.Fatalf("ListAgents()[0] = %+v, want persisted tg-7-8", agents[0])
	}
}

func TestRelayMCPStopAgent_StopsPersistedSession(t *testing.T) {
	store := &fakeSessionStore{
		recordsByID: map[string]relaystate.SessionRecord{
			"tg-5-6": {
				SessionID:    "tg-5-6",
				ChannelType:  relaystate.ChannelTypeTelegram,
				AddressKey:   "5:6",
				AddressJSON:  `{"chat_id":5,"topic_id":6}`,
				AgentName:    "opencode",
				WorkspaceDir: "",
				BranchName:   "",
				Status:       relaystate.SessionStatusActive,
			},
		},
	}

	manager := &Manager{
		logger:       zerolog.Nop(),
		sessionStore: store,
		sessions:     map[string]*TopicSession{},
	}
	svc := &relayMCPServer{manager: manager, logger: zerolog.Nop()}

	if err := svc.StopAgent(context.Background(), "tg-5-6"); err != nil {
		t.Fatalf("StopAgent() error = %v", err)
	}
	if store.deletedSessionID != "tg-5-6" {
		t.Fatalf("DeleteBySessionID called with %q, want %q", store.deletedSessionID, "tg-5-6")
	}
}

func TestResolveStartContext_UsesCallerSessionContext(t *testing.T) {
	manager := &Manager{
		logger: zerolog.Nop(),
		sessions: map[string]*TopicSession{
			"tg-5-0": {
				sessionID: "tg-5-0",
				userID:    "tg-101",
				locator:   NewTelegramSessionLocator(5, 0),
				agentName: "root",
				chatID:    5,
				topicID:   0,
			},
		},
	}
	svc := &relayMCPServer{manager: manager, logger: zerolog.Nop()}

	sessionCtx, err := svc.resolveStartContext(context.Background(), relaymcp.StartRequest{
		AgentName:       "opencode",
		CallerSessionID: "tg-5-0",
	})
	if err != nil {
		t.Fatalf("resolveStartContext() error = %v", err)
	}
	address, ok, err := sessionCtx.Locator.TelegramAddress()
	if err != nil {
		t.Fatalf("TelegramAddress() error = %v", err)
	}
	if !ok || address.ChatID != 5 || address.TopicID != 0 {
		t.Fatalf("resolveStartContext() locator = %+v, want telegram root chat 5", sessionCtx.Locator)
	}
	if sessionCtx.UserID != "tg-101" {
		t.Fatalf("resolveStartContext() user_id = %q, want tg-101", sessionCtx.UserID)
	}
}

func TestSessionLocatorFromStartLocator_TelegramRequiresChatIDAndNoTopic(t *testing.T) {
	_, err := sessionLocatorFromStartLocator(&relaymcp.StartLocator{
		ChannelType: relaystate.ChannelTypeTelegram,
		Address: map[string]any{
			"topic_id": float64(7),
		},
	})
	if err == nil {
		t.Fatal("sessionLocatorFromStartLocator() error = nil, want validation error")
	}
}

func TestResolveStartContext_ExplicitLocatorUsesOwnerUserID(t *testing.T) {
	svc := &relayMCPServer{
		logger: zerolog.Nop(),
		owners: fakeRelayOwnerStore{owner: &auth.Owner{UserID: 2317500}},
	}

	sessionCtx, err := svc.resolveStartContext(context.Background(), relaymcp.StartRequest{
		AgentName: "opencode",
		Locator: &relaymcp.StartLocator{
			ChannelType: relaystate.ChannelTypeTelegram,
			Address: map[string]any{
				"chat_id": float64(5),
			},
		},
	})
	if err != nil {
		t.Fatalf("resolveStartContext() error = %v", err)
	}
	if sessionCtx.UserID != "tg-2317500" {
		t.Fatalf("resolveStartContext() user_id = %q, want tg-2317500", sessionCtx.UserID)
	}
}

type fakeRelayOwnerStore struct {
	owner *auth.Owner
}

func (f fakeRelayOwnerStore) GetOwner() *auth.Owner {
	return f.owner
}
