package memory

import (
	"sync"

	"github.com/smallnest/goclaw/internal/core/config"
)

// SearchManagerPool caches memory search managers by workspace.
type SearchManagerPool struct {
	mu       sync.Mutex
	cfg      config.MemoryConfig
	managers map[string]MemorySearchManager
}

// NewSearchManagerPool creates a new memory search manager pool.
func NewSearchManagerPool(cfg config.MemoryConfig) *SearchManagerPool {
	return &SearchManagerPool{
		cfg:      cfg,
		managers: make(map[string]MemorySearchManager),
	}
}

// Get returns a cached search manager for the workspace or creates one.
func (p *SearchManagerPool) Get(workspace string) (MemorySearchManager, error) {
	p.mu.Lock()
	defer p.mu.Unlock()

	if mgr, ok := p.managers[workspace]; ok {
		return mgr, nil
	}

	mgr, err := GetMemorySearchManager(p.cfg, workspace)
	if err != nil {
		return nil, err
	}
	p.managers[workspace] = mgr
	return mgr, nil
}

// Close closes all cached search managers.
func (p *SearchManagerPool) Close() error {
	p.mu.Lock()
	defer p.mu.Unlock()

	var firstErr error
	for key, mgr := range p.managers {
		if mgr == nil {
			delete(p.managers, key)
			continue
		}
		if err := mgr.Close(); err != nil && firstErr == nil {
			firstErr = err
		}
		delete(p.managers, key)
	}
	return firstErr
}
