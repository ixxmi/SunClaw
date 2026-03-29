package providers

import (
	"context"
	"fmt"
	"sort"
	"sync"
	"time"

	"github.com/smallnest/goclaw/internal/platform/errors"
)

// RotationStrategy 轮换策略
type RotationStrategy string

const (
	// RotationStrategyRoundRobin 轮询策略
	RotationStrategyRoundRobin RotationStrategy = "round_robin"
	// RotationStrategyLeastUsed 最少使用策略
	RotationStrategyLeastUsed RotationStrategy = "least_used"
	// RotationStrategyRandom 随机策略
	RotationStrategyRandom RotationStrategy = "random"
)

// ProviderProfile 提供商配置
type ProviderProfile struct {
	Name          string
	Provider      Provider
	APIKey        string
	Priority      int
	CooldownUntil time.Time
	RequestCount  int64
	mu            sync.Mutex
}

// RotationProvider 支持多配置轮换的提供商
type RotationProvider struct {
	profiles        map[string]*ProviderProfile
	strategy        RotationStrategy
	currentIndex    int
	errorClassifier errors.ErrorClassifier
	defaultCooldown time.Duration
	mu              sync.RWMutex
}

// NewRotationProvider 创建轮换提供商
func NewRotationProvider(strategy RotationStrategy, defaultCooldown time.Duration, errorClassifier errors.ErrorClassifier) *RotationProvider {
	return &RotationProvider{
		profiles:        make(map[string]*ProviderProfile),
		strategy:        strategy,
		currentIndex:    0,
		errorClassifier: errorClassifier,
		defaultCooldown: defaultCooldown,
	}
}

// AddProfile 添加配置
func (p *RotationProvider) AddProfile(name string, provider Provider, apiKey string, priority int) {
	p.mu.Lock()
	defer p.mu.Unlock()

	p.profiles[name] = &ProviderProfile{
		Name:     name,
		Provider: provider,
		APIKey:   apiKey,
		Priority: priority,
	}
}

// RemoveProfile 移除配置
func (p *RotationProvider) RemoveProfile(name string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	delete(p.profiles, name)
}

// GetProfile 获取配置
func (p *RotationProvider) GetProfile(name string) (*ProviderProfile, bool) {
	p.mu.RLock()
	defer p.mu.RUnlock()
	profile, ok := p.profiles[name]
	return profile, ok
}

// Chat 聊天（带配置轮换）
func (p *RotationProvider) Chat(ctx context.Context, messages []Message, tools []ToolDefinition, options ...ChatOption) (*Response, error) {
	candidates := p.getCandidateProfiles()
	if len(candidates) == 0 {
		return nil, fmt.Errorf("no available provider profile")
	}

	var lastErr error
	for _, profile := range candidates {
		response, err := profile.Provider.Chat(ctx, messages, tools, options...)
		if err == nil {
			profile.mu.Lock()
			profile.RequestCount++
			profile.mu.Unlock()
			return response, nil
		}

		lastErr = err
		reason := p.errorClassifier.ClassifyError(err)
		if p.shouldSetCooldown(reason) {
			p.setCooldown(profile.Name)
			continue
		}

		return nil, err
	}

	return nil, lastErr
}

// ChatWithTools 聊天（带工具，支持配置轮换）
func (p *RotationProvider) ChatWithTools(ctx context.Context, messages []Message, tools []ToolDefinition, options ...ChatOption) (*Response, error) {
	return p.Chat(ctx, messages, tools, options...)
}

// getCandidateProfiles 获取当前请求可尝试的 provider 列表。
// 返回顺序由轮换策略决定；若前面的配置因为可回退错误失败，当前请求会继续尝试后续配置。
func (p *RotationProvider) getCandidateProfiles() []*ProviderProfile {
	p.mu.Lock()
	defer p.mu.Unlock()

	now := time.Now()
	available := make([]*ProviderProfile, 0, len(p.profiles))

	// 筛选可用的配置（不在冷却期）
	for _, profile := range p.profiles {
		profile.mu.Lock()
		if profile.CooldownUntil.IsZero() || now.After(profile.CooldownUntil) {
			available = append(available, profile)
		}
		profile.mu.Unlock()
	}

	if len(available) == 0 {
		return nil
	}

	switch p.strategy {
	case RotationStrategyRoundRobin:
		return p.orderRoundRobin(available)
	case RotationStrategyLeastUsed:
		return p.orderLeastUsed(available)
	case RotationStrategyRandom:
		return p.orderRandom(available)
	default:
		return available
	}
}

// orderRoundRobin 轮询排列。
func (p *RotationProvider) orderRoundRobin(available []*ProviderProfile) []*ProviderProfile {
	if len(available) == 0 {
		return nil
	}

	// Sort by name to ensure consistent ordering
	sort.Slice(available, func(i, j int) bool {
		return available[i].Name < available[j].Name
	})

	start := p.currentIndex % len(available)
	p.currentIndex++

	ordered := append([]*ProviderProfile{}, available[start:]...)
	return append(ordered, available[:start]...)
}

// orderLeastUsed 按请求数升序排列。
func (p *RotationProvider) orderLeastUsed(available []*ProviderProfile) []*ProviderProfile {
	if len(available) == 0 {
		return nil
	}

	sort.Slice(available, func(i, j int) bool {
		available[i].mu.Lock()
		left := available[i].RequestCount
		available[i].mu.Unlock()

		available[j].mu.Lock()
		right := available[j].RequestCount
		available[j].mu.Unlock()

		if left == right {
			return available[i].Name < available[j].Name
		}
		return left < right
	})

	return available
}

// orderRandom 使用时间种子做简单旋转。
func (p *RotationProvider) orderRandom(available []*ProviderProfile) []*ProviderProfile {
	if len(available) == 0 {
		return nil
	}

	start := int(time.Now().UnixNano() % int64(len(available)))
	ordered := append([]*ProviderProfile{}, available[start:]...)
	return append(ordered, available[:start]...)
}

// setCooldown 设置冷却时间
func (p *RotationProvider) setCooldown(profileName string) {
	p.mu.RLock()
	profile, ok := p.profiles[profileName]
	p.mu.RUnlock()

	if !ok {
		return
	}

	profile.mu.Lock()
	profile.CooldownUntil = time.Now().Add(p.defaultCooldown)
	profile.mu.Unlock()
}

// shouldSetCooldown 判断是否应该设置冷却
func (p *RotationProvider) shouldSetCooldown(reason errors.FailoverReason) bool {
	switch reason {
	case errors.FailoverReasonAuth, errors.FailoverReasonRateLimit, errors.FailoverReasonBilling, errors.FailoverReasonTimeout:
		return true
	default:
		return false
	}
}

// ResetCooldown 重置所有配置的冷却时间
func (p *RotationProvider) ResetCooldown() {
	p.mu.RLock()
	defer p.mu.RUnlock()

	for _, profile := range p.profiles {
		profile.mu.Lock()
		profile.CooldownUntil = time.Time{}
		profile.mu.Unlock()
	}
}

// Close 关闭所有提供商
func (p *RotationProvider) Close() error {
	p.mu.Lock()
	defer p.mu.Unlock()

	var errs []error
	for _, profile := range p.profiles {
		if err := profile.Provider.Close(); err != nil {
			errs = append(errs, fmt.Errorf("profile %s close error: %w", profile.Name, err))
		}
	}

	if len(errs) > 0 {
		return fmt.Errorf("close errors: %v", errs)
	}

	return nil
}

// ListProfiles 列出所有配置
func (p *RotationProvider) ListProfiles() []string {
	p.mu.RLock()
	defer p.mu.RUnlock()

	names := make([]string, 0, len(p.profiles))
	for name := range p.profiles {
		names = append(names, name)
	}
	return names
}

// GetProfileStatus 获取配置状态
func (p *RotationProvider) GetProfileStatus(name string) (map[string]interface{}, error) {
	p.mu.RLock()
	profile, ok := p.profiles[name]
	p.mu.RUnlock()

	if !ok {
		return nil, fmt.Errorf("profile not found: %s", name)
	}

	profile.mu.Lock()
	defer profile.mu.Unlock()

	now := time.Now()
	isInCooldown := !profile.CooldownUntil.IsZero() && now.Before(profile.CooldownUntil)

	return map[string]interface{}{
		"name":           profile.Name,
		"priority":       profile.Priority,
		"request_count":  profile.RequestCount,
		"in_cooldown":    isInCooldown,
		"cooldown_until": profile.CooldownUntil,
	}, nil
}
