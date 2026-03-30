package sandbox

import (
	"context"
	"testing"
)

func TestDockerExecutorFallbackSucceedsWhenLocalExecutionSucceeds(t *testing.T) {
	auth := NewAuthManager()
	_ = auth.Authorize(context.Background())

	executor := NewDockerExecutor(nil, "", nil, NewLocalExecutor(auth))
	result := executor.Execute(context.Background(), "printf hi\n# bash", "bash", ExecuteOptions{})

	if result.Error != nil {
		t.Fatalf("expected successful fallback, got error: %v", result.Error)
	}
	if result.Executor != "docker-fallback-local" {
		t.Fatalf("executor = %q, want docker-fallback-local", result.Executor)
	}
	if !result.UsedFallback {
		t.Fatalf("expected UsedFallback=true")
	}
	if result.Stdout != "hi" {
		t.Fatalf("stdout = %q, want hi", result.Stdout)
	}
}
