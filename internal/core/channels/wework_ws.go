package channels

import (
	"bytes"
	"context"
	"crypto/md5"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"image"
	_ "image/gif"
	"image/jpeg"
	"image/png"
	"net/http"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/gorilla/websocket"
	"github.com/smallnest/goclaw/internal/core/bus"
	"github.com/smallnest/goclaw/internal/logger"
	"go.uber.org/zap"
)

const (
	weworkLongConnReplyKindMessage  = "message"
	weworkLongConnReplyKindWelcome  = "welcome"
	weworkUploadChunkSize           = 512 << 10
	weworkUploadMaxChunks           = 100
	weworkUploadMinBytes            = 5
	weworkUploadImageSourceMaxBytes = 20 << 20
	weworkUploadImageMaxBytes       = 10 << 20
	weworkUploadFileMaxBytes        = 20 << 20
	weworkUploadImageSafeMaxBytes   = 2 << 20
	weworkUploadImageRetryMaxBytes  = 1 << 20
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
	Body    json.RawMessage
}

type weworkLongConnCommandError struct {
	Cmd     string
	ErrCode int
	ErrMsg  string
}

func (e *weworkLongConnCommandError) Error() string {
	if e == nil {
		return ""
	}
	return fmt.Sprintf("wework websocket command %s failed: %d %s", e.Cmd, e.ErrCode, e.ErrMsg)
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
					Body:    frame.Body,
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
	_, err := c.sendLongConnCommandWithResponse(ctx, cmd, reqID, body, waitAck)
	return err
}

func (c *WeWorkChannel) sendLongConnCommandWithResponse(ctx context.Context, cmd, reqID string, body interface{}, waitAck bool) (weworkLongConnResponse, error) {
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
		return weworkLongConnResponse{}, fmt.Errorf("wework websocket connection is not established")
	}

	c.longConn.writeMu.Lock()
	_ = conn.SetWriteDeadline(time.Now().Add(10 * time.Second))
	err := conn.WriteJSON(payload)
	c.longConn.writeMu.Unlock()
	if err != nil {
		if waitAck {
			c.longConn.unregisterAck(reqID)
		}
		return weworkLongConnResponse{}, fmt.Errorf("write wework websocket command failed: %w", err)
	}

	if !waitAck {
		return weworkLongConnResponse{}, nil
	}

	timer := time.NewTimer(10 * time.Second)
	defer timer.Stop()

	select {
	case ack, ok := <-ackCh:
		if !ok {
			return weworkLongConnResponse{}, fmt.Errorf("wework websocket ack channel closed")
		}
		if ack.ErrCode != 0 {
			return ack, &weworkLongConnCommandError{
				Cmd:     cmd,
				ErrCode: ack.ErrCode,
				ErrMsg:  ack.ErrMsg,
			}
		}
		return ack, nil
	case <-ctx.Done():
		c.longConn.unregisterAck(reqID)
		return weworkLongConnResponse{}, ctx.Err()
	case <-timer.C:
		c.longConn.unregisterAck(reqID)
		return weworkLongConnResponse{}, fmt.Errorf("wework websocket command %s timed out", cmd)
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
	replyCtx := c.longConn.popReplyContext(msg.ReplyTo, msg.ChatID)
	if customCmd, customBody, ok := resolveWeWorkCustomSend(msg.Metadata); ok {
		reqID := ""
		if replyCtx != nil {
			reqID = replyCtx.ReqID
		}
		return c.sendLongConnCommand(context.Background(), customCmd, reqID, customBody, true)
	}

	var nativeMedia []bus.Media
	var fallbackMedia []bus.Media
	for _, media := range msg.Media {
		media.Type = NormalizeMediaType(media.Type)
		switch media.Type {
		case UnifiedMediaImage, UnifiedMediaFile:
			nativeMedia = append(nativeMedia, media)
		default:
			fallbackMedia = append(fallbackMedia, media)
		}
	}

	content := AppendMediaURLsToContent(msg.Content, fallbackMedia, map[string]bool{
		UnifiedMediaImage: true,
		UnifiedMediaFile:  true,
		UnifiedMediaVideo: true,
		UnifiedMediaAudio: true,
	})

	ctx := context.Background()
	if strings.TrimSpace(content) != "" {
		if err := c.sendLongConnTextMessage(ctx, msg.ChatID, content, msg.Metadata, replyCtx); err != nil {
			return err
		}
	}

	for _, media := range nativeMedia {
		if err := c.sendLongConnMediaMessage(ctx, msg.ChatID, msg.Metadata, media, replyCtx); err != nil {
			return err
		}
	}

	if strings.TrimSpace(content) == "" && len(nativeMedia) == 0 && len(msg.Media) > 0 {
		return fmt.Errorf("wework websocket message has no sendable content or supported media")
	}

	return nil
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

func (c *WeWorkChannel) sendLongConnTextMessage(ctx context.Context, chatID, content string, metadata map[string]interface{}, replyCtx *weworkReplyContext) error {
	content = strings.TrimSpace(content)
	if content == "" {
		return nil
	}

	if replyCtx != nil {
		switch replyCtx.Kind {
		case weworkLongConnReplyKindWelcome:
			return c.sendLongConnCommand(ctx, "aibot_respond_welcome_msg", replyCtx.ReqID, map[string]interface{}{
				"msgtype": "text",
				"text": map[string]string{
					"content": content,
				},
			}, true)
		default:
			return c.sendLongConnCommand(ctx, "aibot_respond_msg", replyCtx.ReqID, map[string]interface{}{
				"msgtype": "markdown",
				"markdown": map[string]string{
					"content": content,
				},
			}, true)
		}
	}

	body := map[string]interface{}{
		"chatid":  chatID,
		"msgtype": "markdown",
		"markdown": map[string]string{
			"content": content,
		},
	}
	if chatType := c.longConn.resolveChatType(chatID, metadata); chatType != 0 {
		body["chat_type"] = chatType
	}

	return c.sendLongConnCommand(ctx, "aibot_send_msg", "", body, true)
}

func (c *WeWorkChannel) sendLongConnMediaMessage(ctx context.Context, chatID string, metadata map[string]interface{}, media bus.Media, replyCtx *weworkReplyContext) error {
	media.Type = NormalizeMediaType(media.Type)
	if media.Type != UnifiedMediaImage && media.Type != UnifiedMediaFile {
		return fmt.Errorf("unsupported wework websocket media type: %s", media.Type)
	}

	mediaID, err := c.uploadLongConnMedia(ctx, media)
	if err != nil {
		return err
	}

	body := map[string]interface{}{
		"msgtype": media.Type,
		media.Type: map[string]string{
			"media_id": mediaID,
		},
	}
	if replyCtx == nil {
		body["chatid"] = chatID
		if chatType := c.longConn.resolveChatType(chatID, metadata); chatType != 0 {
			body["chat_type"] = chatType
		}
		return c.sendLongConnCommand(ctx, "aibot_send_msg", "", body, true)
	}

	cmd := "aibot_respond_msg"
	if replyCtx.Kind == weworkLongConnReplyKindWelcome {
		cmd = "aibot_respond_welcome_msg"
	}
	return c.sendLongConnCommand(ctx, cmd, replyCtx.ReqID, body, true)
}

func (c *WeWorkChannel) uploadLongConnMedia(ctx context.Context, media bus.Media) (string, error) {
	media.Type = NormalizeMediaType(media.Type)

	maxBytes := int64(weworkUploadFileMaxBytes)
	fallbackName := "attachment"
	switch media.Type {
	case UnifiedMediaImage:
		maxBytes = weworkUploadImageSourceMaxBytes
		fallbackName = "image.jpg"
	case UnifiedMediaFile:
		maxBytes = weworkUploadFileMaxBytes
	default:
		return "", fmt.Errorf("unsupported wework websocket media type: %s", media.Type)
	}

	originalName := InferMediaFileName(media, fallbackName)
	data, err := MaterializeMediaData(c.httpClient, media, maxBytes)
	if err != nil {
		return "", fmt.Errorf("materialize wework media failed: %w", err)
	}
	originalSize := len(data)
	originalDetectedType := detectWeWorkMediaContentType(data)
	logger.Debug("Preparing WeWork media upload",
		zap.String("media_type", media.Type),
		zap.String("filename", originalName),
		zap.String("mime_type", strings.TrimSpace(media.MimeType)),
		zap.String("detected_content_type", originalDetectedType),
		zap.Int("size_bytes", originalSize),
		zap.Int64("max_bytes", maxBytes),
		zap.Bool("source_is_url", strings.TrimSpace(media.URL) != ""),
		zap.Bool("source_is_base64", strings.TrimSpace(media.Base64) != ""))

	if media.Type == UnifiedMediaImage {
		targetCaps := []int64{weworkUploadImageMaxBytes, weworkUploadImageSafeMaxBytes, weworkUploadImageRetryMaxBytes}
		var lastErr error
		seenCaps := make(map[int64]bool, len(targetCaps))
		for _, targetCap := range targetCaps {
			if seenCaps[targetCap] {
				continue
			}
			seenCaps[targetCap] = true

			preparedMedia, preparedData, prepErr := normalizeWeWorkUploadImage(media, data, targetCap)
			if prepErr != nil {
				logger.Warn("WeWork image normalization failed",
					zap.String("filename", originalName),
					zap.String("detected_content_type", originalDetectedType),
					zap.Int("size_bytes", originalSize),
					zap.Int64("target_max_bytes", targetCap),
					zap.Error(prepErr))
				lastErr = prepErr
				continue
			}
			if preparedMedia.Name != originalName || len(preparedData) != originalSize || detectWeWorkMediaContentType(preparedData) != originalDetectedType {
				logger.Info("WeWork image normalized before upload",
					zap.String("original_filename", originalName),
					zap.String("normalized_filename", InferMediaFileName(preparedMedia, fallbackName)),
					zap.String("original_content_type", originalDetectedType),
					zap.String("normalized_content_type", detectWeWorkMediaContentType(preparedData)),
					zap.Int("original_size_bytes", originalSize),
					zap.Int("normalized_size_bytes", len(preparedData)),
					zap.Int64("target_max_bytes", targetCap))
			}

			mediaID, uploadErr := c.uploadPreparedLongConnMedia(ctx, preparedMedia, preparedData, fallbackName)
			if uploadErr == nil {
				return mediaID, nil
			}

			lastErr = uploadErr
			var cmdErr *weworkLongConnCommandError
			if errors.As(uploadErr, &cmdErr) && cmdErr.ErrCode == 40009 && strings.EqualFold(strings.TrimSpace(cmdErr.Cmd), "aibot_upload_media_init") {
				logger.Warn("WeWork rejected image size during init, retrying with stricter target",
					zap.String("filename", InferMediaFileName(preparedMedia, fallbackName)),
					zap.Int("prepared_size_bytes", len(preparedData)),
					zap.Int64("target_max_bytes", targetCap),
					zap.Int("errcode", cmdErr.ErrCode),
					zap.String("errmsg", truncateWeWorkLog(cmdErr.ErrMsg, 200)))
				continue
			}

			return "", uploadErr
		}

		if lastErr != nil {
			return "", lastErr
		}
		return "", fmt.Errorf("wework image upload failed without a concrete error")
	}

	return c.uploadPreparedLongConnMedia(ctx, media, data, fallbackName)
}

func (c *WeWorkChannel) uploadPreparedLongConnMedia(ctx context.Context, media bus.Media, data []byte, fallbackName string) (string, error) {
	if err := validateWeWorkUploadMedia(media, data); err != nil {
		logger.Warn("WeWork media validation failed",
			zap.String("media_type", media.Type),
			zap.String("filename", InferMediaFileName(media, fallbackName)),
			zap.String("detected_content_type", detectWeWorkMediaContentType(data)),
			zap.Int("size_bytes", len(data)),
			zap.Error(err))
		return "", err
	}

	chunks := splitWeWorkMediaChunks(data, weworkUploadChunkSize)
	if len(chunks) == 0 {
		return "", fmt.Errorf("wework media chunking produced no data")
	}
	if len(chunks) > weworkUploadMaxChunks {
		return "", fmt.Errorf("wework media requires %d chunks, exceeds limit %d", len(chunks), weworkUploadMaxChunks)
	}
	logger.Debug("WeWork media upload validated",
		zap.String("media_type", media.Type),
		zap.String("filename", InferMediaFileName(media, fallbackName)),
		zap.String("detected_content_type", detectWeWorkMediaContentType(data)),
		zap.Int("size_bytes", len(data)),
		zap.Int("chunks", len(chunks)),
		zap.Int("chunk_size_bytes", weworkUploadChunkSize))

	checksum := md5.Sum(data)
	initResp, err := c.sendLongConnCommandWithResponse(ctx, "aibot_upload_media_init", "", map[string]interface{}{
		"type":         media.Type,
		"filename":     InferMediaFileName(media, fallbackName),
		"total_size":   len(data),
		"total_chunks": len(chunks),
		"md5":          hex.EncodeToString(checksum[:]),
	}, true)
	if err != nil {
		return "", fmt.Errorf("init wework media upload failed: %w", err)
	}

	var initBody struct {
		UploadID string `json:"upload_id"`
	}
	if err := json.Unmarshal(initResp.Body, &initBody); err != nil {
		return "", fmt.Errorf("decode wework media upload init response failed: %w", err)
	}
	if strings.TrimSpace(initBody.UploadID) == "" {
		return "", fmt.Errorf("wework media upload init response missing upload_id")
	}
	logger.Debug("WeWork media upload initialized",
		zap.String("media_type", media.Type),
		zap.String("filename", InferMediaFileName(media, fallbackName)),
		zap.String("upload_id", initBody.UploadID))

	for idx, chunk := range chunks {
		if err := c.sendLongConnCommand(ctx, "aibot_upload_media_chunk", "", map[string]interface{}{
			"upload_id":   initBody.UploadID,
			"chunk_index": idx,
			"base64_data": base64.StdEncoding.EncodeToString(chunk),
		}, true); err != nil {
			return "", fmt.Errorf("upload wework media chunk %d failed: %w", idx, err)
		}
	}

	finishResp, err := c.sendLongConnCommandWithResponse(ctx, "aibot_upload_media_finish", "", map[string]interface{}{
		"upload_id": initBody.UploadID,
	}, true)
	if err != nil {
		return "", fmt.Errorf("finish wework media upload failed: %w", err)
	}

	var finishBody struct {
		MediaID string `json:"media_id"`
	}
	if err := json.Unmarshal(finishResp.Body, &finishBody); err != nil {
		return "", fmt.Errorf("decode wework media upload finish response failed: %w", err)
	}
	if strings.TrimSpace(finishBody.MediaID) == "" {
		return "", fmt.Errorf("wework media upload finish response missing media_id")
	}
	logger.Debug("WeWork media upload finished",
		zap.String("media_type", media.Type),
		zap.String("filename", InferMediaFileName(media, fallbackName)),
		zap.String("media_id", finishBody.MediaID),
		zap.Int("size_bytes", len(data)),
		zap.Int("chunks", len(chunks)))

	return finishBody.MediaID, nil
}

func normalizeWeWorkUploadImage(media bus.Media, data []byte, maxBytes int64) (bus.Media, []byte, error) {
	detectedType := strings.ToLower(strings.TrimSpace(http.DetectContentType(data)))
	if (detectedType == "image/jpeg" || detectedType == "image/png") && (maxBytes <= 0 || int64(len(data)) <= maxBytes) {
		return media, data, nil
	}

	img, _, err := image.Decode(bytes.NewReader(data))
	if err != nil {
		return media, nil, fmt.Errorf("wework image upload only supports JPG/JPEG or PNG, and auto-conversion failed: %w", err)
	}

	type encodeAttempt struct {
		ext      string
		mimeType string
		encode   func(*bytes.Buffer, image.Image) error
	}

	attempts := []encodeAttempt{
		{
			ext:      ".png",
			mimeType: "image/png",
			encode: func(buf *bytes.Buffer, img image.Image) error {
				return png.Encode(buf, img)
			},
		},
	}
	for _, quality := range []int{90, 82, 74, 66, 58, 50, 42, 34} {
		q := quality
		attempts = append(attempts, encodeAttempt{
			ext:      ".jpg",
			mimeType: "image/jpeg",
			encode: func(buf *bytes.Buffer, img image.Image) error {
				return jpeg.Encode(buf, img, &jpeg.Options{Quality: q})
			},
		})
	}

	for _, candidate := range buildWeWorkImageCandidates(img) {
		for _, attempt := range attempts {
			var buf bytes.Buffer
			if err := attempt.encode(&buf, candidate); err != nil {
				continue
			}
			converted := buf.Bytes()
			if maxBytes > 0 && int64(len(converted)) > maxBytes {
				continue
			}
			media = updateWeWorkConvertedImageMeta(media, attempt.ext, attempt.mimeType)
			return media, converted, nil
		}
	}

	return media, nil, fmt.Errorf("wework image auto-conversion succeeded but result still exceeds %d bytes", maxBytes)
}

func updateWeWorkConvertedImageMeta(media bus.Media, ext, mimeType string) bus.Media {
	name := InferMediaFileName(media, "image")
	base := strings.TrimSuffix(name, filepath.Ext(name))
	if base == "" {
		base = "image"
	}
	media.Name = base + ext
	media.MimeType = mimeType
	return media
}

func detectWeWorkMediaContentType(data []byte) string {
	if len(data) == 0 {
		return ""
	}
	return strings.ToLower(strings.TrimSpace(http.DetectContentType(data)))
}

func buildWeWorkImageCandidates(src image.Image) []image.Image {
	bounds := src.Bounds()
	width := bounds.Dx()
	height := bounds.Dy()
	if width <= 0 || height <= 0 {
		return []image.Image{src}
	}

	candidates := []image.Image{src}
	for _, scale := range []float64{0.85, 0.7, 0.55, 0.4, 0.3, 0.2} {
		nextWidth := int(float64(width) * scale)
		nextHeight := int(float64(height) * scale)
		if nextWidth < 1 {
			nextWidth = 1
		}
		if nextHeight < 1 {
			nextHeight = 1
		}
		if nextWidth == width && nextHeight == height {
			continue
		}
		candidates = append(candidates, resizeWeWorkImageNearest(src, nextWidth, nextHeight))
	}
	return candidates
}

func resizeWeWorkImageNearest(src image.Image, width, height int) image.Image {
	if width <= 0 || height <= 0 {
		return src
	}

	srcBounds := src.Bounds()
	srcWidth := srcBounds.Dx()
	srcHeight := srcBounds.Dy()
	if srcWidth <= 0 || srcHeight <= 0 {
		return src
	}

	dst := image.NewRGBA(image.Rect(0, 0, width, height))
	for y := 0; y < height; y++ {
		srcY := srcBounds.Min.Y + y*srcHeight/height
		if srcY >= srcBounds.Max.Y {
			srcY = srcBounds.Max.Y - 1
		}
		for x := 0; x < width; x++ {
			srcX := srcBounds.Min.X + x*srcWidth/width
			if srcX >= srcBounds.Max.X {
				srcX = srcBounds.Max.X - 1
			}
			dst.Set(x, y, src.At(srcX, srcY))
		}
	}
	return dst
}

func validateWeWorkUploadMedia(media bus.Media, data []byte) error {
	if len(data) < weworkUploadMinBytes {
		return fmt.Errorf("wework media must be at least %d bytes", weworkUploadMinBytes)
	}

	switch NormalizeMediaType(media.Type) {
	case UnifiedMediaImage:
		detectedType := strings.ToLower(strings.TrimSpace(http.DetectContentType(data)))
		if detectedType == "image/jpeg" || detectedType == "image/png" {
			return nil
		}

		ext := strings.ToLower(filepath.Ext(InferMediaFileName(media, "")))
		if detectedType == "application/octet-stream" {
			if ext == ".jpg" || ext == ".jpeg" || ext == ".png" {
				return nil
			}
		}

		return fmt.Errorf("wework image upload only supports JPG/JPEG or PNG, got content-type %q", detectedType)
	case UnifiedMediaFile:
		return nil
	default:
		return fmt.Errorf("unsupported wework websocket media type: %s", media.Type)
	}
}

func splitWeWorkMediaChunks(data []byte, chunkSize int) [][]byte {
	if len(data) == 0 || chunkSize <= 0 {
		return nil
	}

	chunks := make([][]byte, 0, (len(data)+chunkSize-1)/chunkSize)
	for start := 0; start < len(data); start += chunkSize {
		end := start + chunkSize
		if end > len(data) {
			end = len(data)
		}
		chunks = append(chunks, data[start:end])
	}

	return chunks
}
