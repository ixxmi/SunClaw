package providers

import (
	"testing"
	"time"

	"github.com/smallnest/goclaw/internal/core/config"
)

func TestNewRotationProviderFromConfigUsesProfileModel(t *testing.T) {
	cfg := &config.Config{
		Agents: config.AgentsConfig{
			Defaults: config.AgentDefaults{
				Model:     "gpt-5.3-codex",
				MaxTokens: 1024,
			},
		},
		Providers: config.ProvidersConfig{
			Failover: config.FailoverConfig{
				Enabled:         true,
				Strategy:        "round_robin",
				DefaultCooldown: time.Minute,
				CircuitBreaker: config.CircuitBreakerConfig{
					FailureThreshold: 2,
					Timeout:          time.Minute,
				},
			},
			Profiles: []config.ProviderProfileConfig{
				{
					Name:     "gpt",
					Provider: "openai",
					APIKey:   "test-api-key-123456",
					BaseURL:  "https://example.com/v1",
					Model:    "gpt-5.4",
				},
			},
		},
	}

	provider, err := NewRotationProviderFromConfig(cfg)
	if err != nil {
		t.Fatalf("Expected no error, got %v", err)
	}

	openaiProvider, ok := provider.(*OpenAIProvider)
	if !ok {
		t.Fatalf("Expected single profile to return OpenAIProvider, got %T", provider)
	}
	if openaiProvider.model != "gpt-5.4" {
		t.Fatalf("Expected profile model gpt-5.4, got %q", openaiProvider.model)
	}
}
