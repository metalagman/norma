package hostedagent

import (
	"context"
	"iter"
	"testing"

	"google.golang.org/adk/v2/agent"
	"google.golang.org/adk/v2/model"
	"google.golang.org/adk/v2/runner"
	"google.golang.org/adk/v2/session"
	"google.golang.org/adk/v2/tool"
	"google.golang.org/adk/v2/tool/functiontool"
	"google.golang.org/genai"
)

type captureModel struct {
	req *model.LLMRequest
}

func (*captureModel) Name() string {
	return "capture-model"
}

func (m *captureModel) GenerateContent(_ context.Context, req *model.LLMRequest, _ bool) iter.Seq2[*model.LLMResponse, error] {
	m.req = req
	return func(yield func(*model.LLMResponse, error) bool) {
		yield(&model.LLMResponse{
			Content: genai.NewContentFromText("ok", genai.RoleModel),
		}, nil)
	}
}

type pingArgs struct {
	Value string `json:"value"`
}

type pingResult struct {
	Value string `json:"value"`
}

func TestNew_PassesToolsToLLMAgent(t *testing.T) {
	m := &captureModel{}
	ping, err := functiontool.New(functiontool.Config{
		Name:        "ping",
		Description: "Returns the input value.",
	}, func(_ agent.Context, args pingArgs) (pingResult, error) {
		return pingResult(args), nil
	})
	if err != nil {
		t.Fatalf("functiontool.New() error = %v", err)
	}

	agentRuntime, err := New(Config{
		Name:        "hosted",
		Description: "Hosted test agent",
		Model:       m,
		Tools:       []tool.Tool{ping},
	})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	sessionService := session.InMemoryService()
	r, err := runner.New(runner.Config{
		AppName:        "hostedagent-test",
		Agent:          agentRuntime,
		SessionService: sessionService,
	})
	if err != nil {
		t.Fatalf("runner.New() error = %v", err)
	}
	created, err := sessionService.Create(context.Background(), &session.CreateRequest{
		AppName: "hostedagent-test",
		UserID:  "test-user",
	})
	if err != nil {
		t.Fatalf("session.Create() error = %v", err)
	}

	for _, runErr := range r.Run(context.Background(), "test-user", created.Session.ID(), genai.NewContentFromText("hello", genai.RoleUser), agent.RunConfig{}) {
		if runErr != nil {
			t.Fatalf("runner.Run() error = %v", runErr)
		}
	}

	if m.req == nil {
		t.Fatal("model did not receive a request")
	}
	if m.req.Config == nil || len(m.req.Config.Tools) != 1 {
		t.Fatalf("model request tools = %#v, want one tool", m.req.Config)
	}
	decls := m.req.Config.Tools[0].FunctionDeclarations
	if len(decls) != 1 || decls[0].Name != "ping" {
		t.Fatalf("function declarations = %#v, want ping", decls)
	}
}
