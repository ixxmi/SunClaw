package channels

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/smallnest/goclaw/internal/core/bus"
	"github.com/smallnest/goclaw/internal/core/config"
	"github.com/smallnest/goclaw/internal/logger"
	"go.uber.org/zap"
)

// AcpSessionRouter is an interface for routing messages to ACP sessions.
// This avoids circular dependency between channels and acp packages.
type AcpSessionRouter interface {
	RouteToAcpSession(channel, accountID, conversationID string) string
	IsACPThreadBinding(channel, accountID, conversationID string) bool
}

// Manager 通道管理器
type Manager struct {
	channels             map[string]BaseChannel
	bus                  *bus.MessageBus
	mu                   sync.RWMutex
	threadBindingService *ThreadBindingService
	acpRouter            AcpSessionRouter
}

// NewManager 创建通道管理器
func NewManager(bus *bus.MessageBus) *Manager {
	return &Manager{
		channels: make(map[string]BaseChannel),
		bus:      bus,
	}
}

// SetAcpRouter sets the ACP session router for thread-bound routing.
func (m *Manager) SetAcpRouter(router AcpSessionRouter) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.acpRouter = router
}

// SetThreadBindingService sets the thread binding service.
func (m *Manager) SetThreadBindingService(service *ThreadBindingService) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.threadBindingService = service
}

// RouteToAcpSession checks if there's a thread-bound ACP session for this conversation
// and returns the ACP session key if found.
func (m *Manager) RouteToAcpSession(channel, accountID, conversationID string) string {
	m.mu.RLock()
	router := m.acpRouter
	service := m.threadBindingService
	m.mu.RUnlock()

	if router != nil {
		if sessionKey := router.RouteToAcpSession(channel, accountID, conversationID); sessionKey != "" {
			return sessionKey
		}
	}

	if service == nil {
		return ""
	}

	binding := service.GetByConversation(channel, accountID, conversationID)
	if binding != nil && binding.TargetKind == "session" {
		return binding.TargetSessionKey
	}

	return ""
}

// IsACPThreadBinding checks if a conversation has an active ACP thread binding.
func (m *Manager) IsACPThreadBinding(channel, accountID, conversationID string) bool {
	m.mu.RLock()
	router := m.acpRouter
	service := m.threadBindingService
	m.mu.RUnlock()

	if router != nil && router.IsACPThreadBinding(channel, accountID, conversationID) {
		return true
	}

	if service == nil {
		return false
	}

	binding := service.GetByConversation(channel, accountID, conversationID)
	return binding != nil && binding.TargetKind == "session"
}

// Register 注册通道
func (m *Manager) Register(channel BaseChannel) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	name := channel.Name()
	if _, ok := m.channels[name]; ok {
		return fmt.Errorf("channel %s already registered", name)
	}

	m.channels[name] = channel
	logger.Info("Channel registered", zap.String("channel", name))
	return nil
}

// Start 启动所有通道
func (m *Manager) Start(ctx context.Context) error {
	m.mu.RLock()
	defer m.mu.RUnlock()

	for name, channel := range m.channels {
		logger.Info("Starting channel", zap.String("channel", name))
		if err := channel.Start(ctx); err != nil {
			logger.Error("Failed to start channel",
				zap.String("channel", name),
				zap.Error(err),
			)
			continue
		}
	}

	return nil
}

// Stop 停止所有通道
func (m *Manager) Stop() error {
	m.mu.RLock()
	defer m.mu.RUnlock()

	var errors []error
	for name, channel := range m.channels {
		if err := channel.Stop(); err != nil {
			logger.Error("Failed to stop channel",
				zap.String("channel", name),
				zap.Error(err),
			)
			errors = append(errors, err)
		}
	}

	if len(errors) > 0 {
		return fmt.Errorf("failed to stop some channels: %d errors", len(errors))
	}

	return nil
}

// ReloadFromConfig rebuilds channel registrations from config and starts them on the current runtime context.
func (m *Manager) ReloadFromConfig(ctx context.Context, cfg *config.Config) error {
	if err := m.Stop(); err != nil {
		logger.Warn("Failed to stop channels during reload", zap.Error(err))
	}

	m.mu.Lock()
	m.channels = make(map[string]BaseChannel)
	m.mu.Unlock()

	if err := m.SetupFromConfig(cfg); err != nil {
		return err
	}

	return m.Start(ctx)
}

// Get 获取通道
func (m *Manager) Get(name string) (BaseChannel, bool) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	channel, ok := m.channels[name]
	return channel, ok
}

// List 列出所有通道名称
func (m *Manager) List() []string {
	m.mu.RLock()
	defer m.mu.RUnlock()

	names := make([]string, 0, len(m.channels))
	for name := range m.channels {
		names = append(names, name)
	}
	return names
}

// Status 获取通道状态
func (m *Manager) Status(name string) (map[string]interface{}, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	channel, ok := m.channels[name]
	if !ok {
		return nil, fmt.Errorf("channel not found: %s", name)
	}

	// 简化的状态信息
	return map[string]interface{}{
		"name":    channel.Name(),
		"enabled": true,
	}, nil
}

// DispatchOutbound 分发出站消息
func (m *Manager) DispatchOutbound(ctx context.Context) error {
	logger.Debug(">>> Starting outbound message dispatcher <<<")
	defer logger.Debug(">>> Outbound dispatcher exited <<<")

	// 订阅出站消息
	subscription := m.bus.SubscribeOutbound()
	defer subscription.Unsubscribe()

	logger.Debug("Subscribed to outbound messages",
		zap.String("subscription_id", subscription.ID))

	busChan := subscription.Channel

	// 定期心跳日志
	heartbeat := time.NewTicker(30 * time.Second)
	defer heartbeat.Stop()

	for {
		select {
		case <-ctx.Done():
			logger.Debug("Outbound dispatcher stopped by context")
			return ctx.Err()
		case <-heartbeat.C:
			logger.Debug("Outbound dispatcher heartbeat - waiting for messages...",
				zap.Int("outbound_queue_size", m.bus.OutboundCount()))
		case msg, ok := <-busChan:
			logger.Debug("Outbound dispatcher: got message from channel",
				zap.Bool("ok", ok),
				zap.Bool("msg_nil", msg == nil))
			if !ok {
				logger.Warn("Outbound channel closed, exiting dispatcher")
				return nil
			}
			if msg == nil {
				logger.Warn("Received nil message, continuing")
				continue
			}

			logger.Debug("Outbound message received",
				zap.String("channel", msg.Channel),
				zap.String("account_id", msg.AccountID),
				zap.String("chat_id", msg.ChatID),
				zap.Int("content_length", len(msg.Content)))

			// 特殊处理 cron 消息：如果 chat_id 为空，尝试路由到配置的飞书聊天
			if msg.Channel == "cron" && msg.ChatID == "" {
				// 查找飞书通道的 cron_output_chat_id 配置
				if feishuChannel, ok := m.Get("feishu"); ok {
					if fc, ok := feishuChannel.(interface{ GetCronOutputChatID() string }); ok {
						cronChatID := fc.GetCronOutputChatID()
						if cronChatID != "" {
							msg.ChatID = cronChatID
							msg.Channel = "feishu"
							logger.Info("Routed cron message to configured Feishu chat",
								zap.String("cron_chat_id", cronChatID))
						}
					}
				}
			}

			// 如果仍然没有 chat_id，跳过此消息
			if msg.ChatID == "" {
				logger.Warn("Outbound message has no chat_id, skipping",
					zap.String("channel", msg.Channel),
					zap.String("account_id", msg.AccountID))
				continue
			}

			// 查找对应的通道
			channel, resolvedName, ok := m.resolveOutboundChannel(msg)
			if !ok {
				logger.Warn("Channel not found for outbound message",
					zap.String("channel", msg.Channel),
					zap.String("account_id", msg.AccountID),
				)
				continue
			}

			// 发送消息
			if err := channel.Send(msg); err != nil {
				logger.Error("Failed to send message via channel",
					zap.String("channel", msg.Channel),
					zap.String("resolved_channel", resolvedName),
					zap.String("account_id", msg.AccountID),
					zap.Error(err),
				)
			} else {
				logger.Debug("Message sent successfully via channel",
					zap.String("channel", msg.Channel),
					zap.String("resolved_channel", resolvedName),
					zap.String("account_id", msg.AccountID),
					zap.String("chat_id", msg.ChatID))
			}
		}
	}
}

// SetupFromConfig 从配置设置通道
func (m *Manager) SetupFromConfig(cfg *config.Config) error {
	// 1. 优先使用新的多账号配置格式
	// 2. 如果没有账号配置，则回退到旧的配置格式

	// Telegram 通道
	if cfg.Channels.Telegram.Enabled {
		if len(cfg.Channels.Telegram.Accounts) > 0 {
			// 多账号配置
			for accountID, accountCfg := range cfg.Channels.Telegram.Accounts {
				if accountCfg.Enabled && accountCfg.Token != "" {
					tgCfg := TelegramConfig{
						BaseChannelConfig: BaseChannelConfig{
							Enabled:    accountCfg.Enabled,
							AccountID:  accountID,
							Name:       accountCfg.Name,
							AllowedIDs: accountCfg.AllowedIDs,
						},
						Token: accountCfg.Token,
					}

					channel, err := NewTelegramChannel(accountID, tgCfg, m.bus)
					if err != nil {
						logger.Error("Failed to create Telegram channel",
							zap.String("account_id", accountID),
							zap.Error(err))
					} else {
						channelName := buildChannelName("telegram", accountID)
						if err := m.RegisterWithName(channel, channelName); err != nil {
							logger.Error("Failed to register Telegram channel",
								zap.String("account_id", accountID),
								zap.Error(err))
						} else {
							logger.Info("Telegram channel registered",
								zap.String("account_id", accountID),
								zap.String("name", channelName))
						}
					}
				}
			}
		} else if cfg.Channels.Telegram.Token != "" {
			// 单账号配置（向后兼容）
			tgCfg := TelegramConfig{
				BaseChannelConfig: BaseChannelConfig{
					Enabled:    cfg.Channels.Telegram.Enabled,
					AccountID:  "default",
					AllowedIDs: cfg.Channels.Telegram.AllowedIDs,
				},
				Token: cfg.Channels.Telegram.Token,
			}

			channel, err := NewTelegramChannel("default", tgCfg, m.bus)
			if err != nil {
				logger.Error("Failed to create Telegram channel", zap.Error(err))
			} else {
				if err := m.Register(channel); err != nil {
					logger.Error("Failed to register Telegram channel", zap.Error(err))
				}
			}
		}
	}

	// WhatsApp 通道
	if cfg.Channels.WhatsApp.Enabled {
		if len(cfg.Channels.WhatsApp.Accounts) > 0 {
			// 多账号配置
			for accountID, accountCfg := range cfg.Channels.WhatsApp.Accounts {
				if accountCfg.Enabled && accountCfg.BridgeURL != "" {
					waCfg := WhatsAppConfig{
						BaseChannelConfig: BaseChannelConfig{
							Enabled:    accountCfg.Enabled,
							AccountID:  accountID,
							Name:       accountCfg.Name,
							AllowedIDs: accountCfg.AllowedIDs,
						},
						BridgeURL: accountCfg.BridgeURL,
					}

					channel, err := NewWhatsAppChannel(waCfg, m.bus)
					if err != nil {
						logger.Error("Failed to create WhatsApp channel",
							zap.String("account_id", accountID),
							zap.Error(err))
					} else {
						channelName := buildChannelName("whatsapp", accountID)
						if err := m.RegisterWithName(channel, channelName); err != nil {
							logger.Error("Failed to register WhatsApp channel",
								zap.String("account_id", accountID),
								zap.Error(err))
						}
					}
				}
			}
		} else if cfg.Channels.WhatsApp.BridgeURL != "" {
			// 单账号配置（向后兼容）
			waCfg := WhatsAppConfig{
				BaseChannelConfig: BaseChannelConfig{
					Enabled:    cfg.Channels.WhatsApp.Enabled,
					AccountID:  "default",
					AllowedIDs: cfg.Channels.WhatsApp.AllowedIDs,
				},
				BridgeURL: cfg.Channels.WhatsApp.BridgeURL,
			}

			channel, err := NewWhatsAppChannel(waCfg, m.bus)
			if err != nil {
				logger.Error("Failed to create WhatsApp channel", zap.Error(err))
			} else {
				if err := m.Register(channel); err != nil {
					logger.Error("Failed to register WhatsApp channel", zap.Error(err))
				}
			}
		}
	}

	// iMessage 通道
	if cfg.Channels.IMessage.Enabled {
		if len(cfg.Channels.IMessage.Accounts) > 0 {
			// 多账号配置
			for accountID, accountCfg := range cfg.Channels.IMessage.Accounts {
				if accountCfg.Enabled && accountCfg.BridgeURL != "" {
					imCfg := IMessageConfig{
						BaseChannelConfig: BaseChannelConfig{
							Enabled:    accountCfg.Enabled,
							AccountID:  accountID,
							Name:       accountCfg.Name,
							AllowedIDs: accountCfg.AllowedIDs,
						},
						BridgeURL: accountCfg.BridgeURL,
					}

					channel, err := NewIMessageChannel(accountID, imCfg, m.bus)
					if err != nil {
						logger.Error("Failed to create iMessage channel",
							zap.String("account_id", accountID),
							zap.Error(err))
					} else {
						channelName := buildChannelName("imessage", accountID)
						if err := m.RegisterWithName(channel, channelName); err != nil {
							logger.Error("Failed to register iMessage channel",
								zap.String("account_id", accountID),
								zap.Error(err))
						}
					}
				}
			}
		} else if cfg.Channels.IMessage.BridgeURL != "" {
			// 单账号配置（向后兼容）
			imCfg := IMessageConfig{
				BaseChannelConfig: BaseChannelConfig{
					Enabled:    cfg.Channels.IMessage.Enabled,
					AccountID:  "default",
					AllowedIDs: cfg.Channels.IMessage.AllowedIDs,
				},
				BridgeURL: cfg.Channels.IMessage.BridgeURL,
			}

			channel, err := NewIMessageChannel("default", imCfg, m.bus)
			if err != nil {
				logger.Error("Failed to create iMessage channel", zap.Error(err))
			} else {
				if err := m.Register(channel); err != nil {
					logger.Error("Failed to register iMessage channel", zap.Error(err))
				}
			}
		}
	}

	// 飞书通道
	if cfg.Channels.Feishu.Enabled {
		if len(cfg.Channels.Feishu.Accounts) > 0 {
			// 多账号配置
			for accountID, accountCfg := range cfg.Channels.Feishu.Accounts {
				if accountCfg.Enabled && accountCfg.AppID != "" {
					fsCfg := config.FeishuChannelConfig{
						Enabled:    accountCfg.Enabled,
						AppID:      accountCfg.AppID,
						AppSecret:  accountCfg.AppSecret,
						AllowedIDs: accountCfg.AllowedIDs,
					}
					channel, err := NewFeishuChannel(fsCfg, m.bus)
					if err != nil {
						logger.Error("Failed to create Feishu channel",
							zap.String("account_id", accountID),
							zap.Error(err))
					} else {
						channelName := buildChannelName("feishu", accountID)
						if err := m.RegisterWithName(channel, channelName); err != nil {
							logger.Error("Failed to register Feishu channel",
								zap.String("account_id", accountID),
								zap.Error(err))
						}
					}
				}
			}
		} else if cfg.Channels.Feishu.AppID != "" {
			// 单账号配置（向后兼容）
			channel, err := NewFeishuChannel(cfg.Channels.Feishu, m.bus)
			if err != nil {
				logger.Error("Failed to create Feishu channel", zap.Error(err))
			} else {
				if err := m.Register(channel); err != nil {
					logger.Error("Failed to register Feishu channel", zap.Error(err))
				}
			}
		}
	}

	// QQ 通道 (使用官方 API)
	if cfg.Channels.QQ.Enabled {
		if len(cfg.Channels.QQ.Accounts) > 0 {
			// 多账号配置
			for accountID, accountCfg := range cfg.Channels.QQ.Accounts {
				if accountCfg.Enabled && accountCfg.AppID != "" {
					qqCfg := config.QQChannelConfig{
						Enabled:    accountCfg.Enabled,
						AppID:      accountCfg.AppID,
						AppSecret:  accountCfg.AppSecret,
						AllowedIDs: accountCfg.AllowedIDs,
					}

					channel, err := NewQQChannel(accountID, qqCfg, m.bus)
					if err != nil {
						logger.Error("Failed to create QQ channel",
							zap.String("account_id", accountID),
							zap.Error(err))
					} else {
						channelName := buildChannelName("qq", accountID)
						if err := m.RegisterWithName(channel, channelName); err != nil {
							logger.Error("Failed to register QQ channel",
								zap.String("account_id", accountID),
								zap.Error(err))
						} else {
							logger.Info("QQ channel registered",
								zap.String("account_id", accountID),
								zap.String("name", channelName))
						}
					}
				}
			}
		} else if cfg.Channels.QQ.AppID != "" {
			// 单账号配置（向后兼容）
			channel, err := NewQQChannel("default", cfg.Channels.QQ, m.bus)
			if err != nil {
				logger.Error("Failed to create QQ channel", zap.Error(err))
			} else {
				if err := m.Register(channel); err != nil {
					logger.Error("Failed to register QQ channel", zap.Error(err))
				}
			}
		}
	}

	// 企业微信通道
	if cfg.Channels.WeWork.Enabled {
		registeredWeWorkAccount := false
		if len(cfg.Channels.WeWork.Accounts) > 0 {
			// 多账号配置
			for accountID, accountCfg := range cfg.Channels.WeWork.Accounts {
				mode := strings.TrimSpace(strings.ToLower(accountCfg.Mode))
				if mode == "" {
					mode = "webhook"
				}
				enabled := accountCfg.Enabled && ((mode == "websocket" && accountCfg.BotID != "") || (mode != "websocket" && accountCfg.CorpID != ""))
				if enabled {
					wwCfg := config.WeWorkChannelConfig{
						Enabled:        accountCfg.Enabled,
						Mode:           mode,
						CorpID:         accountCfg.CorpID,
						AgentID:        accountCfg.AgentID,
						Secret:         accountCfg.AppSecret,
						BotID:          accountCfg.BotID,
						BotSecret:      accountCfg.BotSecret,
						WebSocketURL:   accountCfg.WebSocketURL,
						Token:          accountCfg.Token,
						EncodingAESKey: accountCfg.EncodingAESKey,
						WebhookPort:    accountCfg.WebhookPort,
						AllowedIDs:     accountCfg.AllowedIDs,
					}
					channel, err := NewWeWorkChannel(accountID, wwCfg, m.bus)
					if err != nil {
						logger.Error("Failed to create WeWork channel",
							zap.String("account_id", accountID),
							zap.Error(err))
					} else {
						channelName := buildChannelName("wework", accountID)
						if err := m.RegisterWithName(channel, channelName); err != nil {
							logger.Error("Failed to register WeWork channel",
								zap.String("account_id", accountID),
								zap.Error(err))
						} else {
							registeredWeWorkAccount = true
						}
					}
				}
			}
		}

		if !registeredWeWorkAccount {
			mode := strings.TrimSpace(strings.ToLower(cfg.Channels.WeWork.Mode))
			if mode == "" {
				mode = "webhook"
			}
			enabled := (mode == "websocket" && cfg.Channels.WeWork.BotID != "") || (mode != "websocket" && cfg.Channels.WeWork.CorpID != "")
			if enabled {
				// 单账号配置（向后兼容）
				channel, err := NewWeWorkChannel("default", cfg.Channels.WeWork, m.bus)
				if err != nil {
					logger.Error("Failed to create WeWork channel", zap.Error(err))
				} else {
					if err := m.Register(channel); err != nil {
						logger.Error("Failed to register WeWork channel", zap.Error(err))
					}
				}
			}
		}
	}

	// 钉钉通道
	if cfg.Channels.DingTalk.Enabled {
		if len(cfg.Channels.DingTalk.Accounts) > 0 {
			// 多账号配置
			for accountID, accountCfg := range cfg.Channels.DingTalk.Accounts {
				if accountCfg.Enabled && accountCfg.ClientID != "" {
					dtCfg := config.DingTalkChannelConfig{
						Enabled:      accountCfg.Enabled,
						ClientID:     accountCfg.ClientID,
						ClientSecret: accountCfg.ClientSecret,
						AllowedIDs:   accountCfg.AllowedIDs,
					}
					channel, err := NewDingTalkChannel(dtCfg, m.bus)
					if err != nil {
						logger.Error("Failed to create DingTalk channel",
							zap.String("account_id", accountID),
							zap.Error(err))
					} else {
						channelName := buildChannelName("dingtalk", accountID)
						if err := m.RegisterWithName(channel, channelName); err != nil {
							logger.Error("Failed to register DingTalk channel",
								zap.String("account_id", accountID),
								zap.Error(err))
						}
					}
				}
			}
		} else if cfg.Channels.DingTalk.ClientID != "" {
			// 单账号配置（向后兼容）
			channel, err := NewDingTalkChannel(cfg.Channels.DingTalk, m.bus)
			if err != nil {
				logger.Error("Failed to create DingTalk channel", zap.Error(err))
			} else {
				if err := m.Register(channel); err != nil {
					logger.Error("Failed to register DingTalk channel", zap.Error(err))
				}
			}
		}
	}

	// 如流通道
	if cfg.Channels.Infoflow.Enabled {
		if len(cfg.Channels.Infoflow.Accounts) > 0 {
			// 多账号配置
			for accountID, accountCfg := range cfg.Channels.Infoflow.Accounts {
				if accountCfg.Enabled && accountCfg.WebhookURL != "" {
					ifCfg := InfoflowConfig{
						BaseChannelConfig: BaseChannelConfig{
							Enabled:    accountCfg.Enabled,
							AccountID:  accountID,
							Name:       accountCfg.Name,
							AllowedIDs: accountCfg.AllowedIDs,
						},
						WebhookURL:  accountCfg.WebhookURL,
						Token:       accountCfg.Token,
						AESKey:      accountCfg.AESKey,
						WebhookPort: accountCfg.WebhookPort,
					}

					channel, err := NewInfoflowChannel(accountID, ifCfg, m.bus)
					if err != nil {
						logger.Error("Failed to create Infoflow channel",
							zap.String("account_id", accountID),
							zap.Error(err))
					} else {
						channelName := buildChannelName("infoflow", accountID)
						if err := m.RegisterWithName(channel, channelName); err != nil {
							logger.Error("Failed to register Infoflow channel",
								zap.String("account_id", accountID),
								zap.Error(err))
						}
					}
				}
			}
		} else if cfg.Channels.Infoflow.WebhookURL != "" {
			// 单账号配置（向后兼容）
			ifCfg := InfoflowConfig{
				BaseChannelConfig: BaseChannelConfig{
					Enabled:    cfg.Channels.Infoflow.Enabled,
					AccountID:  "default",
					AllowedIDs: cfg.Channels.Infoflow.AllowedIDs,
				},
				WebhookURL:  cfg.Channels.Infoflow.WebhookURL,
				Token:       cfg.Channels.Infoflow.Token,
				AESKey:      cfg.Channels.Infoflow.AESKey,
				WebhookPort: cfg.Channels.Infoflow.WebhookPort,
			}
			channel, err := NewInfoflowChannel("default", ifCfg, m.bus)
			if err != nil {
				logger.Error("Failed to create Infoflow channel", zap.Error(err))
			} else {
				if err := m.Register(channel); err != nil {
					logger.Error("Failed to register Infoflow channel", zap.Error(err))
				}
			}
		}
	}

	// Gotify 通道
	if cfg.Channels.Gotify.Enabled {
		if len(cfg.Channels.Gotify.Accounts) > 0 {
			// 多账号配置
			for accountID, accountCfg := range cfg.Channels.Gotify.Accounts {
				if accountCfg.Enabled && accountCfg.ServerURL != "" && accountCfg.AppToken != "" {
					gfCfg := GotifyConfig{
						BaseChannelConfig: BaseChannelConfig{
							Enabled:    accountCfg.Enabled,
							AccountID:  accountID,
							Name:       accountCfg.Name,
							AllowedIDs: accountCfg.AllowedIDs,
						},
						ServerURL: accountCfg.ServerURL,
						AppToken:  accountCfg.AppToken,
						Priority:  accountCfg.Priority,
					}

					channel, err := NewGotifyChannel(accountID, gfCfg, m.bus)
					if err != nil {
						logger.Error("Failed to create Gotify channel",
							zap.String("account_id", accountID),
							zap.Error(err))
					} else {
						channelName := buildChannelName("gotify", accountID)
						if err := m.RegisterWithName(channel, channelName); err != nil {
							logger.Error("Failed to register Gotify channel",
								zap.String("account_id", accountID),
								zap.Error(err))
						} else {
							logger.Info("Gotify channel registered",
								zap.String("account_id", accountID),
								zap.String("name", channelName))
						}
					}
				}
			}
		} else if cfg.Channels.Gotify.ServerURL != "" && cfg.Channels.Gotify.AppToken != "" {
			// 单账号配置（向后兼容）
			gfCfg := GotifyConfig{
				BaseChannelConfig: BaseChannelConfig{
					Enabled:    cfg.Channels.Gotify.Enabled,
					AccountID:  "default",
					AllowedIDs: cfg.Channels.Gotify.AllowedIDs,
				},
				ServerURL: cfg.Channels.Gotify.ServerURL,
				AppToken:  cfg.Channels.Gotify.AppToken,
				Priority:  cfg.Channels.Gotify.Priority,
			}

			channel, err := NewGotifyChannel("default", gfCfg, m.bus)
			if err != nil {
				logger.Error("Failed to create Gotify channel", zap.Error(err))
			} else {
				if err := m.Register(channel); err != nil {
					logger.Error("Failed to register Gotify channel", zap.Error(err))
				}
			}
		}
	}

	return nil
}

// buildChannelName 构建通道名称
func buildChannelName(channelType, accountID string) string {
	if accountID == "" || accountID == "default" {
		return channelType
	}
	return channelType + ":" + accountID
}

func baseChannelName(channelName string) string {
	if idx := strings.Index(channelName, ":"); idx > 0 {
		return channelName[:idx]
	}
	return channelName
}

func (m *Manager) resolveOutboundChannel(msg *bus.OutboundMessage) (BaseChannel, string, bool) {
	if msg == nil {
		return nil, "", false
	}

	channelName := strings.TrimSpace(msg.Channel)
	if channelName == "" {
		return nil, "", false
	}

	accountID := strings.TrimSpace(msg.AccountID)
	baseName := baseChannelName(channelName)

	candidates := make([]string, 0, 3)
	if strings.Contains(channelName, ":") {
		candidates = append(candidates, channelName)
	} else if accountID != "" && accountID != "default" {
		candidates = append(candidates, buildChannelName(baseName, accountID))
	}
	candidates = append(candidates, channelName)
	if baseName != "" && baseName != channelName {
		candidates = append(candidates, baseName)
	}

	seen := make(map[string]struct{}, len(candidates))
	for _, candidate := range candidates {
		if candidate == "" {
			continue
		}
		if _, ok := seen[candidate]; ok {
			continue
		}
		seen[candidate] = struct{}{}

		channel, ok := m.Get(candidate)
		if ok {
			return channel, candidate, true
		}
	}

	return nil, "", false
}

// RegisterWithName 使用指定名称注册通道
func (m *Manager) RegisterWithName(channel BaseChannel, name string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if _, ok := m.channels[name]; ok {
		return fmt.Errorf("channel %s already registered", name)
	}

	m.channels[name] = channel
	logger.Info("Channel registered", zap.String("channel", name))
	return nil
}
