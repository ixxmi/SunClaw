package tools

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/smallnest/goclaw/internal/core/config"
	"github.com/smallnest/goclaw/internal/core/sandbox"
)

func TestSandboxToolWithAutoApprovalExecutesLocally(t *testing.T) {
	tool := NewSandboxToolWithConfig(config.SandboxConfig{}, config.ApprovalsConfig{Behavior: "auto"})

	result, err := tool.Execute(context.Background(), map[string]interface{}{
		"code":     "printf hi\n# bash",
		"language": "bash",
	})
	if err != nil {
		t.Fatalf("Execute returned error: %v", err)
	}

	details := decodeSandboxToolDetails(t, result)
	if ok, _ := details["ok"].(bool); !ok {
		t.Fatalf("expected ok=true, got details=%v", details)
	}
	if executor, _ := details["executor"].(string); executor != "local" {
		t.Fatalf("expected executor=local, got %q", executor)
	}
	if stdout, _ := details["stdout"].(string); stdout != "hi" {
		t.Fatalf("expected stdout=hi, got %q", stdout)
	}
}

func TestSandboxToolWithAllowlistExecutesLocally(t *testing.T) {
	tool := NewSandboxToolWithConfig(config.SandboxConfig{}, config.ApprovalsConfig{
		Behavior:  "manual",
		Allowlist: []string{"sandbox_execute"},
	})

	result, err := tool.Execute(context.Background(), map[string]interface{}{
		"code":     "printf hi\n# bash",
		"language": "bash",
	})
	if err != nil {
		t.Fatalf("Execute returned error: %v", err)
	}

	details := decodeSandboxToolDetails(t, result)
	if authorized, _ := details["authorized"].(bool); !authorized {
		t.Fatalf("expected authorized=true, got details=%v", details)
	}
}

func TestSandboxToolWithoutApprovalRejectsLocalExecution(t *testing.T) {
	tool := NewSandboxToolWithConfig(config.SandboxConfig{}, config.ApprovalsConfig{Behavior: "manual"})

	result, err := tool.Execute(context.Background(), map[string]interface{}{
		"code":     "printf hi\n# bash",
		"language": "bash",
	})
	if err == nil {
		t.Fatalf("expected authorization error")
	}
	if !strings.Contains(err.Error(), sandbox.ErrLocalExecutionNotAuthorized.Error()) {
		t.Fatalf("expected local authorization error, got %v", err)
	}

	details := decodeSandboxToolDetails(t, result)
	if authorized, _ := details["authorized"].(bool); authorized {
		t.Fatalf("expected authorized=false, got details=%v", details)
	}
}

func TestSandboxToolSkipsNonExecutableContent(t *testing.T) {
	tool := NewSandboxToolWithConfig(config.SandboxConfig{}, config.ApprovalsConfig{Behavior: "auto"})

	result, err := tool.Execute(context.Background(), map[string]interface{}{
		"code":     "hello world",
		"language": "bash",
	})
	if err != nil {
		t.Fatalf("Execute returned error: %v", err)
	}

	details := decodeSandboxToolDetails(t, result)
	if needsSandbox, _ := details["needs_sandbox"].(bool); needsSandbox {
		t.Fatalf("expected needs_sandbox=false, got details=%v", details)
	}
}

func decodeSandboxToolDetails(t *testing.T, raw string) map[string]any {
	t.Helper()

	var payload struct {
		Details map[string]any `json:"details"`
	}
	if err := json.Unmarshal([]byte(raw), &payload); err != nil {
		t.Fatalf("failed to decode sandbox tool result: %v", err)
	}
	return payload.Details
}
