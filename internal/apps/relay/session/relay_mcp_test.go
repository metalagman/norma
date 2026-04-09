package session

import (
	"context"
	"testing"

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

func TestResolveStartLocator_UsesCallerSessionContext(t *testing.T) {
	store := &fakeSessionStore{
		recordsByID: map[string]relaystate.SessionRecord{
			"tg-5-0": {
				SessionID:    "tg-5-0",
				ChannelType:  relaystate.ChannelTypeTelegram,
				AddressKey:   "5:0",
				AddressJSON:  `{"chat_id":5,"topic_id":0}`,
				AgentName:    "root",
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

	locator, err := svc.resolveStartLocator(context.Background(), relaymcp.StartRequest{
		AgentName:       "opencode",
		CallerSessionID: "tg-5-0",
	})
	if err != nil {
		t.Fatalf("resolveStartLocator() error = %v", err)
	}
	address, ok, err := locator.TelegramAddress()
	if err != nil {
		t.Fatalf("TelegramAddress() error = %v", err)
	}
	if !ok || address.ChatID != 5 || address.TopicID != 0 {
		t.Fatalf("resolveStartLocator() = %+v, want telegram root chat 5", locator)
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
