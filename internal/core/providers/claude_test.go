package providers

import (
	"context"
	"os"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestAnthropicProvider_Chat_Integration(t *testing.T) {
	apiKey := os.Getenv("ANTHROPIC_API_KEY")
	if apiKey == "" {
		t.Skip("ANTHROPIC_API_KEY not set, skipping integration test")
	}

	baseURL := os.Getenv("ANTHROPIC_BASE_URL")
	model := os.Getenv("ANTHROPIC_MODEL")
	if model == "" {
		model = "claude-3-5-sonnet-20240620"
	}

	provider, err := NewAnthropicProvider(apiKey, baseURL, model, 4096)
	require.NoError(t, err)

	messages := []Message{
		{Role: "user", Content: "Hello, Claude!"},
	}

	resp, err := provider.Chat(context.Background(), messages, nil)
	require.NoError(t, err)

	assert.NotEmpty(t, resp.Content)
	t.Logf("Claude response: %s", resp.Content)
}
