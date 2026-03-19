package channels

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/gorilla/websocket"
	"github.com/smallnest/goclaw/bus"
	"github.com/smallnest/goclaw/internal/logger"
	"go.uber.org/zap"
)

const (
	weworkLongConnReplyKindMessage = "message"
	weworkLongConnReplyKindWelcome = "welcome"
)

var errWeWorkDisconnectedEvent = fmt.Errorf("wework websocket disconnected by newer connection")

type weworkLongConnState struct {
	conn      *websocket.Conn
	connMu    sync.RWMutex
	writeMu   sync.Mutex
	ackMu     sync.Mutex
	pending   map[string]chan weworkLongConnResponse
	contextMu sync.Mutex
	byMsgID   map[string]*weworkReplyContext
	byChatID  map[string][]*weworkReplyContext
	chatTypes map[string]uint32
}

type weworkLongConnHeaders struct {
	ReqID string `json:"req_id"`
}

type weworkLongConnFrame struct {
	Cmd     string                `json:"cmd,omitempty"`
	Headers weworkLongConnHeaders `json:"headers"`
	Body    json.RawMessage       `json:"body,omitempty"`
	ErrCode int                   `json:"errcode"`
	ErrMsg  string                `json:"errmsg"`
}

type weworkLongConnResponse struct {
	ErrCode int
	ErrMsg  string
}

type weworkReplyContext struct {
	MessageID string
	ReqID     string
	Kind      string
	ChatID    string
	ChatType  string
	CreatedAt time.Time
}

func newWeWorkLongConnState() *weworkLongConnState {
	return &weworkLongConnState{
		pending:   make(map[string]chan weworkLongConnResponse),
		byMsgID:   make(map[string]*weworkReplyContext),
		byChatID:  make(map[string][]*weworkReplyContext),
		chatTypes: make(map[string]uint32),
	}
}

func (s *weworkLongConnState) setConn(conn *websocket.Conn) {
	s.connMu.Lock()
	defer s.connMu.Unlock()
	s.conn = conn
}

func (s *weworkLongConnState) getConn() *websocket.Conn {
	s.connMu.RLock()
	defer s.connMu.RUnlock()
	return s.conn
}

func (s *weworkLongConnState) closeConn() {
	s.connMu.Lock()
	conn := s.conn
	s.conn = nil
	s.connMu.Unlock()

	if conn != nil {
		_ = conn.Close()
	}
}

func (s *weworkLongConnState) registerAck(reqID string) chan weworkLongConnResponse {
	s.ackMu.Lock()
	defer s.ackMu.Unlock()

	ch := make(chan weworkLongConnResponse, 1)
	s.pending[reqID] = ch
	return ch
}

func (s *weworkLongConnState) unregisterAck(reqID string) {
	s.ackMu.Lock()
	ch, ok := s.pending[reqID]
	if ok {
		delete(s.pending, reqID)
	}
	s.ackMu.Unlock()

	if ok {
		close(ch)
	}
}

func (s *weworkLongConnState) resolveAck(reqID string, resp weworkLongConnResponse) bool {
	s.ackMu.Lock()
	ch, ok := s.pending[reqID]
	if ok {
		delete(s.pending, reqID)
	}
	s.ackMu.Unlock()

	if !ok {
		return false
	}

	ch <- resp
	close(ch)
	return true
}

func (s *weworkLongConnState) storeReplyContext(ctx *weworkReplyContext) {
	if ctx == nil || ctx.MessageID == "" || ctx.ChatID == "" {
		return
	}

	s.contextMu.Lock()
	defer s.contextMu.Unlock()

	s.byMsgID[ctx.MessageID] = ctx
	s.byChatID[ctx.ChatID] = append(s.byChatID[ctx.ChatID], ctx)

	switch ctx.ChatType {
	case "single":
		s.chatTypes[ctx.ChatID] = 1
	case "group":
		s.chatTypes[ctx.ChatID] = 2
	}
}

func (s *weworkLongConnState) popReplyContext(replyTo, chatID string) *weworkReplyContext {
	s.contextMu.Lock()
	defer s.contextMu.Unlock()

	if replyTo != "" {
		if ctx, ok := s.byMsgID[replyTo]; ok {
			delete(s.byMsgID, replyTo)
			s.removeFromChatQueueLocked(ctx.ChatID, ctx.MessageID)
			return ctx
		}
	}

	queue := s.byChatID[chatID]
	if len(queue) == 0 {
		return nil
	}

	ctx := queue[0]
	s.byChatID[chatID] = queue[1:]
	if len(s.byChatID[chatID]) == 0 {
		delete(s.byChatID, chatID)
	}
	delete(s.byMsgID, ctx.MessageID)
	return ctx
}

func (s *weworkLongConnState) removeFromChatQueueLocked(chatID, messageID string) {
	queue := s.byChatID[chatID]
	if len(queue) == 0 {
		return
	}

	filtered := queue[:0]
	for _, item := range queue {
		if item.MessageID != messageID {
			filtered = append(filtered, item)
		}
	}
	if len(filtered) == 0 {
		delete(s.byChatID, chatID)
		return
	}
	s.byChatID[chatID] = filtered
}

func (s *weworkLongConnState) resolveChatType(chatID string, metadata map[string]interface{}) uint32 {
	if metadata != nil {
		switch v := metadata["chat_type"].(type) {
		case string:
			switch strings.ToLower(strings.TrimSpace(v)) {
			case "single":
				return 1
			case "group":
				return 2
			}
		case int:
			if v == 1 || v == 2 {
				return uint32(v)
			}
		case float64:
			if v == 1 || v == 2 {
				return uint32(v)
			}
		}
	}

	s.contextMu.Lock()
	defer s.contextMu.Unlock()
	return s.chatTypes[chatID]
}

func (c *WeWorkChannel) startLongConnLoop(ctx context.Context) {
	for {
		select {
		case <-ctx.Done():
			return
		case <-c.WaitForStop():
			return
		default:
		}

		if err := c.runLongConnSession(ctx); err != nil && ctx.Err() == nil {
			logger.Warn("WeWork websocket session ended, retrying",
				zap.Error(err),
				zap.String("account_id", c.AccountID()))
		}

		select {
		case <-ctx.Done():
			return
		case <-c.WaitForStop():
			return
		case <-time.After(5 * time.Second):
		}
	}
}

func (c *WeWorkChannel) runLongConnSession(ctx context.Context) error {
	dialer := websocket.Dialer{
		HandshakeTimeout: 15 * time.Second,
	}

	conn, _, err := dialer.DialContext(ctx, c.webSocketURL, nil)
	if err != nil {
		return fmt.Errorf("dial wework websocket failed: %w", err)
	}

	logger.Info("WeWork websocket connected",
		zap.String("url", c.webSocketURL),
		zap.String("account_id", c.AccountID()))

	return c.runLongConnSessionWithConn(ctx, conn)
}

func (c *WeWorkChannel) runLongConnSessionWithConn(ctx context.Context, conn *websocket.Conn) error {
	c.longConn.setConn(conn)
	defer c.longConn.closeConn()

	sessionCtx, cancel := context.WithCancel(ctx)
	defer cancel()

	errCh := make(chan error, 2)
	go func() {
		errCh <- c.readLongConnLoop(sessionCtx, conn)
	}()

	if err := c.sendLongConnCommand(ctx, "aibot_subscribe", "", map[string]interface{}{
		"bot_id": c.botID,
		"secret": c.botSecret,
	}, true); err != nil {
		return fmt.Errorf("wework websocket subscribe failed: %w", err)
	}

	logger.Info("WeWork websocket subscribed",
		zap.String("bot_id", c.botID),
		zap.String("account_id", c.AccountID()))

	go func() {
		errCh <- c.pingLongConnLoop(sessionCtx)
	}()

	select {
	case err := <-errCh:
		return err
	case <-ctx.Done():
		return nil
	case <-c.WaitForStop():
		return nil
	}
}

func (c *WeWorkChannel) readLongConnLoop(ctx context.Context, conn *websocket.Conn) error {
	for {
		select {
		case <-ctx.Done():
			return nil
		default:
		}

		_, data, err := conn.ReadMessage()
		if err != nil {
			return fmt.Errorf("read wework websocket message failed: %w", err)
		}

		var frame weworkLongConnFrame
		if err := json.Unmarshal(data, &frame); err != nil {
			logger.Warn("Failed to decode WeWork websocket frame",
				zap.Error(err),
				zap.String("payload", truncateWeWorkLog(string(data), 300)))
			continue
		}

		if frame.Cmd == "" {
			if frame.Headers.ReqID != "" {
				c.longConn.resolveAck(frame.Headers.ReqID, weworkLongConnResponse{
					ErrCode: frame.ErrCode,
					ErrMsg:  frame.ErrMsg,
				})
			}
			continue
		}

		switch frame.Cmd {
		case "aibot_msg_callback":
			c.handleLongConnMessageCallback(frame.Headers.ReqID, frame.Body)
		case "aibot_event_callback":
			if err := c.handleLongConnEventCallback(frame.Headers.ReqID, frame.Body); err != nil {
				return err
			}
		default:
			logger.Debug("Ignoring unsupported WeWork websocket command",
				zap.String("cmd", frame.Cmd),
				zap.String("req_id", frame.Headers.ReqID))
		}
	}
}

func (c *WeWorkChannel) pingLongConnLoop(ctx context.Context) error {
	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return nil
		case <-ticker.C:
			if err := c.sendLongConnCommand(ctx, "ping", "", nil, true); err != nil {
				return fmt.Errorf("wework websocket ping failed: %w", err)
			}
		}
	}
}

func (c *WeWorkChannel) sendLongConnCommand(ctx context.Context, cmd, reqID string, body interface{}, waitAck bool) error {
	if reqID == "" {
		reqID = uuid.NewString()
	}

	payload := map[string]interface{}{
		"cmd": cmd,
		"headers": map[string]string{
			"req_id": reqID,
		},
	}
	if body != nil {
		payload["body"] = body
	}

	var ackCh chan weworkLongConnResponse
	if waitAck {
		ackCh = c.longConn.registerAck(reqID)
	}

	conn := c.longConn.getConn()
	if conn == nil {
		if waitAck {
			c.longConn.unregisterAck(reqID)
		}
		return fmt.Errorf("wework websocket connection is not established")
	}

	c.longConn.writeMu.Lock()
	_ = conn.SetWriteDeadline(time.Now().Add(10 * time.Second))
	err := conn.WriteJSON(payload)
	c.longConn.writeMu.Unlock()
	if err != nil {
		if waitAck {
			c.longConn.unregisterAck(reqID)
		}
		return fmt.Errorf("write wework websocket command failed: %w", err)
	}

	if !waitAck {
		return nil
	}

	timer := time.NewTimer(10 * time.Second)
	defer timer.Stop()

	select {
	case ack, ok := <-ackCh:
		if !ok {
			return fmt.Errorf("wework websocket ack channel closed")
		}
		if ack.ErrCode != 0 {
			return fmt.Errorf("wework websocket command %s failed: %d %s", cmd, ack.ErrCode, ack.ErrMsg)
		}
		return nil
	case <-ctx.Done():
		c.longConn.unregisterAck(reqID)
		return ctx.Err()
	case <-timer.C:
		c.longConn.unregisterAck(reqID)
		return fmt.Errorf("wework websocket command %s timed out", cmd)
	}
}

func (c *WeWorkChannel) handleLongConnMessageCallback(reqID string, raw json.RawMessage) {
	var msg weworkAiBotMessage
	if err := json.Unmarshal(raw, &msg); err != nil {
		logger.Error("Failed to decode WeWork websocket message callback",
			zap.Error(err),
			zap.String("payload", truncateWeWorkLog(string(raw), 300)))
		return
	}

	if msg.MsgType == "stream" {
		return
	}

	content := extractAiBotTextContent(&msg)
	if content == "" {
		return
	}

	chatID := resolveWeWorkChatID(&msg)
	if chatID == "" {
		return
	}

	if msg.From.UserID != "" && !c.IsAllowed(msg.From.UserID) {
		return
	}

	c.longConn.storeReplyContext(&weworkReplyContext{
		MessageID: msg.MsgID,
		ReqID:     reqID,
		Kind:      weworkLongConnReplyKindMessage,
		ChatID:    chatID,
		ChatType:  msg.ChatType,
		CreatedAt: time.Now(),
	})

	inMsg := &bus.InboundMessage{
		ID:        msg.MsgID,
		Content:   content,
		SenderID:  msg.From.UserID,
		ChatID:    chatID,
		Channel:   c.Name(),
		AccountID: c.AccountID(),
		Timestamp: weworkMessageTime(msg.CreateTime),
		Metadata: map[string]interface{}{
			"aibot_id":  msg.AiBotID,
			"chat_type": msg.ChatType,
			"msg_type":  msg.MsgType,
			"req_id":    reqID,
		},
	}

	if err := c.PublishInbound(context.Background(), inMsg); err != nil {
		logger.Error("Failed to publish WeWork websocket inbound message",
			zap.Error(err),
			zap.String("msgid", msg.MsgID))
	}
}

func (c *WeWorkChannel) handleLongConnEventCallback(reqID string, raw json.RawMessage) error {
	var msg weworkAiBotMessage
	if err := json.Unmarshal(raw, &msg); err != nil {
		return fmt.Errorf("decode wework websocket event callback failed: %w", err)
	}

	eventType := weworkEventType(msg.Event)
	if eventType == "" {
		logger.Debug("Ignoring WeWork event callback without eventtype",
			zap.String("req_id", reqID))
		return nil
	}

	if eventType == "disconnected_event" {
		logger.Warn("WeWork websocket disconnected by server",
			zap.String("bot_id", c.botID),
			zap.String("msgid", msg.MsgID))
		return errWeWorkDisconnectedEvent
	}

	if msg.From.UserID != "" && !c.IsAllowed(msg.From.UserID) {
		return nil
	}

	chatID := resolveWeWorkChatID(&msg)
	content := formatWeWorkEventPrompt(eventType)
	if content == "" || chatID == "" {
		return nil
	}

	replyKind := ""
	if eventType == "enter_chat" {
		replyKind = weworkLongConnReplyKindWelcome
		c.longConn.storeReplyContext(&weworkReplyContext{
			MessageID: msg.MsgID,
			ReqID:     reqID,
			Kind:      replyKind,
			ChatID:    chatID,
			ChatType:  msg.ChatType,
			CreatedAt: time.Now(),
		})
	}

	inMsg := &bus.InboundMessage{
		ID:        msg.MsgID,
		Content:   content,
		SenderID:  msg.From.UserID,
		ChatID:    chatID,
		Channel:   c.Name(),
		AccountID: c.AccountID(),
		Timestamp: weworkMessageTime(msg.CreateTime),
		Metadata: map[string]interface{}{
			"aibot_id":   msg.AiBotID,
			"chat_type":  msg.ChatType,
			"msg_type":   msg.MsgType,
			"req_id":     reqID,
			"event_type": eventType,
			"event":      msg.Event,
			"reply_kind": replyKind,
		},
	}

	if err := c.PublishInbound(context.Background(), inMsg); err != nil {
		logger.Error("Failed to publish WeWork websocket event message",
			zap.Error(err),
			zap.String("msgid", msg.MsgID),
			zap.String("event_type", eventType))
	}

	return nil
}

func (c *WeWorkChannel) sendLongConnMessage(msg *bus.OutboundMessage) error {
	content := AppendMediaURLsToContent(msg.Content, msg.Media, map[string]bool{
		UnifiedMediaImage: true,
		UnifiedMediaFile:  true,
		UnifiedMediaVideo: true,
		UnifiedMediaAudio: true,
	})

	replyCtx := c.longConn.popReplyContext(msg.ReplyTo, msg.ChatID)
	if customCmd, customBody, ok := resolveWeWorkCustomSend(msg.Metadata); ok {
		reqID := ""
		if replyCtx != nil {
			reqID = replyCtx.ReqID
		}
		return c.sendLongConnCommand(context.Background(), customCmd, reqID, customBody, true)
	}

	if replyCtx != nil {
		switch replyCtx.Kind {
		case weworkLongConnReplyKindWelcome:
			return c.sendLongConnCommand(context.Background(), "aibot_respond_welcome_msg", replyCtx.ReqID, map[string]interface{}{
				"msgtype": "text",
				"text": map[string]string{
					"content": content,
				},
			}, true)
		default:
			return c.sendLongConnCommand(context.Background(), "aibot_respond_msg", replyCtx.ReqID, map[string]interface{}{
				"msgtype": "markdown",
				"markdown": map[string]string{
					"content": content,
				},
			}, true)
		}
	}

	body := map[string]interface{}{
		"chatid":  msg.ChatID,
		"msgtype": "markdown",
		"markdown": map[string]string{
			"content": content,
		},
	}
	if chatType := c.longConn.resolveChatType(msg.ChatID, msg.Metadata); chatType != 0 {
		body["chat_type"] = chatType
	}

	return c.sendLongConnCommand(context.Background(), "aibot_send_msg", "", body, true)
}

func (c *WeWorkChannel) SendStream(chatID string, stream <-chan *bus.StreamMessage) error {
	if c.mode != "websocket" {
		return c.BaseChannelImpl.SendStream(chatID, stream)
	}

	replyCtx := c.longConn.popReplyContext("", chatID)
	if replyCtx == nil {
		var content strings.Builder
		for msg := range stream {
			if msg.Error != "" {
				return fmt.Errorf("stream error: %s", msg.Error)
			}
			if !msg.IsThinking && !msg.IsFinal {
				content.WriteString(msg.Content)
			}
		}
		return c.Send(&bus.OutboundMessage{
			Channel:   c.Name(),
			ChatID:    chatID,
			Content:   content.String(),
			Timestamp: time.Now(),
		})
	}

	if replyCtx.Kind == weworkLongConnReplyKindWelcome {
		var content strings.Builder
		for msg := range stream {
			if msg.Error != "" {
				return fmt.Errorf("stream error: %s", msg.Error)
			}
			if !msg.IsThinking && !msg.IsFinal {
				content.WriteString(msg.Content)
			}
		}
		return c.sendLongConnCommand(context.Background(), "aibot_respond_welcome_msg", replyCtx.ReqID, map[string]interface{}{
			"msgtype": "text",
			"text": map[string]string{
				"content": content.String(),
			},
		}, true)
	}

	streamID := uuid.NewString()
	var content strings.Builder

	for msg := range stream {
		if msg.Error != "" {
			return fmt.Errorf("stream error: %s", msg.Error)
		}
		if !msg.IsThinking && !msg.IsFinal {
			content.WriteString(msg.Content)
		}

		if err := c.sendLongConnCommand(context.Background(), "aibot_respond_msg", replyCtx.ReqID, map[string]interface{}{
			"msgtype": "stream",
			"stream": map[string]interface{}{
				"id":      streamID,
				"finish":  msg.IsComplete,
				"content": content.String(),
			},
		}, true); err != nil {
			return err
		}
	}

	return nil
}

func (c *WeWorkChannel) Stop() error {
	c.longConn.closeConn()
	return c.BaseChannelImpl.Stop()
}

func resolveWeWorkChatID(msg *weworkAiBotMessage) string {
	if msg == nil {
		return ""
	}
	if msg.ChatType == "group" && msg.ChatID != "" {
		return msg.ChatID
	}
	return msg.From.UserID
}

func weworkEventType(event map[string]interface{}) string {
	if event == nil {
		return ""
	}
	if v, ok := event["eventtype"].(string); ok {
		return strings.TrimSpace(v)
	}
	return ""
}

func formatWeWorkEventPrompt(eventType string) string {
	switch eventType {
	case "enter_chat":
		return "[系统事件] 用户进入了机器人会话，请回复欢迎语。"
	case "template_card_event":
		return "[系统事件] 用户触发了模板卡片事件。"
	case "feedback_event":
		return "[系统事件] 用户提交了机器人反馈。"
	default:
		return ""
	}
}

func weworkMessageTime(ts int64) time.Time {
	if ts <= 0 {
		return time.Now()
	}
	return time.Unix(ts, 0)
}

func resolveWeWorkCustomSend(metadata map[string]interface{}) (string, interface{}, bool) {
	if metadata == nil {
		return "", nil, false
	}

	cmd, _ := metadata["wework_cmd"].(string)
	if strings.TrimSpace(cmd) == "" {
		return "", nil, false
	}

	body, ok := metadata["wework_body"]
	if !ok {
		return "", nil, false
	}

	return strings.TrimSpace(cmd), body, true
}
