package imessage

import (
	"context"
	"encoding/json"
	"fmt"
	"github.com/smallnest/goclaw/internal/core/channels/shared"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/smallnest/goclaw/internal/core/bus"
	"github.com/smallnest/goclaw/internal/logger"
	"go.uber.org/zap"
)

// IMessageChannel iMessage 通道（通过 bridge 接入）
type IMessageChannel struct {
	*shared.BaseChannelImpl
	bridgeURL string
	client    *http.Client
}

// IMessageConfig iMessage 配置
type IMessageConfig struct {
	shared.BaseChannelConfig
	BridgeURL string `mapstructure:"bridge_url" json:"bridge_url"`
}

// IMessageBridgeMessage bridge 返回的 iMessage 消息结构
type IMessageBridgeMessage struct {
	ID        string `json:"id"`
	From      string `json:"from"`
	ChatID    string `json:"chat_id"`
	Text      string `json:"text"`
	Type      string `json:"type"`
	Timestamp int64  `json:"timestamp"`
}

// NewIMessageChannel 创建 iMessage 通道
func NewIMessageChannel(accountID string, cfg IMessageConfig, bus *bus.MessageBus) (*IMessageChannel, error) {
	return &IMessageChannel{
		BaseChannelImpl: shared.NewBaseChannelImpl("imessage", accountID, cfg.BaseChannelConfig, bus),
		bridgeURL:       strings.TrimRight(cfg.BridgeURL, "/"),
		client: &http.Client{
			Timeout: 30 * time.Second,
		},
	}, nil
}

// Start 启动 iMessage 通道
func (c *IMessageChannel) Start(ctx context.Context) error {
	if err := c.BaseChannelImpl.Start(ctx); err != nil {
		return err
	}

	if c.bridgeURL == "" {
		logger.Info("iMessage bridge URL not configured, channel disabled",
			zap.String("account_id", c.AccountID()),
		)
		return nil
	}

	logger.Info("Starting iMessage channel",
		zap.String("account_id", c.AccountID()),
		zap.String("bridge_url", c.bridgeURL),
	)

	go c.pollMessages(ctx)
	return nil
}

// pollMessages 轮询消息
func (c *IMessageChannel) pollMessages(ctx context.Context) {
	ticker := time.NewTicker(2 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			logger.Info("iMessage channel stopped by context",
				zap.String("account_id", c.AccountID()),
			)
			return
		case <-c.WaitForStop():
			logger.Info("iMessage channel stopped",
				zap.String("account_id", c.AccountID()),
			)
			return
		case <-ticker.C:
			if err := c.fetchMessages(ctx); err != nil {
				logger.Error("Failed to fetch iMessage messages",
					zap.String("account_id", c.AccountID()),
					zap.Error(err),
				)
			}
		}
	}
}

// fetchMessages 获取消息
func (c *IMessageChannel) fetchMessages(ctx context.Context) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.bridgeURL+"/messages", nil)
	if err != nil {
		return err
	}

	resp, err := c.client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("unexpected status code: %d", resp.StatusCode)
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return err
	}

	var messages []IMessageBridgeMessage
	if err := json.Unmarshal(body, &messages); err != nil {
		return err
	}

	for _, msg := range messages {
		if err := c.handleMessage(ctx, &msg); err != nil {
			logger.Error("Failed to handle iMessage message",
				zap.String("account_id", c.AccountID()),
				zap.String("message_id", msg.ID),
				zap.Error(err),
			)
		}
	}

	return nil
}

// handleMessage 处理消息
func (c *IMessageChannel) handleMessage(ctx context.Context, msg *IMessageBridgeMessage) error {
	if !c.IsAllowed(msg.From) {
		return nil
	}

	inboundMsg := &bus.InboundMessage{
		ID:        msg.ID,
		Channel:   c.Name(),
		AccountID: c.AccountID(),
		SenderID:  msg.From,
		ChatID:    msg.ChatID,
		Content:   msg.Text,
		Metadata: map[string]interface{}{
			"message_type": msg.Type,
			"timestamp":    msg.Timestamp,
		},
		Timestamp: time.Now(),
	}

	return c.PublishInbound(ctx, inboundMsg)
}

// Send 发送消息
func (c *IMessageChannel) Send(msg *bus.OutboundMessage) error {
	if !c.IsRunning() {
		return fmt.Errorf("imessage channel is not running")
	}

	content := shared.AppendMediaURLsToContent(msg.Content, msg.Media, map[string]bool{
		shared.UnifiedMediaImage: true,
		shared.UnifiedMediaFile:  true,
		shared.UnifiedMediaVideo: true,
		shared.UnifiedMediaAudio: true,
	})

	data := map[string]interface{}{
		"chat_id": msg.ChatID,
		"text":    content,
	}

	jsonData, err := json.Marshal(data)
	if err != nil {
		return err
	}

	req, err := http.NewRequest(http.MethodPost, c.bridgeURL+"/send", strings.NewReader(string(jsonData)))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := c.client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("unexpected status code: %d", resp.StatusCode)
	}

	logger.Info("iMessage message sent",
		zap.String("account_id", c.AccountID()),
		zap.String("chat_id", msg.ChatID),
		zap.Int("content_length", len(content)),
	)

	return nil
}
