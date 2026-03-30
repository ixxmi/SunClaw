package agent

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestSessionAgentRouterLoadFromDisk_PrunesLegacyKeys(t *testing.T) {
	tempDir := t.TempDir()
	path := filepath.Join(tempDir, "session_agent_routes.json")
	structuredKey := "tenant:default:channel:telegram:account:default:sender:user-1:chat:chat-1"
	state := map[string]string{
		"telegram:default:chat-1": "legacy-agent",
		structuredKey:             "reviewer",
	}
	data, err := json.Marshal(state)
	if err != nil {
		t.Fatalf("marshal state: %v", err)
	}
	if err := os.WriteFile(path, data, 0o644); err != nil {
		t.Fatalf("write state: %v", err)
	}

	router := NewSessionAgentRouter(tempDir)
	if got := router.GetAgentID("telegram:default:chat-1"); got != "" {
		t.Fatalf("expected legacy key to be pruned, got %q", got)
	}
	if got := router.GetAgentID(structuredKey); got != "reviewer" {
		t.Fatalf("expected structured key to remain, got %q", got)
	}
}
