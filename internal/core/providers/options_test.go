package providers

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"reflect"
	"testing"
)

func TestOpenAIProviderChatUsesModelOverride(t *testing.T) {
	var capturedModel string

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer r.Body.Close()

		var body map[string]any
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatalf("decode request: %v", err)
		}
		if model, ok := body["model"].(string); ok {
			capturedModel = model
		}

		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"choices":[{"message":{"content":"ok"},"finish_reason":"stop"}],"usage":{"prompt_tokens":1,"completion_tokens":1,"total_tokens":2}}`))
	}))
	defer server.Close()

	provider, err := NewOpenAIProvider("test-key", server.URL, "gpt-4o-mini", 1024)
	if err != nil {
		t.Fatalf("NewOpenAIProvider error: %v", err)
	}

	if _, err := provider.Chat(context.Background(), []Message{{Role: "user", Content: "hello"}}, nil, WithModel("gpt-5.4")); err != nil {
		t.Fatalf("Chat error: %v", err)
	}
	if capturedModel != "gpt-5.4" {
		t.Fatalf("expected overridden model gpt-5.4, got %q", capturedModel)
	}
}

func TestBuildAnthropicParamsUsesThinkingOption(t *testing.T) {
	params := buildAnthropicParams(
		[]Message{{Role: "user", Content: "hello"}},
		nil,
		&ChatOptions{
			MaxTokens:   8192,
			Temperature: 0.7,
			Thinking:    "low",
		},
		"claude-sonnet-4.6",
	)

	if reflect.ValueOf(params.Thinking).IsZero() {
		t.Fatalf("expected thinking config to be enabled")
	}
	if params.Temperature.Valid() {
		t.Fatalf("expected temperature to be cleared when thinking is enabled")
	}
	if string(params.Model) != "claude-sonnet-4-6" {
		t.Fatalf("expected normalized anthropic model, got %q", string(params.Model))
	}
}
