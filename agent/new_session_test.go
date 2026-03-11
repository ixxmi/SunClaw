package agent

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/smallnest/goclaw/bus"
	"github.com/smallnest/goclaw/session"
)

func TestIsNewSessionCommand(t *testing.T) {
	cases := []struct {
		input string
		want  bool
	}{
		{input: "/new", want: true},
		{input: "   /new   ", want: true},
		{input: "/new1", want: false},
		{input: "hello /new", want: false},
	}

	for _, tc := range cases {
		if got := isNewSessionCommand(tc.input); got != tc.want {
			t.Fatalf("isNewSessionCommand(%q)=%v, want %v", tc.input, got, tc.want)
		}
	}
}

func TestResetSessionContextIfNeeded_NewCommandClearsOldContext(t *testing.T) {
	messageBus := bus.NewMessageBus(16)
	sub := messageBus.SubscribeOutbound()
	defer sub.Unsubscribe()

	sessionMgr, err := session.NewManager(t.TempDir())
	if err != nil {
		t.Fatalf("create session manager: %v", err)
	}

	mgr := &AgentManager{
		bus:        messageBus,
		sessionMgr: sessionMgr,
	}

	msg := &bus.InboundMessage{
		ID:        "msg-new-1",
		Channel:   "telegram",
		AccountID: "acc-1",
		ChatID:    "chat-1",
		Content:   "   /new   ",
		Timestamp: time.Now(),
	}

	sessionKey := mgr.buildSessionKey(msg)
	sess, err := sessionMgr.GetOrCreate(sessionKey)
	if err != nil {
		t.Fatalf("get session: %v", err)
	}
	sess.AddMessage(session.Message{Role: "user", Content: "old context", Timestamp: time.Now()})
	if sess.Metadata == nil {
		sess.Metadata = map[string]interface{}{}
	}
	sess.Metadata["session_id"] = "old-session-id"
	if err := sessionMgr.Save(sess); err != nil {
		t.Fatalf("save old session: %v", err)
	}

	handled, err := mgr.resetSessionContextIfNeeded(context.Background(), msg)
	if err != nil {
		t.Fatalf("resetSessionContextIfNeeded error: %v", err)
	}
	if !handled {
		t.Fatalf("expected /new to be handled")
	}

	newSess, err := sessionMgr.GetOrCreate(sessionKey)
	if err != nil {
		t.Fatalf("get new session: %v", err)
	}
	if len(newSess.Messages) != 0 {
		t.Fatalf("expected old context to be cleared, got %d messages", len(newSess.Messages))
	}

	sid, _ := newSess.Metadata["session_id"].(string)
	if sid == "" || sid == "old-session-id" {
		t.Fatalf("expected new session_id, got %q", sid)
	}

	select {
	case out := <-sub.Channel:
		if out == nil {
			t.Fatalf("expected outbound ack message")
		}
		if !strings.Contains(out.Content, "已开启新会话") {
			t.Fatalf("unexpected ack content: %q", out.Content)
		}
		if !strings.Contains(out.Content, sid) {
			t.Fatalf("ack should include session_id %q, content: %q", sid, out.Content)
		}
	case <-time.After(2 * time.Second):
		t.Fatalf("timed out waiting /new ack message")
	}
}

func TestResetSessionContextIfNeeded_NormalMessageNotAffected(t *testing.T) {
	messageBus := bus.NewMessageBus(16)
	sessionMgr, err := session.NewManager(t.TempDir())
	if err != nil {
		t.Fatalf("create session manager: %v", err)
	}

	mgr := &AgentManager{
		bus:        messageBus,
		sessionMgr: sessionMgr,
	}

	msg := &bus.InboundMessage{
		ID:        "msg-normal-1",
		Channel:   "telegram",
		AccountID: "acc-1",
		ChatID:    "chat-1",
		Content:   "hello",
		Timestamp: time.Now(),
	}

	sessionKey := mgr.buildSessionKey(msg)
	sess, err := sessionMgr.GetOrCreate(sessionKey)
	if err != nil {
		t.Fatalf("get session: %v", err)
	}
	sess.AddMessage(session.Message{Role: "user", Content: "existing", Timestamp: time.Now()})
	if err := sessionMgr.Save(sess); err != nil {
		t.Fatalf("save session: %v", err)
	}

	handled, err := mgr.resetSessionContextIfNeeded(context.Background(), msg)
	if err != nil {
		t.Fatalf("resetSessionContextIfNeeded error: %v", err)
	}
	if handled {
		t.Fatalf("expected normal message not to be handled as /new")
	}

	sameSess, err := sessionMgr.GetOrCreate(sessionKey)
	if err != nil {
		t.Fatalf("get session again: %v", err)
	}
	if len(sameSess.Messages) != 1 {
		t.Fatalf("expected context preserved for normal message, got %d messages", len(sameSess.Messages))
	}
}
