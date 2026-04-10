package handlers

import (
	"context"
	"iter"
	"net/http"
	"strings"
	"testing"

	relaytelegram "github.com/normahq/norma/internal/apps/relay/channel/telegram"
	"github.com/normahq/norma/internal/apps/relay/messenger"
	relaysession "github.com/normahq/norma/internal/apps/relay/session"
	"github.com/rs/zerolog"
	"github.com/tgbotkit/client"
	adkagent "google.golang.org/adk/agent"
	"google.golang.org/adk/runner"
	adksession "google.golang.org/adk/session"
	"google.golang.org/genai"
)

type relayRunTurnTelegramClient struct {
	client.ClientWithResponsesInterface
	drafts      []client.SendMessageDraftJSONRequestBody
	messages    []client.SendMessageJSONRequestBody
	chatActions []client.SendChatActionJSONRequestBody
}

func (c *relayRunTurnTelegramClient) SendMessageWithResponse(
	_ context.Context,
	body client.SendMessageJSONRequestBody,
	_ ...client.RequestEditorFn,
) (*client.SendMessageResponse, error) {
	c.messages = append(c.messages, body)
	return &client.SendMessageResponse{
		HTTPResponse: &http.Response{StatusCode: http.StatusOK, Status: "200 OK"},
		JSON200: &struct {
			Ok     client.SendMessage200Ok `json:"ok"`
			Result client.Message          `json:"result"`
		}{
			Ok:     true,
			Result: client.Message{MessageId: len(c.messages)},
		},
	}, nil
}

func (c *relayRunTurnTelegramClient) SendMessageDraftWithResponse(
	_ context.Context,
	body client.SendMessageDraftJSONRequestBody,
	_ ...client.RequestEditorFn,
) (*client.SendMessageDraftResponse, error) {
	c.drafts = append(c.drafts, body)
	return &client.SendMessageDraftResponse{
		HTTPResponse: &http.Response{StatusCode: http.StatusOK, Status: "200 OK"},
		JSON200: &struct {
			Ok     client.SendMessageDraft200Ok `json:"ok"`
			Result bool                         `json:"result"`
		}{
			Ok:     true,
			Result: true,
		},
	}, nil
}

func (c *relayRunTurnTelegramClient) SendChatActionWithResponse(
	_ context.Context,
	body client.SendChatActionJSONRequestBody,
	_ ...client.RequestEditorFn,
) (*client.SendChatActionResponse, error) {
	c.chatActions = append(c.chatActions, body)
	return &client.SendChatActionResponse{
		HTTPResponse: &http.Response{StatusCode: http.StatusOK, Status: "200 OK"},
		JSON200: &struct {
			Ok     client.SendChatAction200Ok `json:"ok"`
			Result bool                       `json:"result"`
		}{
			Ok:     true,
			Result: true,
		},
	}, nil
}

func TestRunTurn_SendsTypingForThoughtDraftUpdates(t *testing.T) {
	t.Parallel()

	tgClient := &relayRunTurnTelegramClient{}
	msg := messenger.NewMessenger(tgClient, zerolog.Nop())
	channel := relaytelegram.NewAdapter(relaytelegram.AdapterParams{
		Messenger: msg,
		TGClient:  tgClient,
		Logger:    zerolog.Nop(),
	})
	h := &RelayHandler{
		channel: channel,
		logger:  zerolog.Nop(),
	}

	adkRunner, sessionID := newRelayRunTurnTestRunner(t)
	locator := relaysession.NewTelegramSessionLocator(9001, 77)
	if err := h.runTurn(context.Background(), "hello", adkRunner, "tg-101", sessionID, locator, 41); err != nil {
		t.Fatalf("runTurn() error = %v", err)
	}

	if len(tgClient.drafts) != 2 {
		t.Fatalf("draft calls = %d, want 2", len(tgClient.drafts))
	}
	if got := tgClient.drafts[0].Text; got != "Thinking." {
		t.Fatalf("draft[0].text = %q, want Thinking.", got)
	}
	if got := tgClient.drafts[1].Text; got != "Thinking.." {
		t.Fatalf("draft[1].text = %q, want Thinking..", got)
	}
	for i, draft := range tgClient.drafts {
		if draft.MessageThreadId == nil || *draft.MessageThreadId != 77 {
			t.Fatalf("draft[%d].message_thread_id = %v, want 77", i, draft.MessageThreadId)
		}
	}

	if len(tgClient.chatActions) != 2 {
		t.Fatalf("chat action calls = %d, want 2", len(tgClient.chatActions))
	}
	for i, action := range tgClient.chatActions {
		if action.Action != "typing" {
			t.Fatalf("chatActions[%d].action = %q, want typing", i, action.Action)
		}
		if action.ChatId != 9001 {
			t.Fatalf("chatActions[%d].chat_id = %d, want 9001", i, action.ChatId)
		}
		if action.MessageThreadId == nil || *action.MessageThreadId != 77 {
			t.Fatalf("chatActions[%d].message_thread_id = %v, want 77", i, action.MessageThreadId)
		}
	}

	if len(tgClient.messages) != 1 {
		t.Fatalf("message calls = %d, want 1", len(tgClient.messages))
	}
	if !strings.Contains(tgClient.messages[0].Text, "final answer") {
		t.Fatalf("message text = %q, want to contain final answer", tgClient.messages[0].Text)
	}
}

func newRelayRunTurnTestRunner(t *testing.T) (*runner.Runner, string) {
	t.Helper()

	relayAgent, err := adkagent.New(adkagent.Config{
		Name:        "RelayRunTurnTestAgent",
		Description: "Emits thought and final text events for relay runTurn tests",
		Run: func(ctx adkagent.InvocationContext) iter.Seq2[*adksession.Event, error] {
			return func(yield func(*adksession.Event, error) bool) {
				thoughtOne := adksession.NewEvent(ctx.InvocationID())
				thoughtOne.Content = &genai.Content{
					Role:  genai.RoleModel,
					Parts: []*genai.Part{{Thought: true}},
				}
				if !yield(thoughtOne, nil) {
					return
				}

				thoughtTwo := adksession.NewEvent(ctx.InvocationID())
				thoughtTwo.Content = &genai.Content{
					Role:  genai.RoleModel,
					Parts: []*genai.Part{{Thought: true}},
				}
				if !yield(thoughtTwo, nil) {
					return
				}

				reply := adksession.NewEvent(ctx.InvocationID())
				reply.Content = genai.NewContentFromText("final answer", genai.RoleModel)
				if !yield(reply, nil) {
					return
				}

				done := adksession.NewEvent(ctx.InvocationID())
				done.TurnComplete = true
				_ = yield(done, nil)
			}
		},
	})
	if err != nil {
		t.Fatalf("agent.New() error = %v", err)
	}

	sessionService := adksession.InMemoryService()
	adkRunner, err := runner.New(runner.Config{
		AppName:        "relay-run-turn-test",
		Agent:          relayAgent,
		SessionService: sessionService,
	})
	if err != nil {
		t.Fatalf("runner.New() error = %v", err)
	}

	sess, err := sessionService.Create(context.Background(), &adksession.CreateRequest{
		AppName: "relay-run-turn-test",
		UserID:  "tg-101",
	})
	if err != nil {
		t.Fatalf("session.Create() error = %v", err)
	}

	return adkRunner, sess.Session.ID()
}
