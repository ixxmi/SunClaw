package tools

import (
	"context"
	"fmt"
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

func TestUpdateConfigUsesUserWorkspaceRootForBootstrapOwner(t *testing.T) {
	baseDir := t.TempDir()
	userWorkspace := filepath.Join(baseDir, "users", "tenant-a", "wework", "acc-1", "sender-1")

	tool := NewFileSystemTool(nil, nil, baseDir)
	tool.SetConfigDirResolver(func(ownerID string) string {
		return filepath.Join(baseDir, "agents", ownerID)
	})

	ctx := context.WithValue(context.Background(), "workspace_root", userWorkspace)
	ctx = context.WithValue(ctx, "bootstrap_owner_id", "vibecoding")
	ctx = context.WithValue(ctx, "agent_id", "vibecoding")
	if _, err := tool.UpdateConfig(ctx, map[string]interface{}{
		"file":    "user",
		"content": "per-user profile",
	}); err != nil {
		t.Fatalf("UpdateConfig error: %v", err)
	}

	target := filepath.Join(userWorkspace, "agents", "vibecoding", "bootstrap", "USER.md")
	content, err := os.ReadFile(target)
	if err != nil {
		t.Fatalf("read USER.md: %v", err)
	}
	if string(content) != "per-user profile" {
		t.Fatalf("unexpected USER.md content: %q", string(content))
	}
}

func TestUpdateConfigFallsBackToIdentityScopedWorkspaceWhenWorkspaceRootMissing(t *testing.T) {
	baseDir := t.TempDir()
	tool := NewFileSystemTool(nil, nil, baseDir)

	ctx := context.WithValue(context.Background(), "tenant_id", "tenant-a")
	ctx = context.WithValue(ctx, "channel", "wework")
	ctx = context.WithValue(ctx, "account_id", "acc-1")
	ctx = context.WithValue(ctx, "sender_id", "sender-1")
	ctx = context.WithValue(ctx, "bootstrap_owner_id", "vibecoding")

	if _, err := tool.UpdateConfig(ctx, map[string]interface{}{
		"file":    "identity",
		"content": "identity for namespaced user",
	}); err != nil {
		t.Fatalf("UpdateConfig error: %v", err)
	}

	target := filepath.Join(baseDir, "users", "tenant-a", "wework", "acc-1", "sender-1", "agents", "vibecoding", "bootstrap", "IDENTITY.md")
	content, err := os.ReadFile(target)
	if err != nil {
		t.Fatalf("read IDENTITY.md: %v", err)
	}
	if string(content) != "identity for namespaced user" {
		t.Fatalf("unexpected IDENTITY.md content: %q", string(content))
	}
}

func TestReadFileReturnsFullContentForSmallTextFile(t *testing.T) {
	baseDir := t.TempDir()
	path := filepath.Join(baseDir, "sample.txt")
	want := "line 1\nline 2\nline 3\n"
	if err := os.WriteFile(path, []byte(want), 0644); err != nil {
		t.Fatalf("write sample.txt: %v", err)
	}

	tool := NewFileSystemTool([]string{baseDir}, nil, baseDir)
	got, err := tool.ReadFile(context.Background(), map[string]interface{}{"path": path})
	if err != nil {
		t.Fatalf("ReadFile error: %v", err)
	}
	if got != want {
		t.Fatalf("ReadFile = %q, want %q", got, want)
	}
}

func TestReadFileSupportsExplicitLineRange(t *testing.T) {
	baseDir := t.TempDir()
	path := filepath.Join(baseDir, "range.txt")
	content := strings.Join([]string{
		"alpha",
		"beta",
		"gamma",
		"delta",
		"epsilon",
		"",
	}, "\n")
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatalf("write range.txt: %v", err)
	}

	tool := NewFileSystemTool([]string{baseDir}, nil, baseDir)
	got, err := tool.ReadFile(context.Background(), map[string]interface{}{
		"path":       path,
		"start_line": 2,
		"end_line":   4,
	})
	if err != nil {
		t.Fatalf("ReadFile error: %v", err)
	}
	if !strings.Contains(got, "lines 2-4") {
		t.Fatalf("expected line range header, got %q", got)
	}
	for _, expected := range []string{"beta\n", "gamma\n", "delta\n"} {
		if !strings.Contains(got, expected) {
			t.Fatalf("expected %q in output %q", expected, got)
		}
	}
	if strings.Contains(got, "alpha\n") || strings.Contains(got, "epsilon\n") {
		t.Fatalf("unexpected lines in output %q", got)
	}
}

func TestReadFileUsesPreviewForLargeFiles(t *testing.T) {
	baseDir := t.TempDir()
	path := filepath.Join(baseDir, "large.txt")

	var builder strings.Builder
	for i := 1; i <= 5000; i++ {
		builder.WriteString(fmt.Sprintf("line %04d %s\n", i, strings.Repeat("x", 20)))
	}
	if err := os.WriteFile(path, []byte(builder.String()), 0644); err != nil {
		t.Fatalf("write large.txt: %v", err)
	}

	tool := NewFileSystemTool([]string{baseDir}, nil, baseDir)
	got, err := tool.ReadFile(context.Background(), map[string]interface{}{"path": path})
	if err != nil {
		t.Fatalf("ReadFile error: %v", err)
	}
	if !strings.Contains(got, "Returning a preview") {
		t.Fatalf("expected preview notice, got %q", got)
	}
	if !strings.Contains(got, "Use start_line/end_line") {
		t.Fatalf("expected range guidance, got %q", got)
	}
	if !strings.Contains(got, "line 0001") {
		t.Fatalf("expected head lines in preview, got %q", got)
	}
	if !strings.Contains(got, "line 5000") {
		t.Fatalf("expected tail lines in preview, got %q", got)
	}
	if !strings.Contains(got, "omitted") {
		t.Fatalf("expected omitted-lines note, got %q", got)
	}
	if strings.Contains(got, "line 2500") {
		t.Fatalf("did not expect middle lines in preview")
	}
}

func TestReadFileUsesCompactPreviewForDenseSmallFiles(t *testing.T) {
	baseDir := t.TempDir()
	path := filepath.Join(baseDir, "dense.txt")

	var builder strings.Builder
	for i := 1; i <= readFileInlineMaxLines+20; i++ {
		builder.WriteString(fmt.Sprintf("line %03d\n", i))
	}
	if err := os.WriteFile(path, []byte(builder.String()), 0644); err != nil {
		t.Fatalf("write dense.txt: %v", err)
	}

	tool := NewFileSystemTool([]string{baseDir}, nil, baseDir)
	got, err := tool.ReadFile(context.Background(), map[string]interface{}{"path": path})
	if err != nil {
		t.Fatalf("ReadFile error: %v", err)
	}
	if !strings.Contains(got, "compact preview") {
		t.Fatalf("expected compact preview notice, got %q", got)
	}
	if !strings.Contains(got, "omitted") {
		t.Fatalf("expected omitted lines marker, got %q", got)
	}
	if !strings.Contains(got, "line 001") || !strings.Contains(got, fmt.Sprintf("line %03d", readFileInlineMaxLines+20)) {
		t.Fatalf("expected head/tail coverage, got %q", got)
	}
}

func TestReadFileCapsWideLineRange(t *testing.T) {
	baseDir := t.TempDir()
	path := filepath.Join(baseDir, "wide-range.txt")

	var builder strings.Builder
	for i := 1; i <= 400; i++ {
		builder.WriteString(fmt.Sprintf("line %03d\n", i))
	}
	if err := os.WriteFile(path, []byte(builder.String()), 0644); err != nil {
		t.Fatalf("write wide-range.txt: %v", err)
	}

	tool := NewFileSystemTool([]string{baseDir}, nil, baseDir)
	got, err := tool.ReadFile(context.Background(), map[string]interface{}{
		"path":       path,
		"start_line": 1,
		"end_line":   400,
	})
	if err != nil {
		t.Fatalf("ReadFile error: %v", err)
	}
	if !strings.Contains(got, "was capped") {
		t.Fatalf("expected capped notice, got %q", got)
	}
	if strings.Contains(got, "line 250\n") {
		t.Fatalf("did not expect capped-out lines in output")
	}
}

func TestReadFileTruncatesLongLines(t *testing.T) {
	baseDir := t.TempDir()
	path := filepath.Join(baseDir, "long-line.txt")
	content := strings.Repeat("x", readFileSummaryMaxLineRunes+40) + "\n"
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatalf("write long-line.txt: %v", err)
	}

	tool := NewFileSystemTool([]string{baseDir}, nil, baseDir)
	got, err := tool.ReadFile(context.Background(), map[string]interface{}{"path": path})
	if err != nil {
		t.Fatalf("ReadFile error: %v", err)
	}
	if !strings.Contains(got, "...(truncated)") {
		t.Fatalf("expected long line truncation, got %q", got)
	}
}
