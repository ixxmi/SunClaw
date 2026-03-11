package agent

import (
	"context"
	"testing"
	"time"

	"github.com/smallnest/goclaw/bus"
)

func TestBuildSessionKey_NormalizesEmptyAccountIDAndIncludesThread(t *testing.T) {
	mgr := &AgentManager{}
	msg := &bus.InboundMessage{
		Channel: "slack",
		ChatID:  "C123",
		Metadata: map[string]interface{}{
			"thread_id": "thread-1",
		},
	}

	got := mgr.buildSessionKey(msg)
	want := "slack:default:C123:thread:thread-1"
	if got != want {
		t.Fatalf("buildSessionKey()=%q, want %q", got, want)
	}
}

func TestResolveInboundRoute_PrefersSessionRouteOverBinding(t *testing.T) {
	reviewer := &Agent{}
	defaultAgent := &Agent{}

	mgr := &AgentManager{
		agents: map[string]*Agent{
			"reviewer": reviewer,
			"default":  defaultAgent,
		},
		bindings: map[string]*BindingEntry{
			buildBindingKey("slack", ""): {
				AgentID: "default",
				Agent:   defaultAgent,
			},
		},
		defaultAgent:   defaultAgent,
		defaultAgentID: "default",
		sessionRouter:  NewSessionAgentRouter(""),
	}

	msg := &bus.InboundMessage{Channel: "slack", ChatID: "C123"}
	mgr.sessionRouter.SetAgentID(mgr.buildSessionKey(msg), "reviewer")

	decision := mgr.resolveInboundRoute(msg)
	if decision.agent != reviewer {
		t.Fatalf("expected session-routed agent")
	}
	if decision.agentID != "reviewer" || decision.source != "session" {
		t.Fatalf("unexpected decision: %+v", decision)
	}
}

func TestResolveInboundRoute_SupportsLegacySessionKeyWithoutAccountID(t *testing.T) {
	reviewer := &Agent{}

	mgr := &AgentManager{
		agents: map[string]*Agent{
			"reviewer": reviewer,
		},
		bindings:      make(map[string]*BindingEntry),
		sessionRouter: NewSessionAgentRouter(""),
	}

	msg := &bus.InboundMessage{Channel: "slack", ChatID: "C123"}
	mgr.sessionRouter.SetAgentID("slack::C123", "reviewer")

	decision := mgr.resolveInboundRoute(msg)
	if decision.agent != reviewer || decision.source != "session" {
		t.Fatalf("expected legacy session route to be honored, got %+v", decision)
	}
	if decision.matchedSessionKey != "slack::C123" {
		t.Fatalf("expected legacy session key match, got %q", decision.matchedSessionKey)
	}
}

func TestResolveInboundRoute_FallsBackToBindingThenDefault(t *testing.T) {
	boundAgent := &Agent{}
	defaultAgent := &Agent{}

	mgr := &AgentManager{
		agents: map[string]*Agent{
			"bound":   boundAgent,
			"default": defaultAgent,
		},
		bindings: map[string]*BindingEntry{
			buildBindingKey("teams", ""): {
				AgentID: "bound",
				Agent:   boundAgent,
			},
		},
		defaultAgent:   defaultAgent,
		defaultAgentID: "default",
		sessionRouter:  NewSessionAgentRouter(""),
	}

	bindingDecision := mgr.resolveInboundRoute(&bus.InboundMessage{Channel: "teams", ChatID: "chat-1"})
	if bindingDecision.agent != boundAgent || bindingDecision.source != "binding" {
		t.Fatalf("expected binding route, got %+v", bindingDecision)
	}

	defaultDecision := mgr.resolveInboundRoute(&bus.InboundMessage{Channel: "discord", ChatID: "chat-2"})
	if defaultDecision.agent != defaultAgent || defaultDecision.source != "default" {
		t.Fatalf("expected default route, got %+v", defaultDecision)
	}
}

func TestRouteInbound_AgentSwitchIsThreadScoped(t *testing.T) {
	messageBus := bus.NewMessageBus(16)
	sub := messageBus.SubscribeOutbound()
	defer sub.Unsubscribe()

	mgr := &AgentManager{
		agents: map[string]*Agent{
			"default":  {},
			"reviewer": {},
		},
		bindings:       make(map[string]*BindingEntry),
		defaultAgentID: "default",
		bus:            messageBus,
		sessionRouter:  NewSessionAgentRouter(""),
	}

	msg := &bus.InboundMessage{
		ID:      "msg-switch-1",
		Channel: "slack",
		ChatID:  "C123",
		Content: "/agent reviewer",
		Metadata: map[string]interface{}{
			"thread_id": "thread-1",
		},
		Timestamp: time.Now(),
	}

	if err := mgr.RouteInbound(context.Background(), msg); err != nil {
		t.Fatalf("RouteInbound error: %v", err)
	}

	select {
	case outbound := <-sub.Channel:
		if outbound == nil {
			t.Fatalf("expected outbound switch ack")
		}
	case <-time.After(2 * time.Second):
		t.Fatalf("timed out waiting for switch ack")
	}

	threadKey := mgr.buildSessionKey(msg)
	if got := mgr.sessionRouter.GetAgentID(threadKey); got != "reviewer" {
		t.Fatalf("expected thread session to bind reviewer, got %q", got)
	}

	otherThread := &bus.InboundMessage{
		Channel: "slack",
		ChatID:  "C123",
		Metadata: map[string]interface{}{
			"thread_id": "thread-2",
		},
	}
	if got := mgr.sessionRouter.GetAgentID(mgr.buildSessionKey(otherThread)); got != "" {
		t.Fatalf("expected other thread to remain unbound, got %q", got)
	}
}

func TestHandleAgentSwitchCommand_ClearRemovesLegacySessionKeys(t *testing.T) {
	messageBus := bus.NewMessageBus(16)

	mgr := &AgentManager{
		agents: map[string]*Agent{
			"default":  {},
			"reviewer": {},
		},
		bindings:       make(map[string]*BindingEntry),
		defaultAgentID: "default",
		bus:            messageBus,
		sessionRouter:  NewSessionAgentRouter(""),
	}

	msg := &bus.InboundMessage{
		ID:        "msg-clear-1",
		Channel:   "slack",
		ChatID:    "C123",
		Content:   "/agent clear",
		AccountID: "",
		Timestamp: time.Now(),
	}

	mgr.sessionRouter.SetAgentID("slack::C123", "reviewer")

	if err := mgr.handleAgentSwitchCommand(context.Background(), parseAgentSwitchCommand(msg.Content), msg); err != nil {
		t.Fatalf("handleAgentSwitchCommand error: %v", err)
	}

	if got := mgr.sessionRouter.GetAgentID("slack::C123"); got != "" {
		t.Fatalf("expected legacy session key cleared, got %q", got)
	}
	if got := mgr.sessionRouter.GetAgentID(mgr.buildSessionKey(msg)); got != "" {
		t.Fatalf("expected canonical session key cleared, got %q", got)
	}
}

func TestHandleAgentSwitchCommand_DefaultSetsExplicitSessionRouteOverBinding(t *testing.T) {
	messageBus := bus.NewMessageBus(16)

	defaultAgent := &Agent{}
	mainAgent := &Agent{}
	mgr := &AgentManager{
		agents: map[string]*Agent{
			"default": defaultAgent,
			"main":    mainAgent,
		},
		bindings: map[string]*BindingEntry{
			buildBindingKey("wework", "default"): {
				AgentID:   "main",
				Channel:   "wework",
				AccountID: "default",
				Agent:     mainAgent,
			},
		},
		defaultAgent:   defaultAgent,
		defaultAgentID: "default",
		bus:            messageBus,
		sessionRouter:  NewSessionAgentRouter(""),
	}

	msg := &bus.InboundMessage{
		ID:        "msg-default-1",
		Channel:   "wework",
		AccountID: "default",
		ChatID:    "XueMingYang",
		Content:   "/agent default",
		Timestamp: time.Now(),
	}

	if err := mgr.handleAgentSwitchCommand(context.Background(), parseAgentSwitchCommand(msg.Content), msg); err != nil {
		t.Fatalf("handleAgentSwitchCommand error: %v", err)
	}

	if got := mgr.sessionRouter.GetAgentID(mgr.buildSessionKey(msg)); got != "default" {
		t.Fatalf("expected explicit session route to default, got %q", got)
	}

	followup := &bus.InboundMessage{
		Channel:   "wework",
		AccountID: "default",
		ChatID:    "XueMingYang",
		Content:   "你是谁？",
	}
	decision := mgr.resolveInboundRoute(followup)
	if decision.agent != defaultAgent || decision.agentID != "default" || decision.source != "session" {
		t.Fatalf("expected explicit default session route, got %+v", decision)
	}
}

func TestHandleAgentSwitchCommand_QueryUsesEffectiveBindingRoute(t *testing.T) {
	messageBus := bus.NewMessageBus(16)
	sub := messageBus.SubscribeOutbound()
	defer sub.Unsubscribe()

	defaultAgent := &Agent{}
	mainAgent := &Agent{}
	mgr := &AgentManager{
		agents: map[string]*Agent{
			"default": defaultAgent,
			"main":    mainAgent,
		},
		bindings: map[string]*BindingEntry{
			buildBindingKey("wework", "default"): {
				AgentID:   "main",
				Channel:   "wework",
				AccountID: "default",
				Agent:     mainAgent,
			},
		},
		defaultAgent:   defaultAgent,
		defaultAgentID: "default",
		bus:            messageBus,
		sessionRouter:  NewSessionAgentRouter(""),
	}

	msg := &bus.InboundMessage{
		ID:        "msg-query-1",
		Channel:   "wework",
		AccountID: "default",
		ChatID:    "XueMingYang",
		Content:   "/agent",
		Timestamp: time.Now(),
	}

	if err := mgr.handleAgentSwitchCommand(context.Background(), parseAgentSwitchCommand(msg.Content), msg); err != nil {
		t.Fatalf("handleAgentSwitchCommand error: %v", err)
	}

	select {
	case outbound := <-sub.Channel:
		if outbound == nil {
			t.Fatalf("expected outbound query reply")
		}
		want := "当前会话使用的 Agent：`main`（绑定）"
		if outbound.Content != want {
			t.Fatalf("unexpected query reply: %q, want %q", outbound.Content, want)
		}
	case <-time.After(2 * time.Second):
		t.Fatalf("timed out waiting for query reply")
	}
}

func TestParseAgentSwitchCommand_StripsInvisibleUnicode(t *testing.T) {
	cmd := parseAgentSwitchCommand("/agent default\u2060")
	if !cmd.IsSwitch || cmd.IsClear || cmd.AgentID != "default" {
		t.Fatalf("expected invisible-char default command to target explicit default agent, got %+v", cmd)
	}

	cmd = parseAgentSwitchCommand("/agent re\u2060viewer")
	if !cmd.IsSwitch || cmd.AgentID != "reviewer" {
		t.Fatalf("expected invisible-char agent id normalized, got %+v", cmd)
	}
}
