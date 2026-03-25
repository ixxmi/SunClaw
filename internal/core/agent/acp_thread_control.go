package agent

import (
	"context"
	"strings"

	"github.com/google/uuid"
	"github.com/smallnest/goclaw/internal/core/acp"
	"github.com/smallnest/goclaw/internal/core/bus"
)

type acpThreadControlAction string

const (
	acpThreadControlNone      acpThreadControlAction = ""
	acpThreadControlInterrupt acpThreadControlAction = "interrupt"
	acpThreadControlResume    acpThreadControlAction = "resume"
)

type acpThreadControlCommand struct {
	Action acpThreadControlAction
	Text   string
}

type acpQueuedTurn struct {
	msg  *bus.InboundMessage
	text string
}

type acpThreadRun struct {
	requestID     string
	done          chan struct{}
	suppressReply bool
}

type acpThreadSessionControl struct {
	current    *acpThreadRun
	queued     *acpQueuedTurn
	cancelling bool
}

func parseAcpThreadControlCommand(content string) acpThreadControlCommand {
	trimmed := sanitizeSlashCommandContent(content)
	if trimmed == "" {
		return acpThreadControlCommand{}
	}

	parts := strings.Fields(trimmed)
	if len(parts) == 0 {
		return acpThreadControlCommand{}
	}

	switch strings.ToLower(parts[0]) {
	case "/interrupt", "/stop", "/cancel":
		return acpThreadControlCommand{Action: acpThreadControlInterrupt}
	case "/resume", "/continue":
		text := strings.TrimSpace(strings.TrimPrefix(trimmed, parts[0]))
		return acpThreadControlCommand{Action: acpThreadControlResume, Text: text}
	default:
		return acpThreadControlCommand{}
	}
}

func (m *AgentManager) ensureAcpThreadControls() {
	m.acpThreadRunsMu.Lock()
	defer m.acpThreadRunsMu.Unlock()
	if m.acpThreadRuns == nil {
		m.acpThreadRuns = make(map[string]*acpThreadSessionControl)
	}
}

func (m *AgentManager) beginAcpThreadTurn(sessionKey string) string {
	m.acpThreadRunsMu.Lock()
	defer m.acpThreadRunsMu.Unlock()

	if m.acpThreadRuns == nil {
		m.acpThreadRuns = make(map[string]*acpThreadSessionControl)
	}

	control := m.acpThreadRuns[sessionKey]
	if control == nil {
		control = &acpThreadSessionControl{}
		m.acpThreadRuns[sessionKey] = control
	}

	requestID := uuid.NewString()
	control.current = &acpThreadRun{
		requestID: requestID,
		done:      make(chan struct{}),
	}
	control.cancelling = false
	return requestID
}

func (m *AgentManager) queueAcpThreadTurn(sessionKey string, msg *bus.InboundMessage, text string) (needCancel bool) {
	m.acpThreadRunsMu.Lock()
	defer m.acpThreadRunsMu.Unlock()

	control := m.acpThreadRuns[sessionKey]
	if control == nil {
		return false
	}

	if control.current != nil {
		control.current.suppressReply = true
	}
	control.queued = &acpQueuedTurn{msg: msg, text: text}
	if !control.cancelling {
		control.cancelling = true
		return true
	}
	return false
}

func (m *AgentManager) interruptAcpThreadTurn(sessionKey string) (active bool, needCancel bool) {
	m.acpThreadRunsMu.Lock()
	defer m.acpThreadRunsMu.Unlock()

	control := m.acpThreadRuns[sessionKey]
	if control == nil || control.current == nil {
		return false, false
	}

	control.current.suppressReply = true
	control.queued = nil
	if !control.cancelling {
		control.cancelling = true
		return true, true
	}
	return true, false
}

func (m *AgentManager) finishAcpThreadTurn(sessionKey, requestID string) (publishReply bool, next *acpQueuedTurn, nextRequestID string) {
	m.acpThreadRunsMu.Lock()
	defer m.acpThreadRunsMu.Unlock()

	control := m.acpThreadRuns[sessionKey]
	if control == nil || control.current == nil || control.current.requestID != requestID {
		return false, nil, ""
	}

	current := control.current
	publishReply = !current.suppressReply
	control.current = nil
	control.cancelling = false
	close(current.done)

	if control.queued != nil {
		next = control.queued
		control.queued = nil
		nextRequestID = uuid.NewString()
		control.current = &acpThreadRun{
			requestID: nextRequestID,
			done:      make(chan struct{}),
		}
	}

	if control.current == nil && control.queued == nil {
		delete(m.acpThreadRuns, sessionKey)
	}

	return publishReply, next, nextRequestID
}

func (m *AgentManager) enqueueAcpThreadTurn(ctx context.Context, sessionKey string, msg *bus.InboundMessage, text string, explicitResume bool) {
	if strings.TrimSpace(text) == "" {
		m.publishAcpThreadBindingText(ctx, msg, "请直接发送新的要求，或使用 `/resume <内容>`。")
		return
	}

	m.ensureAcpThreadControls()

	m.acpThreadRunsMu.Lock()
	control := m.acpThreadRuns[sessionKey]
	idle := control == nil || control.current == nil
	m.acpThreadRunsMu.Unlock()

	if idle {
		requestID := m.beginAcpThreadTurn(sessionKey)
		go m.runAcpThreadBindingTurn(ctx, sessionKey, requestID, text, msg)
		return
	}

	needCancel := m.queueAcpThreadTurn(sessionKey, msg, text)
	if explicitResume {
		m.publishAcpThreadBindingText(ctx, msg, "收到继续说明，先中断当前任务，随后继续处理。")
	} else {
		m.publishAcpThreadBindingText(ctx, msg, "收到补充说明，先中断当前任务，随后继续处理。")
	}
	if needCancel {
		go m.cancelAcpThreadTurn(sessionKey, "Superseded by new channel input")
	}
}

func (m *AgentManager) handleAcpThreadInterrupt(ctx context.Context, sessionKey string, msg *bus.InboundMessage) {
	m.ensureAcpThreadControls()
	active, needCancel := m.interruptAcpThreadTurn(sessionKey)
	if !active {
		m.publishAcpThreadBindingText(ctx, msg, "当前没有运行中的任务。")
		return
	}

	m.publishAcpThreadBindingText(ctx, msg, "正在中断当前任务。中断完成后，直接发送新消息即可继续。")
	if needCancel {
		go m.cancelAcpThreadTurn(sessionKey, "Interrupted by channel command")
	}
}

func (m *AgentManager) cancelAcpThreadTurn(sessionKey, reason string) {
	if m.acpManager == nil || m.cfg == nil || strings.TrimSpace(sessionKey) == "" {
		return
	}
	_ = m.acpManager.CancelSession(context.Background(), acp.CancelSessionInput{
		Cfg:        m.cfg,
		SessionKey: sessionKey,
		Reason:     reason,
	})
}
