package gateway

import (
	"encoding/json"
	"fmt"
	"net/http"

	"github.com/smallnest/goclaw/internal/core/agent"
)

func (s *Server) handleShrimpBrainAPI(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		snapshot := s.buildShrimpBrainSnapshot()
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_ = json.NewEncoder(w).Encode(snapshot)
	case http.MethodDelete:
		runID := r.URL.Query().Get("runId")
		if runID == "" {
			http.Error(w, "runId is required", http.StatusBadRequest)
			return
		}
		s.mu.RLock()
		tracker := s.shrimpBrain
		s.mu.RUnlock()
		if tracker == nil {
			http.Error(w, "shrimp brain is unavailable", http.StatusServiceUnavailable)
			return
		}
		if !tracker.DeleteRun(runID) {
			http.Error(w, "run not found", http.StatusNotFound)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_ = json.NewEncoder(w).Encode(map[string]any{
			"deleted": true,
			"runId":   runID,
		})
	default:
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
	}
}

func (s *Server) handleShrimpBrainStream(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "Streaming is unsupported", http.StatusInternalServerError)
		return
	}

	s.mu.RLock()
	tracker := s.shrimpBrain
	s.mu.RUnlock()
	if tracker == nil {
		w.Header().Set("Content-Type", "text/event-stream")
		w.Header().Set("Cache-Control", "no-cache")
		w.Header().Set("Connection", "keep-alive")
		_, _ = fmt.Fprintf(w, "data: %s\n\n", mustMarshalShrimpBrain(agent.EmptyShrimpBrainSnapshot("当前运行路径未挂载 AgentManager，虾脑观测不可用。")))
		flusher.Flush()
		return
	}

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")

	updates := tracker.Subscribe()
	defer tracker.Unsubscribe(updates)

	for {
		select {
		case <-r.Context().Done():
			return
		case snapshot, ok := <-updates:
			if !ok {
				return
			}
			_, _ = fmt.Fprintf(w, "data: %s\n\n", mustMarshalShrimpBrain(snapshot))
			flusher.Flush()
		}
	}
}

func mustMarshalShrimpBrain(snapshot agent.ShrimpBrainSnapshot) string {
	data, err := json.Marshal(snapshot)
	if err != nil {
		fallback, _ := json.Marshal(agent.EmptyShrimpBrainSnapshot("虾脑快照序列化失败。"))
		return string(fallback)
	}
	return string(data)
}
