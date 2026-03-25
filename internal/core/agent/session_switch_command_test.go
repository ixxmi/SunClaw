package agent

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/smallnest/goclaw/internal/core/bus"
	"github.com/smallnest/goclaw/internal/core/session"
)

func TestParseSessionSwitchCommand_ListAll(t *testing.T) {
	cmd := parseSessionSwitchCommand("/session list --all")
	if !cmd.IsSwitch {
		t.Fatalf("expected session command to be recognized")
	}
	if cmd.Action != "list" {
		t.Fatalf("expected action list, got %q", cmd.Action)
	}
	if !cmd.ShowAll {
		t.Fatalf("expected --all to enable ShowAll")
	}
}

func TestParseSessionSwitchCommand_Rename(t *testing.T) {
	cmd := parseSessionSwitchCommand("/session rename old-alias new-alias")
	if !cmd.IsSwitch {
		t.Fatalf("expected session command to be recognized")
	}
	if cmd.Action != "rename" {
		t.Fatalf("expected action rename, got %q", cmd.Action)
	}
	if cmd.Alias != "old-alias" || cmd.NewAlias != "new-alias" {
		t.Fatalf("unexpected aliases: old=%q new=%q", cmd.Alias, cmd.NewAlias)
	}
}

func TestBuildSessionKey_UsesLogicalSessionAlias(t *testing.T) {
	mgr := &AgentManager{sessionContextRouter: NewSessionContextRouter("")}
	msg := &bus.InboundMessage{
		Channel:   "wework",
		AccountID: "default",
		ChatID:    "chat-1",
		Timestamp: time.Now(),
	}

	base := mgr.buildBaseSessionKey(msg)
	if got := mgr.buildSessionKey(msg); got != base {
		t.Fatalf("expected base session key %q, got %q", base, got)
	}

	resolved, err := mgr.sessionContextRouter.Switch(base, "bugfix-login")
	if err != nil {
		t.Fatalf("switch bugfix-login: %v", err)
	}
	if got := mgr.buildSessionKey(msg); got != resolved {
		t.Fatalf("expected resolved session key %q, got %q", resolved, got)
	}
}

func TestBuildSessionKey_ClearLogicalSessionAlias(t *testing.T) {
	mgr := &AgentManager{sessionContextRouter: NewSessionContextRouter("")}
	msg := &bus.InboundMessage{
		Channel:   "telegram",
		AccountID: "default",
		ChatID:    "6411270493",
		Timestamp: time.Now(),
	}

	base := mgr.buildBaseSessionKey(msg)
	if _, err := mgr.sessionContextRouter.Switch(base, "task-a"); err != nil {
		t.Fatalf("switch task-a: %v", err)
	}
	mgr.sessionContextRouter.Clear(base)

	if got := mgr.buildSessionKey(msg); got != base {
		t.Fatalf("expected cleared session key %q, got %q", base, got)
	}
}

func TestGetSessionRecentPreview_PrefersAssistantThenFallsBackUserThenEmpty(t *testing.T) {
	sessionMgr, err := session.NewManager(t.TempDir())
	if err != nil {
		t.Fatalf("create session manager: %v", err)
	}
	mgr := &AgentManager{sessionMgr: sessionMgr}

	assistantKey := "chat:assistant"
	assistantSess, _ := sessionMgr.GetOrCreate(assistantKey)
	assistantSess.AddMessage(session.Message{Role: "user", Content: "用户提问", Timestamp: time.Now().Add(-2 * time.Minute)})
	assistantSess.AddMessage(session.Message{Role: "assistant", Content: "\n  最近助手回复\n第二行  ", Timestamp: time.Now().Add(-time.Minute)})
	assistantSess.AddMessage(session.Message{Role: "tool", Content: "tool output", Timestamp: time.Now()})
	if got := mgr.GetSessionRecentPreview(assistantKey); got != "最近助手回复 第二行" {
		t.Fatalf("expected assistant preview, got %q", got)
	}

	userKey := "chat:user"
	userSess, _ := sessionMgr.GetOrCreate(userKey)
	userSess.AddMessage(session.Message{Role: "assistant", Content: "", ToolCalls: []session.ToolCall{{ID: "call-1", Name: "x"}}, Timestamp: time.Now().Add(-time.Minute)})
	userSess.AddMessage(session.Message{Role: "user", Content: "\n 用户最近问题 \n", Timestamp: time.Now()})
	if got := mgr.GetSessionRecentPreview(userKey); got != "用户最近问题" {
		t.Fatalf("expected user fallback preview, got %q", got)
	}

	if got := mgr.GetSessionRecentPreview("chat:empty"); got != "暂无历史消息" {
		t.Fatalf("expected empty preview fallback, got %q", got)
	}
}

func TestBuildSessionListReply_IncludesRecentPreview(t *testing.T) {
	sessionMgr, err := session.NewManager(t.TempDir())
	if err != nil {
		t.Fatalf("create session manager: %v", err)
	}

	mgr := &AgentManager{
		sessionMgr:           sessionMgr,
		sessionContextRouter: NewSessionContextRouter(""),
	}

	baseKey := "telegram:acc-1:chat-1"
	featureKey, err := mgr.sessionContextRouter.Switch(baseKey, "feature-a")
	if err != nil {
		t.Fatalf("switch feature-a: %v", err)
	}
	mgr.sessionContextRouter.Clear(baseKey)

	defaultSess, err := sessionMgr.GetOrCreate(baseKey)
	if err != nil {
		t.Fatalf("get default session: %v", err)
	}
	defaultSess.AddMessage(session.Message{Role: "assistant", Content: "默认会话最近回复", Timestamp: time.Now()})

	featureSess, err := sessionMgr.GetOrCreate(featureKey)
	if err != nil {
		t.Fatalf("get feature session: %v", err)
	}
	featureSess.AddMessage(session.Message{Role: "user", Content: "功能会话最近问题", Timestamp: time.Now()})

	reply := mgr.buildSessionListReply(baseKey, false)
	if !strings.Contains(reply, "- `default` ← 当前") {
		t.Fatalf("missing default alias entry: %q", reply)
	}
	if !strings.Contains(reply, "`"+baseKey+"`") {
		t.Fatalf("missing default session key: %q", reply)
	}
	if !strings.Contains(reply, "最近预览：默认会话最近回复") {
		t.Fatalf("missing default preview: %q", reply)
	}
	if !strings.Contains(reply, "- `feature-a`") {
		t.Fatalf("missing feature alias entry: %q", reply)
	}
	if !strings.Contains(reply, "`"+featureKey+"`") {
		t.Fatalf("missing feature session key: %q", reply)
	}
	if !strings.Contains(reply, "最近预览：功能会话最近问题") {
		t.Fatalf("missing feature preview: %q", reply)
	}
}

func TestBuildSessionListReply_ShowsEmptyHistoryFallback(t *testing.T) {
	mgr := &AgentManager{
		sessionMgr:           nil,
		sessionContextRouter: NewSessionContextRouter(""),
	}

	baseKey := "wework:default:chat-2"
	aliasKey, err := mgr.sessionContextRouter.Switch(baseKey, "task-a")
	if err != nil {
		t.Fatalf("switch task-a: %v", err)
	}
	mgr.sessionContextRouter.Clear(baseKey)

	reply := mgr.buildSessionListReply(baseKey, false)
	if !strings.Contains(reply, "- `default` ← 当前") {
		t.Fatalf("missing default entry: %q", reply)
	}
	if !strings.Contains(reply, "`"+baseKey+"`") {
		t.Fatalf("missing default session key: %q", reply)
	}
	if !strings.Contains(reply, "`"+aliasKey+"`") {
		t.Fatalf("missing alias session key: %q", reply)
	}
	if count := strings.Count(reply, "最近预览：暂无历史消息"); count != 2 {
		t.Fatalf("expected empty preview fallback for both entries, got %d in %q", count, reply)
	}
}

func TestBuildSessionListReply_HidesArchivedAlias(t *testing.T) {
	sessionMgr, err := session.NewManager(t.TempDir())
	if err != nil {
		t.Fatalf("create session manager: %v", err)
	}

	mgr := &AgentManager{
		sessionMgr:           sessionMgr,
		sessionContextRouter: NewSessionContextRouter(""),
	}

	baseKey := "telegram:acc-1:chat-1"
	featureKey, err := mgr.sessionContextRouter.Switch(baseKey, "feature-a")
	if err != nil {
		t.Fatalf("switch feature-a: %v", err)
	}
	mgr.sessionContextRouter.Clear(baseKey)
	if _, err := mgr.sessionContextRouter.Archive(baseKey, "feature-a"); err != nil {
		t.Fatalf("archive feature-a: %v", err)
	}

	reply := mgr.buildSessionListReply(baseKey, false)
	if strings.Contains(reply, "- `feature-a`") || strings.Contains(reply, "`"+featureKey+"`") {
		t.Fatalf("archived alias should be hidden from list: %q", reply)
	}
	if !strings.Contains(reply, "- `default` ← 当前") {
		t.Fatalf("missing default entry after archive: %q", reply)
	}
}

func TestBuildSessionListReply_ShowsArchivedAliasWithMarkWhenShowAll(t *testing.T) {
	sessionMgr, err := session.NewManager(t.TempDir())
	if err != nil {
		t.Fatalf("create session manager: %v", err)
	}

	mgr := &AgentManager{
		sessionMgr:           sessionMgr,
		sessionContextRouter: NewSessionContextRouter(""),
	}

	baseKey := "telegram:acc-1:chat-1"
	featureKey, err := mgr.sessionContextRouter.Switch(baseKey, "feature-a")
	if err != nil {
		t.Fatalf("switch feature-a: %v", err)
	}
	mgr.sessionContextRouter.Clear(baseKey)
	if _, err := mgr.sessionContextRouter.Archive(baseKey, "feature-a"); err != nil {
		t.Fatalf("archive feature-a: %v", err)
	}

	featureSess, err := sessionMgr.GetOrCreate(featureKey)
	if err != nil {
		t.Fatalf("get feature session: %v", err)
	}
	featureSess.AddMessage(session.Message{Role: "user", Content: "功能会话最近问题", Timestamp: time.Now()})

	reply := mgr.buildSessionListReply(baseKey, true)
	if !strings.Contains(reply, "- `feature-a` [archived]") {
		t.Fatalf("archived alias should be shown with mark: %q", reply)
	}
	if !strings.Contains(reply, "`"+featureKey+"`") {
		t.Fatalf("missing archived session key: %q", reply)
	}
	if !strings.Contains(reply, "最近预览：功能会话最近问题") {
		t.Fatalf("missing archived preview: %q", reply)
	}
}

func TestHandleSessionSwitchCommand_IncludesRecentPreview(t *testing.T) {
	messageBus := bus.NewMessageBus(16)
	sub := messageBus.SubscribeOutbound()
	defer sub.Unsubscribe()

	sessionMgr, err := session.NewManager(t.TempDir())
	if err != nil {
		t.Fatalf("create session manager: %v", err)
	}

	mgr := &AgentManager{
		bus:                  messageBus,
		sessionMgr:           sessionMgr,
		sessionContextRouter: NewSessionContextRouter(""),
	}

	msg := &bus.InboundMessage{
		ID:        "msg-session-switch-1",
		Channel:   "telegram",
		AccountID: "acc-1",
		ChatID:    "chat-1",
		Content:   "/session feature-a",
		Timestamp: time.Now(),
	}

	baseKey := mgr.buildBaseSessionKey(msg)
	resolvedKey, err := mgr.sessionContextRouter.Switch(baseKey, "feature-a")
	if err != nil {
		t.Fatalf("switch feature-a: %v", err)
	}
	sess, err := sessionMgr.GetOrCreate(resolvedKey)
	if err != nil {
		t.Fatalf("get target session: %v", err)
	}
	sess.AddMessage(session.Message{Role: "assistant", Content: "这是目标会话的最近摘要", Timestamp: time.Now()})
	mgr.sessionContextRouter.Clear(baseKey)

	if err := mgr.handleSessionSwitchCommand(context.Background(), parseSessionSwitchCommand(msg.Content), msg); err != nil {
		t.Fatalf("handleSessionSwitchCommand error: %v", err)
	}

	select {
	case outbound := <-sub.Channel:
		if outbound == nil {
			t.Fatalf("expected outbound switch reply")
		}
		if !strings.Contains(outbound.Content, "已切换到逻辑会话：`feature-a`") {
			t.Fatalf("missing alias in reply: %q", outbound.Content)
		}
		if !strings.Contains(outbound.Content, "Session Key：`"+resolvedKey+"`") {
			t.Fatalf("missing session key in reply: %q", outbound.Content)
		}
		if !strings.Contains(outbound.Content, "最近预览：这是目标会话的最近摘要") {
			t.Fatalf("missing preview in reply: %q", outbound.Content)
		}
	case <-time.After(2 * time.Second):
		t.Fatalf("timed out waiting for session switch reply")
	}
}

func TestHandleSessionSwitchCommand_ArchivedAliasCannotSwitchUntilUnarchive(t *testing.T) {
	messageBus := bus.NewMessageBus(16)
	sub := messageBus.SubscribeOutbound()
	defer sub.Unsubscribe()

	mgr := &AgentManager{
		bus:                  messageBus,
		sessionContextRouter: NewSessionContextRouter(""),
	}

	msg := &bus.InboundMessage{
		ID:        "msg-session-switch-archived",
		Channel:   "telegram",
		AccountID: "acc-1",
		ChatID:    "chat-1",
		Content:   "/session switch feature-a",
		Timestamp: time.Now(),
	}
	baseKey := mgr.buildBaseSessionKey(msg)
	if _, err := mgr.sessionContextRouter.Switch(baseKey, "feature-a"); err != nil {
		t.Fatalf("switch feature-a: %v", err)
	}
	mgr.sessionContextRouter.Clear(baseKey)
	if _, err := mgr.sessionContextRouter.Archive(baseKey, "feature-a"); err != nil {
		t.Fatalf("archive feature-a: %v", err)
	}

	if err := mgr.handleSessionSwitchCommand(context.Background(), parseSessionSwitchCommand(msg.Content), msg); err != nil {
		t.Fatalf("handle archived switch command error: %v", err)
	}

	select {
	case outbound := <-sub.Channel:
		if outbound == nil {
			t.Fatalf("expected outbound archived switch reply")
		}
		if !strings.Contains(outbound.Content, "已归档") || !strings.Contains(outbound.Content, "/session unarchive feature-a") {
			t.Fatalf("expected archived hint in reply, got %q", outbound.Content)
		}
	case <-time.After(2 * time.Second):
		t.Fatalf("timed out waiting for archived switch reply")
	}

	if _, err := mgr.sessionContextRouter.Unarchive(baseKey, "feature-a"); err != nil {
		t.Fatalf("unarchive feature-a: %v", err)
	}
	if err := mgr.handleSessionSwitchCommand(context.Background(), parseSessionSwitchCommand(msg.Content), msg); err != nil {
		t.Fatalf("handle unarchived switch command error: %v", err)
	}

	select {
	case outbound := <-sub.Channel:
		if outbound == nil {
			t.Fatalf("expected outbound switch reply after unarchive")
		}
		if !strings.Contains(outbound.Content, "已切换到逻辑会话：`feature-a`") {
			t.Fatalf("expected switch success after unarchive, got %q", outbound.Content)
		}
	case <-time.After(2 * time.Second):
		t.Fatalf("timed out waiting for unarchived switch reply")
	}
}
