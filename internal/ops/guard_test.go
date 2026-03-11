package ops

import (
	"errors"
	"testing"
)

func TestGuard_ReturnsOperationInProgress(t *testing.T) {
	g := NewGuard()
	if _, err := g.Begin("req-1"); err != nil {
		t.Fatalf("begin req-1 failed: %v", err)
	}

	if _, err := g.Begin("req-2"); !errors.Is(err, ErrOperationInProgress) {
		t.Fatalf("expected ErrOperationInProgress, got %v", err)
	}
}

func TestGuard_HitsCacheForSameRequestID(t *testing.T) {
	g := NewGuard()
	resp := Response{Status: "ok", RequestID: "same", Message: "cached"}

	if _, err := g.Begin("same"); err != nil {
		t.Fatalf("begin failed: %v", err)
	}
	g.End("same", resp)

	cached, err := g.Begin("same")
	if err != nil {
		t.Fatalf("second begin failed: %v", err)
	}
	if cached == nil || cached.Message != "cached" {
		t.Fatalf("expected cached response, got %#v", cached)
	}
}
