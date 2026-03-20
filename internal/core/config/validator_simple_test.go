package config

import (
	"testing"
	"time"

	"github.com/smallnest/goclaw/internal/platform/errors"
)

func TestValidatorValidConfig(t *testing.T) {
	validator := NewValidator(true)

	cfg := &Config{
		Workspace: WorkspaceConfig{
			Path: "/tmp/test-workspace",
		},
		Agents: AgentsConfig{
			Defaults: AgentDefaults{
				Model:         "test-model",
				MaxIterations: 10,
				Temperature:   0.7,
				MaxTokens:     2048,
			},
		},
		Providers: ProvidersConfig{
			OpenAI: OpenAIProviderConfig{
				APIKey: "sk-test-valid-api-key-12345",
			},
		},
		Gateway: GatewayConfig{
			Port:         8080,
			ReadTimeout:  30,
			WriteTimeout: 30,
			WebSocket: WebSocketConfig{
				Host:         "localhost",
				Port:         8081,
				PingInterval: 30 * time.Second,
				PongTimeout:  30 * time.Second,
				ReadTimeout:  30 * time.Second,
				WriteTimeout: 30 * time.Second,
			},
		},
		Tools: ToolsConfig{
			Web: WebToolConfig{
				Timeout: 10,
			},
		},
		Memory: MemoryConfig{
			Backend: "builtin",
		},
	}

	if err := validator.Validate(cfg); err != nil {
		t.Errorf("expected valid config, got error: %v", err)
	}
}

func TestValidatorInvalidModel(t *testing.T) {
	validator := NewValidator(true)

	cfg := &Config{
		Agents: AgentsConfig{
			Defaults: AgentDefaults{
				Model: "", // Invalid
			},
		},
		Memory: MemoryConfig{
			Backend: "builtin",
		},
	}

	err := validator.Validate(cfg)
	if err == nil {
		t.Error("expected error for empty model")
	}
	if !errors.Is(err, errors.ErrCodeInvalidConfig) {
		t.Errorf("expected ErrCodeInvalidConfig, got: %v", errors.GetCode(err))
	}
}

func TestValidatorInvalidTemperature(t *testing.T) {
	validator := NewValidator(true)

	cfg := &Config{
		Agents: AgentsConfig{
			Defaults: AgentDefaults{
				Model:       "test-model",
				Temperature: 3.0, // Invalid > 2
			},
		},
		Providers: ProvidersConfig{
			OpenAI: OpenAIProviderConfig{
				APIKey: "sk-test-valid-api-key-12345",
			},
		},
		Memory: MemoryConfig{
			Backend: "builtin",
		},
	}

	err := validator.Validate(cfg)
	if err == nil {
		t.Error("expected error for invalid temperature")
	}
}

func TestValidatorInvalidAPIKey(t *testing.T) {
	validator := NewValidator(true)

	cfg := &Config{
		Agents: AgentsConfig{
			Defaults: AgentDefaults{
				Model:         "test-model",
				MaxIterations: 10,
				Temperature:   0.7,
				MaxTokens:     2048,
			},
		},
		Providers: ProvidersConfig{
			OpenAI: OpenAIProviderConfig{
				APIKey: "short", // Invalid
			},
		},
		Memory: MemoryConfig{
			Backend: "builtin",
		},
	}

	err := validator.Validate(cfg)
	if err == nil {
		t.Error("expected error for short API key")
	}
}

func TestValidatorMissingProvider(t *testing.T) {
	validator := NewValidator(true)

	cfg := &Config{
		Agents: AgentsConfig{
			Defaults: AgentDefaults{
				Model:         "test-model",
				MaxIterations: 10,
				Temperature:   0.7,
				MaxTokens:     2048,
			},
		},
		Providers: ProvidersConfig{
			// No provider configured
		},
		Memory: MemoryConfig{
			Backend: "builtin",
		},
	}

	err := validator.Validate(cfg)
	if err == nil {
		t.Error("expected error when no provider is configured")
	}
}

func TestValidateWeWorkWebhookMode(t *testing.T) {
	validator := NewValidator(true)

	err := validator.validateWeWork(&ChannelsConfig{
		WeWork: WeWorkChannelConfig{
			Enabled: true,
			Mode:    "webhook",
			CorpID:  "corp-id",
			AgentID: "agent-id",
			Secret:  "secret",
		},
	})
	if err != nil {
		t.Fatalf("expected webhook config to be valid, got %v", err)
	}
}

func TestValidateWeWorkWebSocketMode(t *testing.T) {
	validator := NewValidator(true)

	err := validator.validateWeWork(&ChannelsConfig{
		WeWork: WeWorkChannelConfig{
			Enabled:   true,
			Mode:      "websocket",
			BotID:     "bot-id",
			BotSecret: "bot-secret",
		},
	})
	if err != nil {
		t.Fatalf("expected websocket config to be valid, got %v", err)
	}
}

func TestValidateWeWorkWebSocketModeMissingSecret(t *testing.T) {
	validator := NewValidator(true)

	err := validator.validateWeWork(&ChannelsConfig{
		WeWork: WeWorkChannelConfig{
			Enabled: true,
			Mode:    "websocket",
			BotID:   "bot-id",
		},
	})
	if err == nil {
		t.Fatal("expected websocket config without bot_secret to be invalid")
	}
}

func TestValidateWeWorkAccountWebhookModeUsesAppSecret(t *testing.T) {
	validator := NewValidator(true)

	err := validator.validateWeWork(&ChannelsConfig{
		WeWork: WeWorkChannelConfig{
			Accounts: map[string]ChannelAccountConfig{
				"corp-a": {
					Enabled:   true,
					Mode:      "webhook",
					CorpID:    "corp-id",
					AgentID:   "agent-id",
					AppSecret: "secret-value",
				},
			},
		},
	})
	if err != nil {
		t.Fatalf("expected multi-account webhook config to be valid, got %v", err)
	}
}

func TestValidateWeWorkAccountWebhookModeMissingSecret(t *testing.T) {
	validator := NewValidator(true)

	err := validator.validateWeWork(&ChannelsConfig{
		WeWork: WeWorkChannelConfig{
			Accounts: map[string]ChannelAccountConfig{
				"corp-a": {
					Enabled: true,
					Mode:    "webhook",
					CorpID:  "corp-id",
					AgentID: "agent-id",
				},
			},
		},
	})
	if err == nil {
		t.Fatal("expected multi-account webhook config without secret to be invalid")
	}
}

func TestValidateWhatsAppRejectsNonAbsoluteBridgeURL(t *testing.T) {
	validator := NewValidator(true)

	err := validator.validateWhatsApp(&ChannelsConfig{
		WhatsApp: WhatsAppChannelConfig{
			Enabled:   true,
			BridgeURL: "localhost:3000",
		},
	})
	if err == nil {
		t.Fatal("expected whatsapp bridge_url without scheme to be invalid")
	}
}
