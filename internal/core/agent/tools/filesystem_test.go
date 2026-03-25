package tools

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestReadConfigUsesBootstrapOwnerDirectory(t *testing.T) {
	baseDir := t.TempDir()
	ownerDir := filepath.Join(baseDir, "agents", "vibecoding")
	if err := os.MkdirAll(ownerDir, 0755); err != nil {
		t.Fatalf("mkdir owner dir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(ownerDir, "IDENTITY.md"), []byte("owner-specific identity"), 0644); err != nil {
		t.Fatalf("write IDENTITY.md: %v", err)
	}

	tool := NewFileSystemTool(nil, nil, baseDir)
	tool.SetConfigDirResolver(func(ownerID string) string {
		if ownerID == "vibecoding" {
			return ownerDir
		}
		return baseDir
	})

	ctx := context.WithValue(context.Background(), "agent_id", "architect-subagent")
	ctx = context.WithValue(ctx, "bootstrap_owner_id", "vibecoding")
	got, err := tool.ReadConfig(ctx, map[string]interface{}{"file": "identity"})
	if err != nil {
		t.Fatalf("ReadConfig error: %v", err)
	}
	if !strings.Contains(got, "owner-specific identity") {
		t.Fatalf("expected owner-specific content, got %q", got)
	}
}

func TestUpdateConfigUsesBootstrapOwnerDirectory(t *testing.T) {
	baseDir := t.TempDir()
	ownerDir := filepath.Join(baseDir, "agents", "architect")

	tool := NewFileSystemTool(nil, nil, baseDir)
	tool.SetConfigDirResolver(func(ownerID string) string {
		if ownerID == "architect" {
			return ownerDir
		}
		return baseDir
	})

	ctx := context.WithValue(context.Background(), "agent_id", "planner-subagent")
	ctx = context.WithValue(ctx, "bootstrap_owner_id", "architect")
	if _, err := tool.UpdateConfig(ctx, map[string]interface{}{
		"file":    "soul",
		"content": "architect isolated soul",
	}); err != nil {
		t.Fatalf("UpdateConfig error: %v", err)
	}

	content, err := os.ReadFile(filepath.Join(ownerDir, "SOUL.md"))
	if err != nil {
		t.Fatalf("read SOUL.md: %v", err)
	}
	if string(content) != "architect isolated soul" {
		t.Fatalf("unexpected SOUL.md content: %q", string(content))
	}
}
