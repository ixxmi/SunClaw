package agent

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestSessionContextRouterRename_SuccessAndActiveAliasUpdated(t *testing.T) {
	router := NewSessionContextRouter("")
	baseSessionKey := "telegram:default:chat-1"

	resolved, err := router.Switch(baseSessionKey, "task-a")
	if err != nil {
		t.Fatalf("switch task-a: %v", err)
	}
	if _, err := router.Rename(baseSessionKey, "task-a", "task-b"); err != nil {
		t.Fatalf("expected rename success, got error: %v", err)
	}

	entries := router.List(baseSessionKey)
	foundOld := false
	foundNew := false
	newActive := false
	newSessionKey := ""
	for _, entry := range entries {
		switch entry.Alias {
		case "task-a":
			foundOld = true
		case "task-b":
			foundNew = true
			newActive = entry.IsActive
			newSessionKey = entry.SessionKey
		}
	}

	if foundOld {
		t.Fatalf("old alias should be removed after rename")
	}
	if !foundNew {
		t.Fatalf("new alias should exist after rename")
	}
	if !newActive {
		t.Fatalf("renamed active alias should remain active")
	}
	if newSessionKey != resolved {
		t.Fatalf("expected session key to remain %q, got %q", resolved, newSessionKey)
	}
	if got := router.CurrentAlias(baseSessionKey); got != "task-b" {
		t.Fatalf("expected active alias to update to task-b, got %q", got)
	}
	if got := router.Resolve(baseSessionKey); got != resolved {
		t.Fatalf("expected resolved session key %q, got %q", resolved, got)
	}
}

func TestSessionContextRouterRename_DefaultNotAllowed(t *testing.T) {
	router := NewSessionContextRouter("")
	baseSessionKey := "telegram:default:chat-1"

	if _, err := router.Rename(baseSessionKey, "default", "task-b"); err == nil {
		t.Fatalf("expected renaming default alias to fail")
	}
}

func TestSessionContextRouterRename_NewAliasAlreadyExists(t *testing.T) {
	router := NewSessionContextRouter("")
	baseSessionKey := "telegram:default:chat-1"

	if _, err := router.Switch(baseSessionKey, "task-a"); err != nil {
		t.Fatalf("switch task-a: %v", err)
	}
	if _, err := router.Switch(baseSessionKey, "task-b"); err != nil {
		t.Fatalf("switch task-b: %v", err)
	}

	if _, err := router.Rename(baseSessionKey, "task-a", "task-b"); err == nil {
		t.Fatalf("expected renaming to existing alias to fail")
	}
}

func TestSessionContextRouterArchive_CurrentActiveFallsBackToDefault(t *testing.T) {
	router := NewSessionContextRouter("")
	baseSessionKey := "telegram:default:chat-1"

	resolved, err := router.Switch(baseSessionKey, "task-a")
	if err != nil {
		t.Fatalf("switch task-a: %v", err)
	}
	archivedKey, err := router.Archive(baseSessionKey, "task-a")
	if err != nil {
		t.Fatalf("archive task-a: %v", err)
	}
	if archivedKey != resolved {
		t.Fatalf("expected archived session key %q, got %q", resolved, archivedKey)
	}
	if got := router.CurrentAlias(baseSessionKey); got != "" {
		t.Fatalf("expected active alias cleared after archive, got %q", got)
	}
	if got := router.Resolve(baseSessionKey); got != baseSessionKey {
		t.Fatalf("expected resolve to fall back to default %q, got %q", baseSessionKey, got)
	}
	if !router.IsArchived(baseSessionKey, "task-a") {
		t.Fatalf("expected task-a to be archived")
	}
}

func TestSessionContextRouterArchive_PreventsSwitchUntilUnarchive(t *testing.T) {
	router := NewSessionContextRouter("")
	baseSessionKey := "telegram:default:chat-1"

	resolved, err := router.Switch(baseSessionKey, "task-a")
	if err != nil {
		t.Fatalf("switch task-a: %v", err)
	}
	router.Clear(baseSessionKey)
	if _, err := router.Archive(baseSessionKey, "task-a"); err != nil {
		t.Fatalf("archive task-a: %v", err)
	}
	if _, err := router.Switch(baseSessionKey, "task-a"); err == nil {
		t.Fatalf("expected switching archived alias to fail")
	}
	if _, err := router.Unarchive(baseSessionKey, "task-a"); err != nil {
		t.Fatalf("unarchive task-a: %v", err)
	}
	got, err := router.Switch(baseSessionKey, "task-a")
	if err != nil {
		t.Fatalf("switch task-a after unarchive: %v", err)
	}
	if got != resolved {
		t.Fatalf("expected session key to stay %q after unarchive, got %q", resolved, got)
	}
}

func TestSessionContextRouterLoadFromDisk_BackwardCompatibleWithoutArchived(t *testing.T) {
	tempDir := t.TempDir()
	path := filepath.Join(tempDir, "session_context_routes.json")
	state := map[string]any{
		"active_alias": map[string]string{"telegram:default:chat-1": "task-a"},
		"aliases": map[string]map[string]string{
			"telegram:default:chat-1": {"task-a": "telegram:default:chat-1:session:task-a"},
		},
	}
	data, err := json.Marshal(state)
	if err != nil {
		t.Fatalf("marshal state: %v", err)
	}
	if err := os.WriteFile(path, data, 0o644); err != nil {
		t.Fatalf("write state: %v", err)
	}

	router := NewSessionContextRouter(tempDir)
	baseSessionKey := "telegram:default:chat-1"
	if got := router.CurrentAlias(baseSessionKey); got != "task-a" {
		t.Fatalf("expected current alias task-a, got %q", got)
	}
	if router.IsArchived(baseSessionKey, "task-a") {
		t.Fatalf("expected task-a not archived when archived field is absent")
	}
	if got := router.Resolve(baseSessionKey); got != "telegram:default:chat-1:session:task-a" {
		t.Fatalf("unexpected resolved session key: %q", got)
	}
}
