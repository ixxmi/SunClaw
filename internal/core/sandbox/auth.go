package sandbox

import (
	"context"
	"sync"
)

// contextAuthKey 用于在 context 中保存授权状态。
type contextAuthKey struct{}

// Authorizer 定义本地执行授权管理接口。
type Authorizer interface {
	Authorize(ctx context.Context) context.Context
	Revoke(ctx context.Context) context.Context
	IsAuthorized(ctx context.Context) bool
}

// AuthManager 提供线程安全的授权状态管理。
type AuthManager struct {
	mu               sync.RWMutex
	globalAuthorized bool
}

// NewAuthManager 创建授权管理器。
func NewAuthManager() *AuthManager {
	return &AuthManager{}
}

// Authorize 将授权状态写入 context，并标记全局已授权。
func (a *AuthManager) Authorize(ctx context.Context) context.Context {
	a.mu.Lock()
	a.globalAuthorized = true
	a.mu.Unlock()
	return context.WithValue(ctx, contextAuthKey{}, true)
}

// Revoke 撤销全局授权，并在返回的 context 中清除授权标记。
func (a *AuthManager) Revoke(ctx context.Context) context.Context {
	a.mu.Lock()
	a.globalAuthorized = false
	a.mu.Unlock()
	return context.WithValue(ctx, contextAuthKey{}, false)
}

// IsAuthorized 判断 context 或全局状态中是否已授权。
func (a *AuthManager) IsAuthorized(ctx context.Context) bool {
	if ctx != nil {
		if v, ok := ctx.Value(contextAuthKey{}).(bool); ok {
			return v
		}
	}
	a.mu.RLock()
	defer a.mu.RUnlock()
	return a.globalAuthorized
}
