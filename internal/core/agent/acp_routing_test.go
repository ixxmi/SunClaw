package agent

import (
	"context"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/smallnest/goclaw/internal/core/acp"
	acpruntime "github.com/smallnest/goclaw/internal/core/acp/runtime"
	"github.com/smallnest/goclaw/internal/core/bus"
	"github.com/smallnest/goclaw/internal/core/channels"
	"github.com/smallnest/goclaw/internal/core/config"
)

type testRouteRuntime struct {
	backendID string
}

func (r *testRouteRuntime) EnsureSession(ctx context.Context, input acpruntime.AcpRuntimeEnsureInput) (acpruntime.AcpRuntimeHandle, error) {
	return acpruntime.AcpRuntimeHandle{
		SessionKey:         input.SessionKey,
		Backend:            r.backendID,
		RuntimeSessionName: "route-test-session",
		Cwd:                input.Cwd,
		BackendSessionId:   "route-test-backend-session",
	}, nil
}

func (r *testRouteRuntime) RunTurn(ctx context.Context, input acpruntime.AcpRuntimeTurnInput) (<-chan acpruntime.AcpRuntimeEvent, error) {
	ch := make(chan acpruntime.AcpRuntimeEvent, 2)
	go func() {
		defer close(ch)
		ch <- &acpruntime.AcpEventTextDelta{Text: "acp:" + input.Text, Stream: "output"}
		ch <- &acpruntime.AcpEventDone{StopReason: "completed"}
	}()
	return ch, nil
}

func (r *testRouteRuntime) GetCapabilities(ctx context.Context, handle *acpruntime.AcpRuntimeHandle) (acpruntime.AcpRuntimeCapabilities, error) {
	return acpruntime.AcpRuntimeCapabilities{}, nil
}
func (r *testRouteRuntime) GetStatus(ctx context.Context, handle acpruntime.AcpRuntimeHandle) (*acpruntime.AcpRuntimeStatus, error) {
	return &acpruntime.AcpRuntimeStatus{Summary: "ok"}, nil
}
func (r *testRouteRuntime) SetMode(ctx context.Context, handle acpruntime.AcpRuntimeHandle, mode string) error {
	return nil
}
func (r *testRouteRuntime) SetConfigOption(ctx context.Context, handle acpruntime.AcpRuntimeHandle, key, value string) error {
	return nil
}
func (r *testRouteRuntime) Doctor(ctx context.Context) (acpruntime.AcpRuntimeDoctorReport, error) {
	return acpruntime.AcpRuntimeDoctorReport{Ok: true, Message: "ok"}, nil
}
func (r *testRouteRuntime) Cancel(ctx context.Context, handle acpruntime.AcpRuntimeHandle, reason string) error {
	return nil
}
func (r *testRouteRuntime) Close(ctx context.Context, handle acpruntime.AcpRuntimeHandle, reason string) error {
	return nil
}

type slowRouteRuntime struct {
	testRouteRuntime
	delay time.Duration
}

func (r *slowRouteRuntime) RunTurn(ctx context.Context, input acpruntime.AcpRuntimeTurnInput) (<-chan acpruntime.AcpRuntimeEvent, error) {
	ch := make(chan acpruntime.AcpRuntimeEvent, 2)
	go func() {
		defer close(ch)
		select {
		case <-ctx.Done():
			return
		case <-time.After(r.delay):
		}
		ch <- &acpruntime.AcpEventTextDelta{Text: "acp:" + input.Text, Stream: "output"}
		ch <- &acpruntime.AcpEventDone{StopReason: "completed"}
	}()
	return ch, nil
}

type errorRouteRuntime struct {
	testRouteRuntime
}

func (r *errorRouteRuntime) RunTurn(ctx context.Context, input acpruntime.AcpRuntimeTurnInput) (<-chan acpruntime.AcpRuntimeEvent, error) {
	return nil, acpruntime.NewTurnError("forced error", nil)
}

type interruptibleRouteRuntime struct {
	testRouteRuntime
	started chan string
	mu      sync.Mutex
	runs    []string
}

func (r *interruptibleRouteRuntime) RunTurn(ctx context.Context, input acpruntime.AcpRuntimeTurnInput) (<-chan acpruntime.AcpRuntimeEvent, error) {
	ch := make(chan acpruntime.AcpRuntimeEvent, 2)
	r.mu.Lock()
	r.runs = append(r.runs, input.Text)
	r.mu.Unlock()

	go func() {
		defer close(ch)
		select {
		case r.started <- input.Text:
		default:
		}

		if input.Text == "slow" {
			<-ctx.Done()
			ch <- &acpruntime.AcpEventError{
				Message:   "interrupted",
				Code:      acpruntime.ErrCodeTurnCanceled,
				Retryable: false,
			}
			return
		}

		ch <- &acpruntime.AcpEventTextDelta{Text: "acp:" + input.Text, Stream: "output"}
		ch <- &acpruntime.AcpEventDone{StopReason: "completed"}
	}()
	return ch, nil
}

func (r *interruptibleRouteRuntime) Cancel(ctx context.Context, handle acpruntime.AcpRuntimeHandle, reason string) error {
	return nil
}

type staticRouter struct {
	sessionKey string
}

func (r *staticRouter) RouteToAcpSession(channel, accountID, conversationID string) string {
	return r.sessionKey
}
func (r *staticRouter) IsACPThreadBinding(channel, accountID, conversationID string) bool {
	return r.sessionKey != ""
}

func TestHandleInboundMessageRoutesToAcpThreadSession(t *testing.T) {
	backendID := "agent-acp-route-test-backend"
	rt := &testRouteRuntime{backendID: backendID}
	if err := acpruntime.RegisterAcpRuntimeBackend(acpruntime.AcpRuntimeBackend{
		ID:      backendID,
		Runtime: rt,
		Healthy: func() bool { return true },
	}); err != nil {
		t.Fatalf("register backend: %v", err)
	}
	t.Cleanup(func() { acpruntime.UnregisterAcpRuntimeBackend(backendID) })

	cfg := &config.Config{}
	cfg.ACP.Enabled = true
	cfg.ACP.Backend = backendID
	cfg.ACP.DefaultAgent = "main"

	acpMgr := acp.NewManager(cfg)
	sessionKey := "agent:main:acp:routed"
	if _, _, err := acpMgr.InitializeSession(context.Background(), acp.InitializeSessionInput{
		Cfg:        cfg,
		SessionKey: sessionKey,
		Agent:      "main",
		Mode:       acpruntime.AcpSessionModePersistent,
	}); err != nil {
		t.Fatalf("initialize acp session: %v", err)
	}

	messageBus := bus.NewMessageBus(16)
	channelMgr := channels.NewManager(messageBus)
	channelMgr.SetAcpRouter(&staticRouter{sessionKey: sessionKey})

	manager := &AgentManager{
		bus:        messageBus,
		cfg:        cfg,
		channelMgr: channelMgr,
		acpManager: acpMgr,
	}

	sub := messageBus.SubscribeOutbound()
	defer sub.Unsubscribe()

	err := manager.handleInboundMessage(context.Background(), &bus.InboundMessage{
		ID:        "msg-1",
		Channel:   "telegram",
		AccountID: "acc-1",
		ChatID:    "thread-1",
		Content:   "hello",
		Timestamp: time.Now(),
	}, nil)
	if err != nil {
		t.Fatalf("handle inbound: %v", err)
	}

	select {
	case out := <-sub.Channel:
		if out == nil {
			t.Fatalf("expected outbound message, got nil")
		}
		if out.Content != "acp:hello" {
			t.Fatalf("unexpected outbound content: %q", out.Content)
		}
	case <-time.After(2 * time.Second):
		t.Fatalf("timed out waiting for outbound message")
	}
}

func TestHandleInboundMessageAcpRoutingIsNonBlocking(t *testing.T) {
	backendID := "agent-acp-route-nonblocking-backend"
	rt := &slowRouteRuntime{
		testRouteRuntime: testRouteRuntime{backendID: backendID},
		delay:            300 * time.Millisecond,
	}
	if err := acpruntime.RegisterAcpRuntimeBackend(acpruntime.AcpRuntimeBackend{
		ID:      backendID,
		Runtime: rt,
		Healthy: func() bool { return true },
	}); err != nil {
		t.Fatalf("register backend: %v", err)
	}
	t.Cleanup(func() { acpruntime.UnregisterAcpRuntimeBackend(backendID) })

	cfg := &config.Config{}
	cfg.ACP.Enabled = true
	cfg.ACP.Backend = backendID
	cfg.ACP.DefaultAgent = "main"

	acpMgr := acp.NewManager(cfg)
	sessionKey := "agent:main:acp:routed-nonblocking"
	if _, _, err := acpMgr.InitializeSession(context.Background(), acp.InitializeSessionInput{
		Cfg:        cfg,
		SessionKey: sessionKey,
		Agent:      "main",
		Mode:       acpruntime.AcpSessionModePersistent,
	}); err != nil {
		t.Fatalf("initialize acp session: %v", err)
	}

	messageBus := bus.NewMessageBus(16)
	channelMgr := channels.NewManager(messageBus)
	channelMgr.SetAcpRouter(&staticRouter{sessionKey: sessionKey})

	manager := &AgentManager{
		bus:        messageBus,
		cfg:        cfg,
		channelMgr: channelMgr,
		acpManager: acpMgr,
	}

	start := time.Now()
	err := manager.handleInboundMessage(context.Background(), &bus.InboundMessage{
		ID:        "msg-2",
		Channel:   "telegram",
		AccountID: "acc-1",
		ChatID:    "thread-1",
		Content:   "hello",
		Timestamp: time.Now(),
	}, nil)
	if err != nil {
		t.Fatalf("handle inbound: %v", err)
	}
	if time.Since(start) > 100*time.Millisecond {
		t.Fatalf("expected non-blocking ACP routing, took %s", time.Since(start))
	}
}

func TestHandleInboundMessageAcpRoutingPublishesErrorOnTurnStartFailure(t *testing.T) {
	backendID := "agent-acp-route-error-backend"
	rt := &errorRouteRuntime{
		testRouteRuntime: testRouteRuntime{backendID: backendID},
	}
	if err := acpruntime.RegisterAcpRuntimeBackend(acpruntime.AcpRuntimeBackend{
		ID:      backendID,
		Runtime: rt,
		Healthy: func() bool { return true },
	}); err != nil {
		t.Fatalf("register backend: %v", err)
	}
	t.Cleanup(func() { acpruntime.UnregisterAcpRuntimeBackend(backendID) })

	cfg := &config.Config{}
	cfg.ACP.Enabled = true
	cfg.ACP.Backend = backendID
	cfg.ACP.DefaultAgent = "main"

	acpMgr := acp.NewManager(cfg)
	sessionKey := "agent:main:acp:routed-error"
	if _, _, err := acpMgr.InitializeSession(context.Background(), acp.InitializeSessionInput{
		Cfg:        cfg,
		SessionKey: sessionKey,
		Agent:      "main",
		Mode:       acpruntime.AcpSessionModePersistent,
	}); err != nil {
		t.Fatalf("initialize acp session: %v", err)
	}

	messageBus := bus.NewMessageBus(16)
	channelMgr := channels.NewManager(messageBus)
	channelMgr.SetAcpRouter(&staticRouter{sessionKey: sessionKey})
	manager := &AgentManager{
		bus:        messageBus,
		cfg:        cfg,
		channelMgr: channelMgr,
		acpManager: acpMgr,
	}

	sub := messageBus.SubscribeOutbound()
	defer sub.Unsubscribe()

	err := manager.handleInboundMessage(context.Background(), &bus.InboundMessage{
		ID:        "msg-3",
		Channel:   "telegram",
		AccountID: "acc-1",
		ChatID:    "thread-1",
		Content:   "hello",
		Timestamp: time.Now(),
	}, nil)
	if err != nil {
		t.Fatalf("handle inbound: %v", err)
	}

	select {
	case out := <-sub.Channel:
		if out == nil {
			t.Fatalf("expected outbound error message, got nil")
		}
		if out.Content == "" {
			t.Fatalf("expected non-empty outbound error message")
		}
	case <-time.After(2 * time.Second):
		t.Fatalf("timed out waiting for outbound error message")
	}
}

func TestHandleInboundMessageAcpInterruptCommandCancelsActiveTurn(t *testing.T) {
	backendID := "agent-acp-route-interrupt-backend"
	rt := &interruptibleRouteRuntime{
		testRouteRuntime: testRouteRuntime{backendID: backendID},
		started:          make(chan string, 4),
	}
	if err := acpruntime.RegisterAcpRuntimeBackend(acpruntime.AcpRuntimeBackend{
		ID:      backendID,
		Runtime: rt,
		Healthy: func() bool { return true },
	}); err != nil {
		t.Fatalf("register backend: %v", err)
	}
	t.Cleanup(func() { acpruntime.UnregisterAcpRuntimeBackend(backendID) })

	cfg := &config.Config{}
	cfg.ACP.Enabled = true
	cfg.ACP.Backend = backendID
	cfg.ACP.DefaultAgent = "main"

	acpMgr := acp.NewManager(cfg)
	sessionKey := "agent:main:acp:routed-interrupt"
	if _, _, err := acpMgr.InitializeSession(context.Background(), acp.InitializeSessionInput{
		Cfg:        cfg,
		SessionKey: sessionKey,
		Agent:      "main",
		Mode:       acpruntime.AcpSessionModePersistent,
	}); err != nil {
		t.Fatalf("initialize acp session: %v", err)
	}

	messageBus := bus.NewMessageBus(16)
	channelMgr := channels.NewManager(messageBus)
	channelMgr.SetAcpRouter(&staticRouter{sessionKey: sessionKey})
	manager := &AgentManager{
		bus:           messageBus,
		cfg:           cfg,
		channelMgr:    channelMgr,
		acpManager:    acpMgr,
		acpThreadRuns: make(map[string]*acpThreadSessionControl),
	}

	sub := messageBus.SubscribeOutbound()
	defer sub.Unsubscribe()

	if err := manager.handleInboundMessage(context.Background(), &bus.InboundMessage{
		ID:        "msg-interrupt-1",
		Channel:   "telegram",
		AccountID: "acc-1",
		ChatID:    "thread-1",
		Content:   "slow",
		Timestamp: time.Now(),
	}, nil); err != nil {
		t.Fatalf("handle slow inbound: %v", err)
	}

	select {
	case started := <-rt.started:
		if started != "slow" {
			t.Fatalf("expected slow run to start, got %q", started)
		}
	case <-time.After(2 * time.Second):
		t.Fatalf("timed out waiting for slow run to start")
	}

	if err := manager.handleInboundMessage(context.Background(), &bus.InboundMessage{
		ID:        "msg-interrupt-2",
		Channel:   "telegram",
		AccountID: "acc-1",
		ChatID:    "thread-1",
		Content:   "/interrupt",
		Timestamp: time.Now(),
	}, nil); err != nil {
		t.Fatalf("handle interrupt inbound: %v", err)
	}

	select {
	case out := <-sub.Channel:
		if out == nil {
			t.Fatalf("expected outbound interrupt ack, got nil")
		}
		if !strings.Contains(out.Content, "正在中断当前任务") {
			t.Fatalf("unexpected interrupt ack: %q", out.Content)
		}
	case <-time.After(2 * time.Second):
		t.Fatalf("timed out waiting for interrupt ack")
	}

	select {
	case out := <-sub.Channel:
		t.Fatalf("unexpected extra outbound after interrupt: %+v", out)
	case <-time.After(300 * time.Millisecond):
	}
}

func TestHandleInboundMessageAcpFollowUpInterruptsAndContinues(t *testing.T) {
	backendID := "agent-acp-route-followup-backend"
	rt := &interruptibleRouteRuntime{
		testRouteRuntime: testRouteRuntime{backendID: backendID},
		started:          make(chan string, 4),
	}
	if err := acpruntime.RegisterAcpRuntimeBackend(acpruntime.AcpRuntimeBackend{
		ID:      backendID,
		Runtime: rt,
		Healthy: func() bool { return true },
	}); err != nil {
		t.Fatalf("register backend: %v", err)
	}
	t.Cleanup(func() { acpruntime.UnregisterAcpRuntimeBackend(backendID) })

	cfg := &config.Config{}
	cfg.ACP.Enabled = true
	cfg.ACP.Backend = backendID
	cfg.ACP.DefaultAgent = "main"

	acpMgr := acp.NewManager(cfg)
	sessionKey := "agent:main:acp:routed-followup"
	if _, _, err := acpMgr.InitializeSession(context.Background(), acp.InitializeSessionInput{
		Cfg:        cfg,
		SessionKey: sessionKey,
		Agent:      "main",
		Mode:       acpruntime.AcpSessionModePersistent,
	}); err != nil {
		t.Fatalf("initialize acp session: %v", err)
	}

	messageBus := bus.NewMessageBus(16)
	channelMgr := channels.NewManager(messageBus)
	channelMgr.SetAcpRouter(&staticRouter{sessionKey: sessionKey})
	manager := &AgentManager{
		bus:           messageBus,
		cfg:           cfg,
		channelMgr:    channelMgr,
		acpManager:    acpMgr,
		acpThreadRuns: make(map[string]*acpThreadSessionControl),
	}

	sub := messageBus.SubscribeOutbound()
	defer sub.Unsubscribe()

	if err := manager.handleInboundMessage(context.Background(), &bus.InboundMessage{
		ID:        "msg-followup-1",
		Channel:   "telegram",
		AccountID: "acc-1",
		ChatID:    "thread-1",
		Content:   "slow",
		Timestamp: time.Now(),
	}, nil); err != nil {
		t.Fatalf("handle slow inbound: %v", err)
	}

	select {
	case started := <-rt.started:
		if started != "slow" {
			t.Fatalf("expected slow run to start, got %q", started)
		}
	case <-time.After(2 * time.Second):
		t.Fatalf("timed out waiting for slow run to start")
	}

	if err := manager.handleInboundMessage(context.Background(), &bus.InboundMessage{
		ID:        "msg-followup-2",
		Channel:   "telegram",
		AccountID: "acc-1",
		ChatID:    "thread-1",
		Content:   "follow up",
		Timestamp: time.Now(),
	}, nil); err != nil {
		t.Fatalf("handle follow-up inbound: %v", err)
	}

	select {
	case out := <-sub.Channel:
		if out == nil {
			t.Fatalf("expected outbound follow-up ack, got nil")
		}
		if !strings.Contains(out.Content, "收到补充说明") {
			t.Fatalf("unexpected follow-up ack: %q", out.Content)
		}
	case <-time.After(2 * time.Second):
		t.Fatalf("timed out waiting for follow-up ack")
	}

	select {
	case started := <-rt.started:
		if started != "follow up" {
			t.Fatalf("expected follow-up run to start, got %q", started)
		}
	case <-time.After(2 * time.Second):
		t.Fatalf("timed out waiting for follow-up run to start")
	}

	select {
	case out := <-sub.Channel:
		if out == nil {
			t.Fatalf("expected outbound follow-up reply, got nil")
		}
		if out.Content != "acp:follow up" {
			t.Fatalf("unexpected follow-up reply: %q", out.Content)
		}
	case <-time.After(2 * time.Second):
		t.Fatalf("timed out waiting for follow-up reply")
	}

	select {
	case out := <-sub.Channel:
		t.Fatalf("unexpected stale outbound after follow-up: %+v", out)
	case <-time.After(300 * time.Millisecond):
	}
}

func TestHandleInboundMessageAcpResumeCommandStartsTurn(t *testing.T) {
	backendID := "agent-acp-route-resume-backend"
	rt := &interruptibleRouteRuntime{
		testRouteRuntime: testRouteRuntime{backendID: backendID},
		started:          make(chan string, 4),
	}
	if err := acpruntime.RegisterAcpRuntimeBackend(acpruntime.AcpRuntimeBackend{
		ID:      backendID,
		Runtime: rt,
		Healthy: func() bool { return true },
	}); err != nil {
		t.Fatalf("register backend: %v", err)
	}
	t.Cleanup(func() { acpruntime.UnregisterAcpRuntimeBackend(backendID) })

	cfg := &config.Config{}
	cfg.ACP.Enabled = true
	cfg.ACP.Backend = backendID
	cfg.ACP.DefaultAgent = "main"

	acpMgr := acp.NewManager(cfg)
	sessionKey := "agent:main:acp:routed-resume"
	if _, _, err := acpMgr.InitializeSession(context.Background(), acp.InitializeSessionInput{
		Cfg:        cfg,
		SessionKey: sessionKey,
		Agent:      "main",
		Mode:       acpruntime.AcpSessionModePersistent,
	}); err != nil {
		t.Fatalf("initialize acp session: %v", err)
	}

	messageBus := bus.NewMessageBus(16)
	channelMgr := channels.NewManager(messageBus)
	channelMgr.SetAcpRouter(&staticRouter{sessionKey: sessionKey})
	manager := &AgentManager{
		bus:           messageBus,
		cfg:           cfg,
		channelMgr:    channelMgr,
		acpManager:    acpMgr,
		acpThreadRuns: make(map[string]*acpThreadSessionControl),
	}

	sub := messageBus.SubscribeOutbound()
	defer sub.Unsubscribe()

	if err := manager.handleInboundMessage(context.Background(), &bus.InboundMessage{
		ID:        "msg-resume-1",
		Channel:   "telegram",
		AccountID: "acc-1",
		ChatID:    "thread-1",
		Content:   "/resume review status",
		Timestamp: time.Now(),
	}, nil); err != nil {
		t.Fatalf("handle resume inbound: %v", err)
	}

	select {
	case started := <-rt.started:
		if started != "review status" {
			t.Fatalf("expected resume text to be used, got %q", started)
		}
	case <-time.After(2 * time.Second):
		t.Fatalf("timed out waiting for resume run to start")
	}

	select {
	case out := <-sub.Channel:
		if out == nil {
			t.Fatalf("expected outbound resume reply, got nil")
		}
		if out.Content != "acp:review status" {
			t.Fatalf("unexpected resume reply: %q", out.Content)
		}
	case <-time.After(2 * time.Second):
		t.Fatalf("timed out waiting for resume reply")
	}
}
