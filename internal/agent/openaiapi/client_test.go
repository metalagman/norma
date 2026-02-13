package openaiapi

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
)

func TestClientComplete_SendsExpectedPayloadAndParsesOutput(t *testing.T) {
	const envKey = "NORMA_OPENAI_TEST_KEY"
	t.Setenv(envKey, "test-api-key")

	var gotAuth string
	var gotPath string
	var gotBody map[string]any

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		gotPath = r.URL.Path

		body, err := io.ReadAll(r.Body)
		if err != nil {
			t.Fatalf("read request body: %v", err)
		}
		if err := json.Unmarshal(body, &gotBody); err != nil {
			t.Fatalf("unmarshal request body: %v", err)
		}

		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{
			"error": {"code": "", "message": ""},
			"output": [
				{
					"type": "message",
					"role": "assistant",
					"content": [
						{"type": "output_text", "text": "{\"status\":\"ok\"}", "annotations": []}
					]
				}
			]
		}`))
	}))
	t.Cleanup(srv.Close)

	client, err := NewClient(Config{
		Model:     "gpt-5",
		BaseURL:   srv.URL,
		APIKeyEnv: envKey,
	}, srv.Client())
	if err != nil {
		t.Fatalf("NewClient returned error: %v", err)
	}

	out, err := client.Complete(context.Background(), CompletionRequest{
		Instructions: "Output only JSON.",
		Input:        `{"task":"demo"}`,
	})
	if err != nil {
		t.Fatalf("Complete returned error: %v", err)
	}
	if out.OutputText != `{"status":"ok"}` {
		t.Fatalf("output text = %q, want %q", out.OutputText, `{"status":"ok"}`)
	}

	if gotAuth != "Bearer test-api-key" {
		t.Fatalf("authorization header = %q, want bearer auth", gotAuth)
	}
	if gotPath != "/responses" {
		t.Fatalf("path = %q, want %q", gotPath, "/responses")
	}
	if gotBody["model"] != "gpt-5" {
		t.Fatalf("model = %v, want %q", gotBody["model"], "gpt-5")
	}
	if gotBody["instructions"] != "Output only JSON." {
		t.Fatalf("instructions = %v, want %q", gotBody["instructions"], "Output only JSON.")
	}
	if gotBody["input"] != `{"task":"demo"}` {
		t.Fatalf("input = %v, want %q", gotBody["input"], `{"task":"demo"}`)
	}
}

func TestNewClient_ReturnsErrorWhenAPIKeyMissing(t *testing.T) {
	const envKey = "NORMA_OPENAI_MISSING_KEY"
	if err := os.Unsetenv(envKey); err != nil {
		t.Fatalf("unset env: %v", err)
	}

	_, err := NewClient(Config{
		Model:     "gpt-5",
		BaseURL:   "http://127.0.0.1",
		APIKeyEnv: envKey,
	}, nil)
	if err == nil {
		t.Fatal("NewClient returned nil error, want error")
	}
}

func TestClientComplete_ReturnsErrorWhenOutputTextMissing(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{
			"error": {"code": "", "message": ""},
			"output": []
		}`))
	}))
	t.Cleanup(srv.Close)

	client, err := NewClient(Config{
		Model:   "gpt-5",
		BaseURL: srv.URL,
		APIKey:  "test-api-key",
	}, srv.Client())
	if err != nil {
		t.Fatalf("NewClient returned error: %v", err)
	}

	_, err = client.Complete(context.Background(), CompletionRequest{
		Instructions: "Output JSON",
		Input:        "{}",
	})
	if err == nil {
		t.Fatal("Complete returned nil error, want error")
	}
	if !strings.Contains(err.Error(), "output text") {
		t.Fatalf("error = %q, want output text failure", err.Error())
	}
}
