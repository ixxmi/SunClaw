package providers

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestAnthropicProvider_Chat_Integration(t *testing.T) {
	//apiKey := os.Getenv("ANTHROPIC_API_KEY")
	//if apiKey == "" {
	//	t.Skip("ANTHROPIC_API_KEY not set, skipping integration test")
	//}

	provider, err := NewAnthropicProvider("claude-Sonnet-4-6", "https://api.claudecode.net.cn/api/claudecode", "claude-Sonnet-4-6", 4096)
	require.NoError(t, err)

	messages := []Message{
		{Role: "user", Content: "Hello, Claude!"},
	}

	resp, err := provider.Chat(context.Background(), messages, nil)
	require.NoError(t, err)

	assert.NotEmpty(t, resp.Content)
	t.Logf("Claude response: %s", resp.Content)
}
