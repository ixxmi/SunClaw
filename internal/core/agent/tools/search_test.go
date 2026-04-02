package tools

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestGlobFilesFallbackFindsMatchingFiles(t *testing.T) {
	baseDir := t.TempDir()
	mustWriteFile(t, filepath.Join(baseDir, "main.go"), "package main\n")
	mustWriteFile(t, filepath.Join(baseDir, "README.md"), "# readme\n")
	mustWriteFile(t, filepath.Join(baseDir, "internal", "router.go"), "package internal\n")

	tool := NewSearchTool([]string{baseDir}, nil, baseDir)
	tool.rgPath = ""

	got, err := tool.GlobFiles(context.Background(), map[string]interface{}{
		"pattern": "**/*.go",
	})
	if err != nil {
		t.Fatalf("GlobFiles error: %v", err)
	}

	if !strings.Contains(got, "main.go") {
		t.Fatalf("expected main.go in output, got %q", got)
	}
	if !strings.Contains(got, "internal/router.go") {
		t.Fatalf("expected internal/router.go in output, got %q", got)
	}
	if strings.Contains(got, "README.md") {
		t.Fatalf("did not expect README.md in output, got %q", got)
	}
}

func TestGlobFilesFallbackSupportsOffsetAndLimit(t *testing.T) {
	baseDir := t.TempDir()
	mustWriteFile(t, filepath.Join(baseDir, "a.go"), "package a\n")
	mustWriteFile(t, filepath.Join(baseDir, "b.go"), "package b\n")
	mustWriteFile(t, filepath.Join(baseDir, "c.go"), "package c\n")

	tool := NewSearchTool([]string{baseDir}, nil, baseDir)
	tool.rgPath = ""

	got, err := tool.GlobFiles(context.Background(), map[string]interface{}{
		"pattern":    "*.go",
		"offset":     1,
		"head_limit": 1,
	})
	if err != nil {
		t.Fatalf("GlobFiles error: %v", err)
	}

	if strings.Contains(got, "a.go") {
		t.Fatalf("expected a.go to be skipped by offset, got %q", got)
	}
	if !strings.Contains(got, "b.go") {
		t.Fatalf("expected b.go in output, got %q", got)
	}
	if strings.Contains(got, "c.go") {
		t.Fatalf("expected head_limit to trim c.go, got %q", got)
	}
}

func TestGrepContentFallbackReturnsMatchingLines(t *testing.T) {
	baseDir := t.TempDir()
	mustWriteFile(t, filepath.Join(baseDir, "internal", "router.go"), strings.Join([]string{
		"package internal",
		"",
		"func buildRouter() {}",
		"func buildHandler() {}",
	}, "\n"))

	tool := NewSearchTool([]string{baseDir}, nil, baseDir)
	tool.rgPath = ""

	got, err := tool.GrepContent(context.Background(), map[string]interface{}{
		"pattern": "buildRouter",
		"glob":    "**/*.go",
	})
	if err != nil {
		t.Fatalf("GrepContent error: %v", err)
	}

	if !strings.Contains(got, "internal/router.go:3:func buildRouter() {}") {
		t.Fatalf("expected matching line in output, got %q", got)
	}
}

func TestGrepContentFallbackSupportsFilesWithMatchesMode(t *testing.T) {
	baseDir := t.TempDir()
	mustWriteFile(t, filepath.Join(baseDir, "internal", "router.go"), "func buildRouter() {}\n")
	mustWriteFile(t, filepath.Join(baseDir, "internal", "service.go"), "func buildService() {}\n")

	tool := NewSearchTool([]string{baseDir}, nil, baseDir)
	tool.rgPath = ""

	got, err := tool.GrepContent(context.Background(), map[string]interface{}{
		"pattern":     "build",
		"glob":        "**/*.go",
		"output_mode": "files_with_matches",
	})
	if err != nil {
		t.Fatalf("GrepContent error: %v", err)
	}

	if !strings.Contains(got, "internal/router.go") || !strings.Contains(got, "internal/service.go") {
		t.Fatalf("expected matching files in output, got %q", got)
	}
}

func TestGrepContentRespectsAllowlist(t *testing.T) {
	baseDir := t.TempDir()
	privateDir := filepath.Join(baseDir, "private")
	mustWriteFile(t, filepath.Join(privateDir, "secret.txt"), "token=abc\n")

	tool := NewSearchTool([]string{filepath.Join(baseDir, "allowed")}, nil, baseDir)
	tool.rgPath = ""

	if _, err := tool.GrepContent(context.Background(), map[string]interface{}{
		"pattern": "token",
		"path":    privateDir,
	}); err == nil {
		t.Fatalf("expected allowlist enforcement error")
	}
}

func mustWriteFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("mkdir %s: %v", filepath.Dir(path), err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}
