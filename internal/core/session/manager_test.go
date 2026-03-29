package session

import "testing"

func TestSessionSummaryPersistsAcrossSaveLoad(t *testing.T) {
	dir := t.TempDir()
	manager, err := NewManager(dir)
	if err != nil {
		t.Fatalf("NewManager() error = %v", err)
	}

	sess, err := manager.GetOrCreate("chat-1")
	if err != nil {
		t.Fatalf("GetOrCreate() error = %v", err)
	}
	sess.SetSummary("older conversation summary")
	sess.AddMessage(Message{Role: "user", Content: "hello"})

	if err := manager.Save(sess); err != nil {
		t.Fatalf("Save() error = %v", err)
	}

	reloaded, err := manager.load("chat-1")
	if err != nil {
		t.Fatalf("load() error = %v", err)
	}
	if got := reloaded.GetSummary(); got != "older conversation summary" {
		t.Fatalf("summary = %q, want %q", got, "older conversation summary")
	}
}
