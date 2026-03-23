package weixin

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"github.com/smallnest/goclaw/internal/core/channels/shared"
	"io"
	"net/http"
	neturl "net/url"
	"strings"
	"sync"
	"time"

	"github.com/smallnest/goclaw/internal/core/bus"
	"github.com/smallnest/goclaw/internal/logger"
	"go.uber.org/zap"
)

const weixinSessionBootstrapCooldown = 30 * time.Second

// WeixinChannel 微信通道（通过 bridge 接入）
type WeixinChannel struct {
	*shared.BaseChannelImpl
	mode       string
	token      string
	baseURL    string
	cdnBaseURL string
	bridgeURL  string
	client     *http.Client
	directAPI  *weixinDirectAPIClient

	bootstrapMu     sync.Mutex
	lastBootstrapAt time.Time

	directCancel   context.CancelFunc
	contextTokens  sync.Map
	pauseMu        sync.Mutex
	pauseUntil     time.Time
	syncCursorPath string
}

// WeixinConfig 微信配置
type WeixinConfig struct {
	shared.BaseChannelConfig
	Mode       string `mapstructure:"mode" json:"mode"`
	Token      string `mapstructure:"token" json:"token"`
	BaseURL    string `mapstructure:"base_url" json:"base_url"`
	CDNBaseURL string `mapstructure:"cdn_base_url" json:"cdn_base_url"`
	Proxy      string `mapstructure:"proxy" json:"proxy"`
	BridgeURL  string `mapstructure:"bridge_url" json:"bridge_url"`
}

// WeixinBridgeMessage bridge 返回的微信消息结构
type WeixinBridgeMessage struct {
	ID             string                 `json:"id"`
	From           string                 `json:"from"`
	SenderID       string                 `json:"sender_id"`
	ChatID         string                 `json:"chat_id"`
	ConversationID string                 `json:"conversation_id"`
	Text           string                 `json:"text"`
	Content        string                 `json:"content"`
	Type           string                 `json:"type"`
	Timestamp      int64                  `json:"timestamp"`
	Media          []bus.Media            `json:"media"`
	Metadata       map[string]interface{} `json:"metadata"`
}

type weixinMessageEnvelope struct {
	Messages []WeixinBridgeMessage `json:"messages"`
	Data     []WeixinBridgeMessage `json:"data"`
}

type weixinSessionStatus struct {
	Authenticated bool   `json:"authenticated"`
	NeedsScan     bool   `json:"needs_scan"`
	SessionID     string `json:"session_id"`
	QRCodeURL     string `json:"qr_code_url"`
	QRCodeBase64  string `json:"qr_code_base64"`
	ExpiresAt     int64  `json:"expires_at"`
	Message       string `json:"message"`
}

type weixinSendRequest struct {
	ChatID  string      `json:"chat_id"`
	Text    string      `json:"text,omitempty"`
	Content string      `json:"content,omitempty"`
	ReplyTo string      `json:"reply_to,omitempty"`
	Media   []bus.Media `json:"media,omitempty"`
}

// NewWeixinChannel 创建微信通道
func NewWeixinChannel(accountID string, cfg WeixinConfig, bus *bus.MessageBus) (*WeixinChannel, error) {
	mode := resolveWeixinRuntimeMode(cfg)
	client, err := newWeixinHTTPClient(cfg.Proxy)
	if err != nil {
		return nil, err
	}

	channel := &WeixinChannel{
		BaseChannelImpl: shared.NewBaseChannelImpl("weixin", accountID, cfg.BaseChannelConfig, bus),
		mode:            mode,
		token:           strings.TrimSpace(cfg.Token),
		baseURL:         strings.TrimSpace(cfg.BaseURL),
		cdnBaseURL:      strings.TrimSpace(cfg.CDNBaseURL),
		bridgeURL:       strings.TrimRight(cfg.BridgeURL, "/"),
		client:          client,
	}

	if mode == weixinModeDirect {
		if channel.token == "" {
			return nil, fmt.Errorf("weixin direct mode requires token")
		}
		api, err := newWeixinDirectAPIClient(channel.baseURL, channel.token, client)
		if err != nil {
			return nil, err
		}
		channel.directAPI = api
		channel.syncCursorPath = buildWeixinDirectSyncCursorPath(channel.baseURL, channel.token)
	}

	return channel, nil
}

// Start 启动微信通道
func (c *WeixinChannel) Start(ctx context.Context) error {
	if err := c.BaseChannelImpl.Start(ctx); err != nil {
		return err
	}

	if c.mode == weixinModeDirect {
		return c.startDirect(ctx)
	}

	if c.bridgeURL == "" {
		logger.Info("Weixin bridge URL not configured, channel disabled",
			zap.String("account_id", c.AccountID()),
		)
		return nil
	}

	logger.Info("Starting Weixin channel",
		zap.String("account_id", c.AccountID()),
		zap.String("bridge_url", c.bridgeURL),
	)

	if err := c.ensureSession(ctx, true); err != nil {
		logger.Warn("Weixin bridge session bootstrap failed",
			zap.String("account_id", c.AccountID()),
			zap.Error(err),
		)
	}

	go c.pollMessages(ctx)
	return nil
}

// Stop 停止微信通道
func (c *WeixinChannel) Stop() error {
	if c.directCancel != nil {
		c.directCancel()
	}
	return c.BaseChannelImpl.Stop()
}

func (c *WeixinChannel) pollMessages(ctx context.Context) {
	ticker := time.NewTicker(2 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			logger.Info("Weixin channel stopped by context",
				zap.String("account_id", c.AccountID()),
			)
			return
		case <-c.WaitForStop():
			logger.Info("Weixin channel stopped",
				zap.String("account_id", c.AccountID()),
			)
			return
		case <-ticker.C:
			if err := c.fetchMessages(ctx); err != nil {
				logger.Error("Failed to fetch Weixin messages",
					zap.String("account_id", c.AccountID()),
					zap.Error(err),
				)
			}
		}
	}
}

func (c *WeixinChannel) fetchMessages(ctx context.Context) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.bridgeURL+"/messages", nil)
	if err != nil {
		return err
	}

	resp, err := c.client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden {
		_ = c.ensureSession(ctx, false)
		return nil
	}
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("unexpected status code: %d", resp.StatusCode)
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return err
	}

	messages, err := decodeWeixinMessages(body)
	if err != nil {
		return err
	}

	for _, msg := range messages {
		if err := c.handleMessage(ctx, &msg); err != nil {
			logger.Error("Failed to handle Weixin message",
				zap.String("account_id", c.AccountID()),
				zap.String("message_id", msg.ID),
				zap.Error(err),
			)
		}
	}

	return nil
}

func decodeWeixinMessages(body []byte) ([]WeixinBridgeMessage, error) {
	trimmed := bytes.TrimSpace(body)
	if len(trimmed) == 0 {
		return nil, nil
	}

	if trimmed[0] == '[' {
		var messages []WeixinBridgeMessage
		if err := json.Unmarshal(trimmed, &messages); err != nil {
			return nil, err
		}
		return messages, nil
	}

	var envelope weixinMessageEnvelope
	if err := json.Unmarshal(trimmed, &envelope); err != nil {
		return nil, err
	}
	if len(envelope.Messages) > 0 {
		return envelope.Messages, nil
	}
	return envelope.Data, nil
}

func (c *WeixinChannel) handleMessage(ctx context.Context, msg *WeixinBridgeMessage) error {
	senderID := strings.TrimSpace(msg.SenderID)
	if senderID == "" {
		senderID = strings.TrimSpace(msg.From)
	}
	chatID := strings.TrimSpace(msg.ChatID)
	if chatID == "" {
		chatID = strings.TrimSpace(msg.ConversationID)
	}
	if senderID == "" {
		senderID = chatID
	}
	if chatID == "" {
		chatID = senderID
	}

	if senderID != "" && !c.IsAllowed(senderID) {
		return nil
	}

	content := strings.TrimSpace(msg.Text)
	if content == "" {
		content = strings.TrimSpace(msg.Content)
	}

	inboundMsg := &bus.InboundMessage{
		ID:        msg.ID,
		Channel:   c.Name(),
		AccountID: c.AccountID(),
		SenderID:  senderID,
		ChatID:    chatID,
		Content:   content,
		Media:     msg.Media,
		Metadata: map[string]interface{}{
			"message_type": msg.Type,
			"timestamp":    msg.Timestamp,
			"metadata":     msg.Metadata,
		},
		Timestamp: time.Now(),
	}

	return c.PublishInbound(ctx, inboundMsg)
}

// Send 发送消息
func (c *WeixinChannel) Send(msg *bus.OutboundMessage) error {
	if c.mode == weixinModeDirect {
		return c.sendDirect(msg)
	}
	return c.sendViaBridge(msg)
}

func (c *WeixinChannel) sendViaBridge(msg *bus.OutboundMessage) error {
	if !c.IsRunning() {
		return fmt.Errorf("weixin channel is not running")
	}

	payload := weixinSendRequest{
		ChatID:  msg.ChatID,
		Text:    msg.Content,
		Content: msg.Content,
		ReplyTo: msg.ReplyTo,
		Media:   msg.Media,
	}

	jsonData, err := json.Marshal(payload)
	if err != nil {
		return err
	}

	req, err := http.NewRequest(http.MethodPost, c.bridgeURL+"/send", bytes.NewReader(jsonData))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := c.client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden {
		_ = c.ensureSession(context.Background(), false)
		return fmt.Errorf("weixin bridge requires login")
	}
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("unexpected status code: %d", resp.StatusCode)
	}

	logger.Info("Weixin message sent",
		zap.String("account_id", c.AccountID()),
		zap.String("chat_id", msg.ChatID),
		zap.Int("content_length", len(msg.Content)),
		zap.Int("media_count", len(msg.Media)),
	)

	return nil
}

func (c *WeixinChannel) ensureSession(ctx context.Context, force bool) error {
	if c.bridgeURL == "" {
		return nil
	}

	c.bootstrapMu.Lock()
	if !force && !c.lastBootstrapAt.IsZero() && time.Since(c.lastBootstrapAt) < weixinSessionBootstrapCooldown {
		c.bootstrapMu.Unlock()
		return nil
	}
	c.lastBootstrapAt = time.Now()
	c.bootstrapMu.Unlock()

	status, supported, err := c.fetchSessionStatus(ctx)
	if err != nil {
		return err
	}
	if supported {
		if status.Authenticated {
			logger.Info("Weixin bridge session is ready",
				zap.String("account_id", c.AccountID()),
				zap.String("session_id", status.SessionID),
			)
			return nil
		}
		if status.NeedsScan || status.QRCodeURL != "" || status.QRCodeBase64 != "" {
			c.logScanRequired(status)
			return nil
		}
	}

	status, supported, err = c.startSession(ctx)
	if err != nil {
		return err
	}
	if !supported {
		return nil
	}
	if status.Authenticated {
		logger.Info("Weixin bridge session authenticated",
			zap.String("account_id", c.AccountID()),
			zap.String("session_id", status.SessionID),
		)
		return nil
	}
	if status.NeedsScan || status.QRCodeURL != "" || status.QRCodeBase64 != "" {
		c.logScanRequired(status)
	}
	return nil
}

func (c *WeixinChannel) logScanRequired(status weixinSessionStatus) {
	logger.Warn("Weixin bridge requires QR scan",
		zap.String("account_id", c.AccountID()),
		zap.String("session_id", status.SessionID),
		zap.String("qr_code_url", status.QRCodeURL),
		zap.Bool("has_qr_code_base64", strings.TrimSpace(status.QRCodeBase64) != ""),
		zap.Int64("expires_at", status.ExpiresAt),
		zap.String("message", status.Message),
	)
}

func (c *WeixinChannel) fetchSessionStatus(ctx context.Context) (weixinSessionStatus, bool, error) {
	return c.fetchSessionPayload(ctx, http.MethodGet, "/session/status")
}

func (c *WeixinChannel) startSession(ctx context.Context) (weixinSessionStatus, bool, error) {
	return c.fetchSessionPayload(ctx, http.MethodPost, "/session/start")
}

func (c *WeixinChannel) fetchSessionPayload(ctx context.Context, method, path string) (weixinSessionStatus, bool, error) {
	req, err := http.NewRequestWithContext(ctx, method, c.bridgeURL+path, nil)
	if err != nil {
		return weixinSessionStatus{}, false, err
	}

	resp, err := c.client.Do(req)
	if err != nil {
		return weixinSessionStatus{}, false, err
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusNotFound || resp.StatusCode == http.StatusMethodNotAllowed {
		return weixinSessionStatus{}, false, nil
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return weixinSessionStatus{}, true, fmt.Errorf("unexpected status code from %s: %d", path, resp.StatusCode)
	}

	var status weixinSessionStatus
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return weixinSessionStatus{}, true, err
	}
	if len(bytes.TrimSpace(body)) == 0 {
		return weixinSessionStatus{}, true, nil
	}
	if err := json.Unmarshal(body, &status); err != nil {
		return weixinSessionStatus{}, true, err
	}
	return status, true, nil
}

const (
	ModeBridge       = "bridge"
	ModeDirect       = "direct"
	weixinModeBridge = ModeBridge
	weixinModeDirect = ModeDirect
)

func ResolveRuntimeMode(cfg WeixinConfig) string {
	mode := strings.ToLower(strings.TrimSpace(cfg.Mode))
	switch mode {
	case weixinModeDirect, "native", "ilink":
		return weixinModeDirect
	case weixinModeBridge:
		return weixinModeBridge
	}

	if strings.TrimSpace(cfg.BridgeURL) != "" {
		return weixinModeBridge
	}
	if strings.TrimSpace(cfg.Token) != "" || strings.TrimSpace(cfg.BaseURL) != "" || strings.TrimSpace(cfg.CDNBaseURL) != "" || strings.TrimSpace(cfg.Proxy) != "" {
		return weixinModeDirect
	}
	return weixinModeBridge
}

func resolveWeixinRuntimeMode(cfg WeixinConfig) string {
	return ResolveRuntimeMode(cfg)
}

func newWeixinHTTPClient(proxy string) (*http.Client, error) {
	client := &http.Client{Timeout: 30 * time.Second}
	if strings.TrimSpace(proxy) == "" {
		return client, nil
	}

	proxyURL, err := neturl.Parse(strings.TrimSpace(proxy))
	if err != nil {
		return nil, fmt.Errorf("invalid weixin proxy url: %w", err)
	}

	if defaultTransport, ok := http.DefaultTransport.(*http.Transport); ok {
		transport := defaultTransport.Clone()
		transport.Proxy = http.ProxyURL(proxyURL)
		client.Transport = transport
		return client, nil
	}

	client.Transport = &http.Transport{Proxy: http.ProxyURL(proxyURL)}
	return client, nil
}
