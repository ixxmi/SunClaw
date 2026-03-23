package config

import (
	"path/filepath"
	"strings"
	"testing"
)

func TestGetWorkspacePathHandlesNilConfig(t *testing.T) {
	workspace, err := GetWorkspacePath(nil)
	if err != nil {
		t.Fatalf("GetWorkspacePath(nil) returned error: %v", err)
	}

	if !strings.HasSuffix(workspace, filepath.Join(".goclaw", "workspace")) {
		t.Fatalf("expected default workspace path, got %q", workspace)
	}
}

func TestGetDerivedWorkspacePaths(t *testing.T) {
	cfg := &Config{
		Workspace: WorkspaceConfig{
			Path: "/tmp/custom-workspace",
		},
	}

	workspace, err := GetWorkspacePath(cfg)
	if err != nil {
		t.Fatalf("GetWorkspacePath returned error: %v", err)
	}
	if workspace != "/tmp/custom-workspace" {
		t.Fatalf("unexpected workspace path: %q", workspace)
	}

	skillsPath, err := GetSkillsPath(cfg)
	if err != nil {
		t.Fatalf("GetSkillsPath returned error: %v", err)
	}
	if skillsPath != filepath.Join("/tmp/custom-workspace", "skills") {
		t.Fatalf("unexpected skills path: %q", skillsPath)
	}

	memoryPath, err := GetMemoryPath(cfg)
	if err != nil {
		t.Fatalf("GetMemoryPath returned error: %v", err)
	}
	if memoryPath != filepath.Join("/tmp/custom-workspace", "memory") {
		t.Fatalf("unexpected memory path: %q", memoryPath)
	}
}
