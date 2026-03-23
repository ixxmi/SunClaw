package wework

import (
	"bytes"
	"context"
	"crypto/md5"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/smallnest/goclaw/internal/core/channels/shared"
	"image"
	_ "image/gif"
	"image/jpeg"
	"image/png"
	"io"
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
	weworkLongConnReplyKindMessage   = "message"
	weworkLongConnReplyKindWelcome   = "welcome"
	weworkLongConnAckTimeout         = 10 * time.Second
	weworkLongConnStreamSendInterval = 300 * time.Millisecond
	weworkUploadChunkSize            = 512 << 10
	weworkUploadMaxChunks            = 100
	weworkUploadMinBytes             = 5
	weworkUploadImageSourceMaxBytes  = 20 << 20
	weworkUploadImageMaxBytes        = 10 << 20
	weworkUploadVideoMaxBytes        = 10 << 20
	weworkUploadFileMaxBytes         = 20 << 20
	weworkUploadImageSafeMaxBytes    = 2 << 20
	weworkUploadImageRetryMaxBytes   = 1 << 20
	weworkMediaDecryptPaddingBytes   = 32
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

func (s *weworkLongConnState) pendingAckCount() int {
	s.ackMu.Lock()
	defer s.ackMu.Unlock()
	return len(s.pending)
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
			if ctx.Err() != nil {
				return nil
			}
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
				resolved := c.longConn.resolveAck(frame.Headers.ReqID, weworkLongConnResponse{
					ErrCode: frame.ErrCode,
					ErrMsg:  frame.ErrMsg,
					Body:    frame.Body,
				})
				logger.Debug("Received WeWork websocket ack",
					zap.String("req_id", frame.Headers.ReqID),
					zap.Int("errcode", frame.ErrCode),
					zap.String("errmsg", truncateWeWorkLog(frame.ErrMsg, 200)),
					zap.Bool("resolved", resolved),
					zap.Int("pending_acks", c.longConn.pendingAckCount()))
			}
			continue
		}

		switch frame.Cmd {
		case "aibot_msg_callback":
			msg, chatID, ok := c.prepareLongConnMessageCallback(frame.Headers.ReqID, frame.Body)
			if !ok {
				continue
			}
			go func(reqID, chatID string, msg *weworkAiBotMessage) {
				defer func() {
					if r := recover(); r != nil {
						logger.Error("Panic while handling WeWork websocket message callback",
							zap.Any("panic", r),
							zap.String("req_id", reqID),
							zap.String("chat_id", chatID),
							zap.String("msgid", msg.MsgID),
							zap.Stack("stack"))
					}
				}()
				c.handlePreparedLongConnMessageCallback(reqID, chatID, msg)
			}(frame.Headers.ReqID, chatID, msg)
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
	_ = conn.SetWriteDeadline(time.Now().Add(weworkLongConnAckTimeout))
	err := conn.WriteJSON(payload)
	c.longConn.writeMu.Unlock()
	if err != nil {
		if waitAck {
			c.longConn.unregisterAck(reqID)
		}
		return weworkLongConnResponse{}, fmt.Errorf("write wework websocket command failed: %w", err)
	}

	logger.Debug("Sent WeWork websocket command",
		zap.String("cmd", cmd),
		zap.String("req_id", reqID),
		zap.Bool("wait_ack", waitAck),
		zap.Int("pending_acks", c.longConn.pendingAckCount()))

	if !waitAck {
		return weworkLongConnResponse{}, nil
	}

	timer := time.NewTimer(weworkLongConnAckTimeout)
	defer timer.Stop()

	select {
	case ack, ok := <-ackCh:
		if !ok {
			return weworkLongConnResponse{}, fmt.Errorf("wework websocket ack channel closed for req_id=%s", reqID)
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
		pendingAcks := c.longConn.pendingAckCount()
		logger.Warn("WeWork websocket command timed out waiting for ack",
			zap.String("cmd", cmd),
			zap.String("req_id", reqID),
			zap.Duration("waited", weworkLongConnAckTimeout),
			zap.Int("pending_acks", pendingAcks))
		return weworkLongConnResponse{}, fmt.Errorf("wework websocket command %s timed out (req_id=%s, waited=%s, pending_acks=%d)", cmd, reqID, weworkLongConnAckTimeout, pendingAcks)
	}
}

func (c *WeWorkChannel) handleLongConnMessageCallback(reqID string, raw json.RawMessage) {
	msg, chatID, ok := c.prepareLongConnMessageCallback(reqID, raw)
	if !ok {
		return
	}
	c.handlePreparedLongConnMessageCallback(reqID, chatID, msg)
}

func (c *WeWorkChannel) prepareLongConnMessageCallback(reqID string, raw json.RawMessage) (*weworkAiBotMessage, string, bool) {
	var msg weworkAiBotMessage
	if err := json.Unmarshal(raw, &msg); err != nil {
		logger.Error("Failed to decode WeWork websocket message callback",
			zap.Error(err),
			zap.String("payload", truncateWeWorkLog(string(raw), 300)))
		return nil, "", false
	}

	if msg.MsgType == "stream" {
		return nil, "", false
	}

	chatID := resolveWeWorkChatID(&msg)
	if chatID == "" {
		return nil, "", false
	}

	if msg.From.UserID != "" && !c.IsAllowed(msg.From.UserID) {
		return nil, "", false
	}

	c.longConn.storeReplyContext(&weworkReplyContext{
		MessageID: msg.MsgID,
		ReqID:     reqID,
		Kind:      weworkLongConnReplyKindMessage,
		ChatID:    chatID,
		ChatType:  msg.ChatType,
		CreatedAt: time.Now(),
	})

	return &msg, chatID, true
}

func (c *WeWorkChannel) handlePreparedLongConnMessageCallback(reqID, chatID string, msg *weworkAiBotMessage) {
	content, media := c.extractLongConnMessageContentAndMedia(msg)
	if content == "" && len(media) == 0 {
		return
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
			"aibot_id":  msg.AiBotID,
			"chat_type": msg.ChatType,
			"msg_type":  msg.MsgType,
			"req_id":    reqID,
		},
		Media: media,
	}

	if err := c.PublishInbound(context.Background(), inMsg); err != nil {
		logger.Error("Failed to publish WeWork websocket inbound message",
			zap.Error(err),
			zap.String("msgid", msg.MsgID))
	}
}

func warnWeWorkReplyContextAging(cmd string, replyCtx *weworkReplyContext) {
	if replyCtx == nil || replyCtx.ReqID == "" || replyCtx.CreatedAt.IsZero() {
		return
	}

	age := time.Since(replyCtx.CreatedAt)
	if age < 8*time.Second {
		return
	}

	logger.Warn("WeWork reply context is aging and may expire",
		zap.String("cmd", cmd),
		zap.String("req_id", replyCtx.ReqID),
		zap.String("chat_id", replyCtx.ChatID),
		zap.String("message_id", replyCtx.MessageID),
		zap.String("kind", replyCtx.Kind),
		zap.Duration("age", age))
}

func (c *WeWorkChannel) extractLongConnMessageContentAndMedia(msg *weworkAiBotMessage) (string, []bus.Media) {
	if msg == nil {
		return "", nil
	}

	var parts []string
	var media []bus.Media

	appendPart := func(s string) {
		s = strings.TrimSpace(s)
		if s != "" {
			parts = append(parts, s)
		}
	}

	addEncryptedMedia := func(kind, label, mediaURL, aesKey string, maxBytes int64) {
		appendPart(label)

		item := bus.Media{Type: kind}
		if strings.TrimSpace(mediaURL) == "" {
			return
		}
		if strings.TrimSpace(aesKey) == "" {
			item.URL = mediaURL
			media = append(media, item)
			return
		}

		data, mimeType, err := c.downloadAndDecryptLongConnMedia(mediaURL, aesKey, maxBytes)
		if err != nil {
			logger.Warn("Failed to download/decrypt WeWork websocket media",
				zap.Error(err),
				zap.String("msgid", msg.MsgID),
				zap.String("msg_type", msg.MsgType),
				zap.String("media_type", kind))
			return
		}

		item.Base64 = base64.StdEncoding.EncodeToString(data)
		item.MimeType = mimeType
		item.Name = inferWeWorkInboundMediaName(kind, mediaURL, mimeType)
		media = append(media, item)
	}

	switch msg.MsgType {
	case "text":
		if msg.Text != nil {
			appendPart(msg.Text.Content)
		}
	case "voice":
		if msg.Voice != nil {
			appendPart(msg.Voice.Content)
		}
	case "image":
		if msg.Image != nil {
			addEncryptedMedia(shared.UnifiedMediaImage, "[图片]", msg.Image.URL, msg.Image.AESKey, weworkUploadImageMaxBytes)
		}
	case "file":
		if msg.File != nil {
			addEncryptedMedia(shared.UnifiedMediaFile, "[文件]", msg.File.URL, msg.File.AESKey, weworkUploadFileMaxBytes)
		}
	case "video":
		if msg.Video != nil {
			addEncryptedMedia(shared.UnifiedMediaVideo, "[视频]", msg.Video.URL, msg.Video.AESKey, weworkUploadVideoMaxBytes)
		}
	case "mixed":
		if msg.Mixed != nil {
			for _, item := range msg.Mixed.MsgItems {
				switch item.MsgType {
				case "text":
					if item.Text != nil {
						appendPart(item.Text.Content)
					}
				case "image":
					if item.Image != nil {
						addEncryptedMedia(shared.UnifiedMediaImage, "[图片]", item.Image.URL, item.Image.AESKey, weworkUploadImageMaxBytes)
					}
				}
			}
		}
	default:
		appendPart(extractAiBotTextContent(msg))
	}

	return strings.Join(parts, "\n"), media
}

func (c *WeWorkChannel) downloadAndDecryptLongConnMedia(mediaURL, aesKey string, maxBytes int64) ([]byte, string, error) {
	req, err := http.NewRequestWithContext(context.Background(), http.MethodGet, mediaURL, nil)
	if err != nil {
		return nil, "", fmt.Errorf("build media request failed: %w", err)
	}

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, "", fmt.Errorf("download media failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, "", fmt.Errorf("download media failed with status %d", resp.StatusCode)
	}

	encryptedLimit := maxBytes
	if encryptedLimit > 0 {
		encryptedLimit += weworkMediaDecryptPaddingBytes
	}

	reader := io.Reader(resp.Body)
	if encryptedLimit > 0 {
		reader = io.LimitReader(resp.Body, encryptedLimit+1)
	}

	ciphertext, err := io.ReadAll(reader)
	if err != nil {
		return nil, "", fmt.Errorf("read media body failed: %w", err)
	}
	if encryptedLimit > 0 && int64(len(ciphertext)) > encryptedLimit {
		return nil, "", fmt.Errorf("encrypted media exceeds size limit: %d > %d", len(ciphertext), encryptedLimit)
	}

	plaintext, err := decryptWeWorkCBCPayload(ciphertext, aesKey)
	if err != nil {
		return nil, "", fmt.Errorf("decrypt media failed: %w", err)
	}
	if maxBytes > 0 && int64(len(plaintext)) > maxBytes {
		return nil, "", fmt.Errorf("decrypted media exceeds size limit: %d > %d", len(plaintext), maxBytes)
	}

	return plaintext, detectWeWorkMediaContentType(plaintext), nil
}

func inferWeWorkInboundMediaName(kind, mediaURL, mimeType string) string {
	fallback := "attachment"
	switch kind {
	case shared.UnifiedMediaImage:
		fallback = "image"
	case shared.UnifiedMediaFile:
		fallback = "file"
	case shared.UnifiedMediaVideo:
		fallback = "video"
	}

	name := shared.InferMediaFileName(bus.Media{Type: kind, URL: mediaURL}, fallback)
	if filepath.Ext(name) != "" {
		return name
	}

	switch mimeType {
	case "image/png":
		return name + ".png"
	case "image/jpeg":
		return name + ".jpg"
	case "image/gif":
		return name + ".gif"
	case "video/mp4":
		return name + ".mp4"
	default:
		return name
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
			warnWeWorkReplyContextAging(customCmd, replyCtx)
		}
		return c.sendLongConnCommand(context.Background(), customCmd, reqID, customBody, true)
	}

	var nativeMedia []bus.Media
	var fallbackMedia []bus.Media
	for _, media := range msg.Media {
		media.Type = shared.NormalizeMediaType(media.Type)
		switch media.Type {
		case shared.UnifiedMediaImage, shared.UnifiedMediaFile:
			nativeMedia = append(nativeMedia, media)
		default:
			fallbackMedia = append(fallbackMedia, media)
		}
	}

	content := shared.AppendMediaURLsToContent(msg.Content, fallbackMedia, map[string]bool{
		shared.UnifiedMediaImage: true,
		shared.UnifiedMediaFile:  true,
		shared.UnifiedMediaVideo: true,
		shared.UnifiedMediaAudio: true,
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
	lastSentAt := time.Time{}
	lastSentLen := 0
	sentAny := false

	sendStreamChunk := func(finish bool) error {
		warnWeWorkReplyContextAging("aibot_respond_msg", replyCtx)
		if err := c.sendLongConnCommand(context.Background(), "aibot_respond_msg", replyCtx.ReqID, map[string]interface{}{
			"msgtype": "stream",
			"stream": map[string]interface{}{
				"id":      streamID,
				"finish":  finish,
				"content": content.String(),
			},
		}, true); err != nil {
			return err
		}

		sentAny = true
		lastSentLen = content.Len()
		lastSentAt = time.Now()
		return nil
	}

	for msg := range stream {
		if msg.Error != "" {
			return fmt.Errorf("stream error: %s", msg.Error)
		}
		if !msg.IsThinking && !msg.IsFinal {
			content.WriteString(msg.Content)
		}

		shouldSend := false
		switch {
		case msg.IsComplete:
			shouldSend = true
		case !sentAny && content.Len() > 0:
			shouldSend = true
		case content.Len() > lastSentLen && !lastSentAt.IsZero() && time.Since(lastSentAt) >= weworkLongConnStreamSendInterval:
			shouldSend = true
		}

		if shouldSend {
			if err := sendStreamChunk(msg.IsComplete); err != nil {
				return err
			}
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
			warnWeWorkReplyContextAging("aibot_respond_welcome_msg", replyCtx)
			return c.sendLongConnCommand(ctx, "aibot_respond_welcome_msg", replyCtx.ReqID, map[string]interface{}{
				"msgtype": "text",
				"text": map[string]string{
					"content": content,
				},
			}, true)
		default:
			warnWeWorkReplyContextAging("aibot_respond_msg", replyCtx)
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
	media.Type = shared.NormalizeMediaType(media.Type)
	if media.Type != shared.UnifiedMediaImage && media.Type != shared.UnifiedMediaFile {
		return fmt.Errorf("unsupported wework websocket media type: %s", media.Type)
	}

	effectiveMedia, mediaID, err := c.uploadLongConnMedia(ctx, media)
	if err != nil {
		return err
	}

	body := map[string]interface{}{
		"msgtype": effectiveMedia.Type,
		effectiveMedia.Type: map[string]string{
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
	warnWeWorkReplyContextAging(cmd, replyCtx)
	return c.sendLongConnCommand(ctx, cmd, replyCtx.ReqID, body, true)
}

func (c *WeWorkChannel) uploadLongConnMedia(ctx context.Context, media bus.Media) (bus.Media, string, error) {
	media.Type = shared.NormalizeMediaType(media.Type)

	maxBytes := int64(weworkUploadFileMaxBytes)
	fallbackName := "attachment"
	switch media.Type {
	case shared.UnifiedMediaImage:
		maxBytes = weworkUploadImageSourceMaxBytes
		fallbackName = "image.jpg"
	case shared.UnifiedMediaFile:
		maxBytes = weworkUploadFileMaxBytes
	default:
		return bus.Media{}, "", fmt.Errorf("unsupported wework websocket media type: %s", media.Type)
	}

	originalName := shared.InferMediaFileName(media, fallbackName)
	data, err := shared.MaterializeMediaData(c.httpClient, media, maxBytes)
	if err != nil {
		return bus.Media{}, "", fmt.Errorf("materialize wework media failed: %w", err)
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

	if downgradedMedia, downgraded := downgradeUnsupportedWeWorkImage(media, data); downgraded {
		logger.Warn("WeWork does not support SVG image upload, falling back to file message",
			zap.String("original_filename", originalName),
			zap.String("fallback_filename", shared.InferMediaFileName(downgradedMedia, "attachment.svg")),
			zap.String("detected_content_type", originalDetectedType),
			zap.Int("size_bytes", originalSize))
		media = downgradedMedia
		fallbackName = "attachment.svg"
	}

	if media.Type == shared.UnifiedMediaImage {
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
					zap.String("normalized_filename", shared.InferMediaFileName(preparedMedia, fallbackName)),
					zap.String("original_content_type", originalDetectedType),
					zap.String("normalized_content_type", detectWeWorkMediaContentType(preparedData)),
					zap.Int("original_size_bytes", originalSize),
					zap.Int("normalized_size_bytes", len(preparedData)),
					zap.Int64("target_max_bytes", targetCap))
			}

			mediaID, uploadErr := c.uploadPreparedLongConnMedia(ctx, preparedMedia, preparedData, fallbackName)
			if uploadErr == nil {
				return preparedMedia, mediaID, nil
			}

			lastErr = uploadErr
			var cmdErr *weworkLongConnCommandError
			if errors.As(uploadErr, &cmdErr) && cmdErr.ErrCode == 40009 && strings.EqualFold(strings.TrimSpace(cmdErr.Cmd), "aibot_upload_media_init") {
				logger.Warn("WeWork rejected image size during init, retrying with stricter target",
					zap.String("filename", shared.InferMediaFileName(preparedMedia, fallbackName)),
					zap.Int("prepared_size_bytes", len(preparedData)),
					zap.Int64("target_max_bytes", targetCap),
					zap.Int("errcode", cmdErr.ErrCode),
					zap.String("errmsg", truncateWeWorkLog(cmdErr.ErrMsg, 200)))
				continue
			}

			return bus.Media{}, "", uploadErr
		}

		if lastErr != nil {
			return bus.Media{}, "", lastErr
		}
		return bus.Media{}, "", fmt.Errorf("wework image upload failed without a concrete error")
	}

	mediaID, err := c.uploadPreparedLongConnMedia(ctx, media, data, fallbackName)
	if err != nil {
		return bus.Media{}, "", err
	}
	return media, mediaID, nil
}

func downgradeUnsupportedWeWorkImage(media bus.Media, data []byte) (bus.Media, bool) {
	media.Type = shared.NormalizeMediaType(media.Type)
	if media.Type != shared.UnifiedMediaImage || !isWeWorkSVGImage(data, media) {
		return media, false
	}

	downgraded := media
	downgraded.Type = shared.UnifiedMediaFile
	downgraded.MimeType = "image/svg+xml"

	name := shared.InferMediaFileName(media, "image")
	base := strings.TrimSuffix(name, filepath.Ext(name))
	if base == "" {
		base = "image"
	}
	downgraded.Name = base + ".svg"
	return downgraded, true
}

func isWeWorkSVGImage(data []byte, media bus.Media) bool {
	if strings.EqualFold(strings.TrimSpace(media.MimeType), "image/svg+xml") {
		return true
	}
	if strings.EqualFold(strings.ToLower(filepath.Ext(shared.InferMediaFileName(media, ""))), ".svg") {
		return true
	}

	snippet := bytes.TrimSpace(data)
	if len(snippet) > 512 {
		snippet = snippet[:512]
	}
	lower := strings.ToLower(string(snippet))
	if strings.HasPrefix(lower, "<svg") {
		return true
	}
	if strings.HasPrefix(lower, "<?xml") && strings.Contains(lower, "<svg") {
		return true
	}
	return false
}

func (c *WeWorkChannel) uploadPreparedLongConnMedia(ctx context.Context, media bus.Media, data []byte, fallbackName string) (string, error) {
	if err := validateWeWorkUploadMedia(media, data); err != nil {
		logger.Warn("WeWork media validation failed",
			zap.String("media_type", media.Type),
			zap.String("filename", shared.InferMediaFileName(media, fallbackName)),
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
		zap.String("filename", shared.InferMediaFileName(media, fallbackName)),
		zap.String("detected_content_type", detectWeWorkMediaContentType(data)),
		zap.Int("size_bytes", len(data)),
		zap.Int("chunks", len(chunks)),
		zap.Int("chunk_size_bytes", weworkUploadChunkSize))

	checksum := md5.Sum(data)
	initResp, err := c.sendLongConnCommandWithResponse(ctx, "aibot_upload_media_init", "", map[string]interface{}{
		"type":         media.Type,
		"filename":     shared.InferMediaFileName(media, fallbackName),
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
		zap.String("filename", shared.InferMediaFileName(media, fallbackName)),
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
		zap.String("filename", shared.InferMediaFileName(media, fallbackName)),
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
	name := shared.InferMediaFileName(media, "image")
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

	switch shared.NormalizeMediaType(media.Type) {
	case shared.UnifiedMediaImage:
		detectedType := strings.ToLower(strings.TrimSpace(http.DetectContentType(data)))
		if detectedType == "image/jpeg" || detectedType == "image/png" {
			return nil
		}

		ext := strings.ToLower(filepath.Ext(shared.InferMediaFileName(media, "")))
		if detectedType == "application/octet-stream" {
			if ext == ".jpg" || ext == ".jpeg" || ext == ".png" {
				return nil
			}
		}

		return fmt.Errorf("wework image upload only supports JPG/JPEG or PNG, got content-type %q", detectedType)
	case shared.UnifiedMediaFile:
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
