package openaiapi

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"
)

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(req *http.Request) (*http.Response, error) {
	return f(req)
}

func TestClientComplete_SendsExpectedPayloadAndParsesOutput(t *testing.T) {
	var gotAuth string
	var gotPath string
	var gotBody map[string]any

	httpClient := &http.Client{
		Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
			gotAuth = req.Header.Get("Authorization")
			gotPath = req.URL.Path

			body, err := io.ReadAll(req.Body)
			if err != nil {
				t.Fatalf("read request body: %v", err)
			}
			if err := json.Unmarshal(body, &gotBody); err != nil {
				t.Fatalf("unmarshal request body: %v", err)
			}

			return &http.Response{
				StatusCode: http.StatusOK,
				Header:     http.Header{"Content-Type": []string{"application/json"}},
				Body: io.NopCloser(strings.NewReader(`{
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
				}`)),
				Request: req,
			}, nil
		}),
	}

	client, err := NewClient(Config{
		Model:   "gpt-5",
		BaseURL: "https://api.example.test",
		APIKey:  "test-api-key",
	}, httpClient)
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
	_, err := NewClient(Config{
		Model:   "gpt-5",
		BaseURL: "http://127.0.0.1",
	}, nil)
	if err == nil {
		t.Fatal("NewClient returned nil error, want error")
	}
}

func TestClientComplete_ReturnsErrorWhenOutputTextMissing(t *testing.T) {
	httpClient := &http.Client{
		Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
			return &http.Response{
				StatusCode: http.StatusOK,
				Header:     http.Header{"Content-Type": []string{"application/json"}},
				Body: io.NopCloser(strings.NewReader(`{
					"error": {"code": "", "message": ""},
					"output": []
				}`)),
				Request: req,
			}, nil
		}),
	}

	client, err := NewClient(Config{
		Model:   "gpt-5",
		BaseURL: "https://api.example.test",
		APIKey:  "test-api-key",
	}, httpClient)
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
