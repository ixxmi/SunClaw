package providers

import (
	"fmt"
	"strings"
	"time"

	"github.com/smallnest/goclaw/internal/core/config"
	"github.com/smallnest/goclaw/internal/platform/errors"
)

// ProviderType 提供商类型
type ProviderType string

const (
	ProviderTypeOpenAI     ProviderType = "openai"
	ProviderTypeAnthropic  ProviderType = "anthropic"
	ProviderTypeOpenRouter ProviderType = "openrouter"
)

// NewProvider 创建提供商（支持故障转移和配置轮换）
func NewProvider(cfg *config.Config) (Provider, error) {
	// 如果启用了故障转移且配置了多个配置，使用轮换提供商
	if cfg.Providers.Failover.Enabled && len(cfg.Providers.Profiles) > 0 {
		return NewRotationProviderFromConfig(cfg)
	}

	// 否则使用单一提供商
	return NewSimpleProvider(cfg)
}

// NewSimpleProvider 创建单一提供商
func NewSimpleProvider(cfg *config.Config) (Provider, error) {
	// 确定使用哪个提供商
	providerType, model, err := determineProvider(cfg)
	if err != nil {
		return nil, err
	}

	switch providerType {
	case ProviderTypeOpenAI:
		timeout := time.Duration(cfg.Providers.OpenAI.Timeout) * time.Second
		return NewOpenAIProviderWithTimeout(cfg.Providers.OpenAI.APIKey, cfg.Providers.OpenAI.BaseURL, model, cfg.Agents.Defaults.MaxTokens, timeout)
	case ProviderTypeAnthropic:
		timeout := time.Duration(cfg.Providers.Anthropic.Timeout) * time.Second
		return NewAnthropicProviderWithTimeout(cfg.Providers.Anthropic.APIKey, cfg.Providers.Anthropic.BaseURL, model, cfg.Agents.Defaults.MaxTokens, timeout)
	case ProviderTypeOpenRouter:
		timeout := time.Duration(cfg.Providers.OpenRouter.Timeout) * time.Second
		return NewOpenRouterProviderWithTimeout(cfg.Providers.OpenRouter.APIKey, cfg.Providers.OpenRouter.BaseURL, model, cfg.Agents.Defaults.MaxTokens, timeout)
	default:
		return nil, fmt.Errorf("unsupported provider type: %s", providerType)
	}
}

// NewRotationProviderFromConfig 从配置创建轮换提供商
func NewRotationProviderFromConfig(cfg *config.Config) (Provider, error) {
	// 创建错误分类器
	errorClassifier := errors.NewSimpleErrorClassifier()

	// 确定轮换策略
	strategy := RotationStrategy(cfg.Providers.Failover.Strategy)
	if strategy == "" {
		strategy = RotationStrategyRoundRobin
	}

	// 创建轮换提供商
	rotation := NewRotationProvider(
		strategy,
		cfg.Providers.Failover.DefaultCooldown,
		errorClassifier,
	)

	// 添加所有配置
	for _, profileCfg := range cfg.Providers.Profiles {
		prov, err := createProviderByType(profileCfg.Provider, profileCfg.APIKey, profileCfg.BaseURL, cfg.Agents.Defaults.Model, cfg.Agents.Defaults.MaxTokens)
		if err != nil {
			return nil, fmt.Errorf("failed to create provider for profile %s: %w", profileCfg.Name, err)
		}

		priority := profileCfg.Priority
		if priority == 0 {
			priority = 1
		}

		rotation.AddProfile(profileCfg.Name, prov, profileCfg.APIKey, priority)
	}

	// 如果只有一个配置，返回第一个提供商
	if len(cfg.Providers.Profiles) == 1 {
		p := cfg.Providers.Profiles[0]
		prov, err := createProviderByType(p.Provider, p.APIKey, p.BaseURL, cfg.Agents.Defaults.Model, cfg.Agents.Defaults.MaxTokens)
		if err != nil {
			return nil, err
		}
		return prov, nil
	}

	return rotation, nil
}

// createProviderByType 根据类型创建提供商
func createProviderByType(providerType, apiKey, baseURL, model string, maxTokens int) (Provider, error) {
	switch ProviderType(providerType) {
	case ProviderTypeOpenAI:
		return NewOpenAIProvider(apiKey, baseURL, model, maxTokens)
	case ProviderTypeAnthropic:
		return NewAnthropicProvider(apiKey, baseURL, model, maxTokens)
	case ProviderTypeOpenRouter:
		return NewOpenRouterProvider(apiKey, baseURL, model, maxTokens)
	default:
		return nil, fmt.Errorf("unsupported provider type: %s", providerType)
	}
}

// NewProviderFromProfile 根据 profile 名称从配置中找到对应 profile 并创建 provider。
// agentModel 为 agent 级别的模型覆盖，优先级：agentModel > profile.Model > globalDefaultModel。
func NewProviderFromProfile(cfg *config.Config, profileName string, agentModel string, maxTokens int) (Provider, string, error) {
	// 在 profiles 中查找
	for _, p := range cfg.Providers.Profiles {
		if p.Name != profileName {
			continue
		}
		// 确定最终使用的模型
		model := agentModel
		if model == "" {
			model = p.Model
		}
		if model == "" {
			model = cfg.Agents.Defaults.Model
		}
		prov, err := createProviderByType(p.Provider, p.APIKey, p.BaseURL, model, maxTokens)
		if err != nil {
			return nil, "", fmt.Errorf("failed to create provider for profile %q: %w", profileName, err)
		}
		return prov, model, nil
	}

	// profile 不存在时，尝试按名称匹配内置 provider
	model := agentModel
	if model == "" {
		model = cfg.Agents.Defaults.Model
	}
	timeout := time.Duration(600) * time.Second

	switch strings.ToLower(profileName) {
	case "openai":
		if cfg.Providers.OpenAI.APIKey == "" {
			return nil, "", fmt.Errorf("provider profile %q not found and openai api_key not configured", profileName)
		}
		if cfg.Providers.OpenAI.Timeout > 0 {
			timeout = time.Duration(cfg.Providers.OpenAI.Timeout) * time.Second
		}
		prov, err := NewOpenAIProviderWithTimeout(cfg.Providers.OpenAI.APIKey, cfg.Providers.OpenAI.BaseURL, model, maxTokens, timeout)
		return prov, model, err
	case "anthropic":
		if cfg.Providers.Anthropic.APIKey == "" {
			return nil, "", fmt.Errorf("provider profile %q not found and anthropic api_key not configured", profileName)
		}
		if cfg.Providers.Anthropic.Timeout > 0 {
			timeout = time.Duration(cfg.Providers.Anthropic.Timeout) * time.Second
		}
		prov, err := NewAnthropicProviderWithTimeout(cfg.Providers.Anthropic.APIKey, cfg.Providers.Anthropic.BaseURL, model, maxTokens, timeout)
		return prov, model, err
	case "openrouter":
		if cfg.Providers.OpenRouter.APIKey == "" {
			return nil, "", fmt.Errorf("provider profile %q not found and openrouter api_key not configured", profileName)
		}
		if cfg.Providers.OpenRouter.Timeout > 0 {
			timeout = time.Duration(cfg.Providers.OpenRouter.Timeout) * time.Second
		}
		prov, err := NewOpenRouterProviderWithTimeout(cfg.Providers.OpenRouter.APIKey, cfg.Providers.OpenRouter.BaseURL, model, maxTokens, timeout)
		return prov, model, err
	case "gemini":
		// gemini 中转站使用 OpenAI 兼容接口，fallback 时复用 openai 节配置
		if cfg.Providers.OpenAI.APIKey == "" {
			return nil, "", fmt.Errorf("provider profile %q not found; add a profile named \"gemini\" in providers.profiles", profileName)
		}
		prov, err := NewOpenAIProviderWithTimeout(cfg.Providers.OpenAI.APIKey, cfg.Providers.OpenAI.BaseURL, model, maxTokens, timeout)
		return prov, model, err
	}

	return nil, "", fmt.Errorf("provider profile %q not found", profileName)
}

// determineProvider 确定提供商
func determineProvider(cfg *config.Config) (ProviderType, string, error) {
	model := cfg.Agents.Defaults.Model

	// 检查模型名称前缀
	if strings.HasPrefix(model, "openrouter:") {
		return ProviderTypeOpenRouter, strings.TrimPrefix(model, "openrouter:"), nil
	}

	if strings.HasPrefix(model, "anthropic:") || strings.HasPrefix(model, "claude-") {
		return ProviderTypeAnthropic, model, nil
	}

	if strings.HasPrefix(model, "openai:") || strings.HasPrefix(model, "gpt-") {
		return ProviderTypeOpenAI, model, nil
	}

	// 根据可用的 API key 决定
	if cfg.Providers.OpenRouter.APIKey != "" {
		return ProviderTypeOpenRouter, model, nil
	}

	if cfg.Providers.Anthropic.APIKey != "" {
		return ProviderTypeAnthropic, model, nil
	}

	if cfg.Providers.OpenAI.APIKey != "" {
		return ProviderTypeOpenAI, model, nil
	}

	return "", "", fmt.Errorf("no LLM provider API key configured")
}
