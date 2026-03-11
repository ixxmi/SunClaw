package ops

import "sync"

// Guard provides in-process mutual exclusion and request_id idempotent cache.
type Guard struct {
	mu         sync.Mutex
	inProgress bool
	cache      map[string]Response
}

func NewGuard() *Guard {
	return &Guard{cache: make(map[string]Response)}
}

// Begin acquires operation lock. If requestID result is cached, returns it directly.
func (g *Guard) Begin(requestID string) (*Response, error) {
	if g == nil {
		return nil, nil
	}
	g.mu.Lock()
	defer g.mu.Unlock()

	if requestID != "" {
		if resp, ok := g.cache[requestID]; ok {
			cp := resp
			return &cp, nil
		}
	}
	if g.inProgress {
		return nil, ErrOperationInProgress
	}
	g.inProgress = true
	return nil, nil
}

// End releases lock and optionally caches response by requestID.
func (g *Guard) End(requestID string, resp Response) {
	if g == nil {
		return
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	g.inProgress = false
	if requestID != "" {
		g.cache[requestID] = resp
	}
}
