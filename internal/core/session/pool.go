package session

import "sync"

// ManagerPool caches session managers by storage directory.
type ManagerPool struct {
	mu       sync.Mutex
	managers map[string]*Manager
}

// NewManagerPool creates a new session manager pool.
func NewManagerPool() *ManagerPool {
	return &ManagerPool{
		managers: make(map[string]*Manager),
	}
}

// Get returns a cached manager for baseDir or creates a new one.
func (p *ManagerPool) Get(baseDir string) (*Manager, error) {
	p.mu.Lock()
	defer p.mu.Unlock()

	if mgr, ok := p.managers[baseDir]; ok {
		return mgr, nil
	}

	mgr, err := NewManager(baseDir)
	if err != nil {
		return nil, err
	}
	p.managers[baseDir] = mgr
	return mgr, nil
}
