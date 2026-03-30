package sandbox

import (
	"context"
	"fmt"
	"log"
	"sync"
	"time"
)

// PoolConfig 定义容器预热池配置。
type PoolConfig struct {
	Image           string
	WarmTarget      int
	AcquireTimeout  time.Duration
	KeepAlive       time.Duration
	HealthCheck     time.Duration
	Factory         func(context.Context, string) (string, error)
	Destroy         func(context.Context, string) error
	HealthCheckFunc func(context.Context, string) error
}

// containerEntry 表示池中一个容器实例的状态。
type containerEntry struct {
	id         string
	createdAt  time.Time
	lastUsedAt time.Time
	inUse      bool
}

// ContainerPool 管理预热容器资源。
type ContainerPool struct {
	mu             sync.Mutex
	cfg            PoolConfig
	entries        map[string]*containerEntry
	idle           []string
	closed         bool
	healthStopOnce sync.Once
}

// NewContainerPool 创建容器预热池。
func NewContainerPool(image string, warmTarget int) *ContainerPool {
	cfg := PoolConfig{
		Image:          image,
		WarmTarget:     warmTarget,
		AcquireTimeout: 30 * time.Second,
		KeepAlive:      5 * time.Minute,
		HealthCheck:    30 * time.Second,
	}
	return NewContainerPoolWithConfig(cfg)
}

// NewContainerPoolWithConfig 使用完整配置创建容器预热池。
func NewContainerPoolWithConfig(cfg PoolConfig) *ContainerPool {
	if cfg.WarmTarget < 0 {
		cfg.WarmTarget = 0
	}
	if cfg.AcquireTimeout <= 0 {
		cfg.AcquireTimeout = 30 * time.Second
	}
	if cfg.KeepAlive <= 0 {
		cfg.KeepAlive = 5 * time.Minute
	}
	if cfg.HealthCheck <= 0 {
		cfg.HealthCheck = 30 * time.Second
	}
	return &ContainerPool{
		cfg:     cfg,
		entries: make(map[string]*containerEntry),
		idle:    make([]string, 0, cfg.WarmTarget),
	}
}

// Warmup 执行预热，补足目标数量的空闲容器。
func (p *ContainerPool) Warmup(ctx context.Context) error {
	if p == nil {
		return fmt.Errorf("container pool is nil")
	}
	if p.cfg.Image == "" {
		return fmt.Errorf("pool image is empty")
	}

	p.startBackgroundHealthCheck()

	for {
		p.mu.Lock()
		if p.closed {
			p.mu.Unlock()
			return fmt.Errorf("container pool is closed")
		}
		need := p.cfg.WarmTarget - len(p.idle)
		p.mu.Unlock()

		if need <= 0 {
			return nil
		}

		id, err := p.createContainer(ctx)
		if err != nil {
			return err
		}

		p.mu.Lock()
		if p.closed {
			p.mu.Unlock()
			_ = p.destroyContainer(context.Background(), id)
			return fmt.Errorf("container pool is closed")
		}
		if _, exists := p.entries[id]; exists {
			p.mu.Unlock()
			continue
		}
		now := time.Now()
		p.entries[id] = &containerEntry{id: id, createdAt: now, lastUsedAt: now}
		p.idle = append(p.idle, id)
		p.mu.Unlock()
		log.Printf("sandbox: warmed container %s for image %s", id, p.cfg.Image)
	}
}

// Acquire 获取一个可用容器标识。
func (p *ContainerPool) Acquire(ctx context.Context) (string, error) {
	if p == nil {
		return "", fmt.Errorf("container pool is nil")
	}
	if p.cfg.Image == "" {
		return "", fmt.Errorf("pool image is empty")
	}
	if ctx == nil {
		ctx = context.Background()
	}
	if p.cfg.AcquireTimeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, p.cfg.AcquireTimeout)
		defer cancel()
	}

	for {
		p.mu.Lock()
		if p.closed {
			p.mu.Unlock()
			return "", fmt.Errorf("container pool is closed")
		}
		if n := len(p.idle); n > 0 {
			id := p.idle[n-1]
			p.idle = p.idle[:n-1]
			entry := p.entries[id]
			if entry == nil {
				p.mu.Unlock()
				continue
			}
			entry.inUse = true
			entry.lastUsedAt = time.Now()
			p.mu.Unlock()

			if err := p.healthCheck(ctx, id); err != nil {
				log.Printf("sandbox: health check failed for container %s: %v", id, err)
				p.removeEntry(id)
				_ = p.destroyContainer(context.Background(), id)
				continue
			}
			return id, nil
		}
		p.mu.Unlock()

		id, err := p.createContainer(ctx)
		if err != nil {
			return "", err
		}
		if err := p.healthCheck(ctx, id); err != nil {
			log.Printf("sandbox: health check failed for new container %s: %v", id, err)
			_ = p.destroyContainer(context.Background(), id)
			return "", err
		}

		p.mu.Lock()
		if p.closed {
			p.mu.Unlock()
			_ = p.destroyContainer(context.Background(), id)
			return "", fmt.Errorf("container pool is closed")
		}
		now := time.Now()
		p.entries[id] = &containerEntry{id: id, createdAt: now, lastUsedAt: now, inUse: true}
		p.mu.Unlock()
		log.Printf("sandbox: acquired cold container %s for image %s", id, p.cfg.Image)
		return id, nil
	}
}

// Release 释放一个容器回池中；若超过保活窗口则销毁。
func (p *ContainerPool) Release(id string) {
	if p == nil || id == "" {
		return
	}

	p.mu.Lock()
	if p.closed {
		p.mu.Unlock()
		_ = p.destroyContainer(context.Background(), id)
		return
	}
	entry, ok := p.entries[id]
	if !ok {
		p.mu.Unlock()
		return
	}
	entry.inUse = false
	entry.lastUsedAt = time.Now()

	shouldDestroy := p.cfg.KeepAlive > 0 && time.Since(entry.createdAt) > p.cfg.KeepAlive && len(p.idle) >= p.cfg.WarmTarget
	if shouldDestroy {
		delete(p.entries, id)
		p.mu.Unlock()
		if err := p.destroyContainer(context.Background(), id); err != nil {
			log.Printf("sandbox: destroy expired container %s failed: %v", id, err)
		}
		return
	}

	for _, idleID := range p.idle {
		if idleID == id {
			p.mu.Unlock()
			return
		}
	}
	p.idle = append(p.idle, id)
	p.mu.Unlock()
}

// Close 关闭池并清理全部容器。
func (p *ContainerPool) Close(ctx context.Context) error {
	if p == nil {
		return nil
	}
	p.mu.Lock()
	if p.closed {
		p.mu.Unlock()
		return nil
	}
	p.closed = true
	ids := make([]string, 0, len(p.entries))
	for id := range p.entries {
		ids = append(ids, id)
	}
	p.entries = make(map[string]*containerEntry)
	p.idle = nil
	p.mu.Unlock()

	var firstErr error
	for _, id := range ids {
		if err := p.destroyContainer(ctx, id); err != nil && firstErr == nil {
			firstErr = err
		}
	}
	return firstErr
}

// Stats 返回当前池状态。
func (p *ContainerPool) Stats() (warmed int, target int) {
	if p == nil {
		return 0, 0
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	return len(p.idle), p.cfg.WarmTarget
}

func (p *ContainerPool) startBackgroundHealthCheck() {
	if p == nil || p.cfg.HealthCheck <= 0 {
		return
	}
	p.healthStopOnce.Do(func() {
		go func() {
			ticker := time.NewTicker(p.cfg.HealthCheck)
			defer ticker.Stop()
			for range ticker.C {
				p.mu.Lock()
				if p.closed {
					p.mu.Unlock()
					return
				}
				idleIDs := append([]string(nil), p.idle...)
				p.mu.Unlock()

				for _, id := range idleIDs {
					ctx, cancel := context.WithTimeout(context.Background(), p.cfg.HealthCheck)
					err := p.healthCheck(ctx, id)
					cancel()
					if err != nil {
						log.Printf("sandbox: periodic health check failed for container %s: %v", id, err)
						p.removeEntry(id)
						_ = p.destroyContainer(context.Background(), id)
					}
				}

				warmCtx, cancel := context.WithTimeout(context.Background(), p.cfg.AcquireTimeout)
				_ = p.Warmup(warmCtx)
				cancel()
			}
		}()
	})
}

func (p *ContainerPool) createContainer(ctx context.Context) (string, error) {
	if p.cfg.Factory == nil {
		return p.cfg.Image, nil
	}
	id, err := p.cfg.Factory(ctx, p.cfg.Image)
	if err != nil {
		return "", fmt.Errorf("create container failed: %w", err)
	}
	if id == "" {
		return "", fmt.Errorf("create container failed: empty container id")
	}
	return id, nil
}

func (p *ContainerPool) destroyContainer(ctx context.Context, id string) error {
	if id == "" {
		return nil
	}
	if p.cfg.Destroy == nil {
		return nil
	}
	if err := p.cfg.Destroy(ctx, id); err != nil {
		return fmt.Errorf("destroy container %s failed: %w", id, err)
	}
	return nil
}

func (p *ContainerPool) healthCheck(ctx context.Context, id string) error {
	if id == "" {
		return fmt.Errorf("empty container id")
	}
	if p.cfg.HealthCheckFunc == nil {
		return nil
	}
	if err := p.cfg.HealthCheckFunc(ctx, id); err != nil {
		return fmt.Errorf("health check failed: %w", err)
	}
	return nil
}

func (p *ContainerPool) removeEntry(id string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	delete(p.entries, id)
	if len(p.idle) == 0 {
		return
	}
	filtered := p.idle[:0]
	for _, idleID := range p.idle {
		if idleID != id {
			filtered = append(filtered, idleID)
		}
	}
	p.idle = filtered
}
