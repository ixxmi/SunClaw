package config

import "strings"

const (
	ReplyDeliveryModeSingle     = "single"
	ReplyDeliveryModeMultiPush  = "multi_push"
	ReplyDeliveryModeStreamEdit = "stream_edit"
	ReplyDeliveryModeHybrid     = "hybrid"

	DefaultReplyDeliveryMinChunkChars = 48
	DefaultReplyDeliveryMaxChunkChars = 160
	DefaultReplyDeliveryMinDelayMs    = 300
	DefaultReplyDeliveryMaxDelayMs    = 900
	DefaultReplyDeliveryMaxPushCount  = 3
)

func DefaultReplyDeliveryConfig() ReplyDeliveryConfig {
	return ReplyDeliveryConfig{
		Mode:          ReplyDeliveryModeSingle,
		MinChunkChars: DefaultReplyDeliveryMinChunkChars,
		MaxChunkChars: DefaultReplyDeliveryMaxChunkChars,
		MinDelayMs:    DefaultReplyDeliveryMinDelayMs,
		MaxDelayMs:    DefaultReplyDeliveryMaxDelayMs,
		MaxPushCount:  DefaultReplyDeliveryMaxPushCount,
	}
}

func NormalizeReplyDeliveryMode(mode string) string {
	switch strings.ToLower(strings.TrimSpace(mode)) {
	case ReplyDeliveryModeSingle:
		return ReplyDeliveryModeSingle
	case ReplyDeliveryModeMultiPush:
		return ReplyDeliveryModeMultiPush
	case ReplyDeliveryModeStreamEdit:
		return ReplyDeliveryModeStreamEdit
	case ReplyDeliveryModeHybrid:
		return ReplyDeliveryModeHybrid
	default:
		return ""
	}
}

func MergeReplyDeliveryConfig(base, override ReplyDeliveryConfig) ReplyDeliveryConfig {
	result := base
	if mode := NormalizeReplyDeliveryMode(override.Mode); mode != "" {
		result.Mode = mode
	}
	if override.MinChunkChars > 0 {
		result.MinChunkChars = override.MinChunkChars
	}
	if override.MaxChunkChars > 0 {
		result.MaxChunkChars = override.MaxChunkChars
	}
	if override.MinDelayMs > 0 {
		result.MinDelayMs = override.MinDelayMs
	}
	if override.MaxDelayMs > 0 {
		result.MaxDelayMs = override.MaxDelayMs
	}
	if override.MaxPushCount > 0 {
		result.MaxPushCount = override.MaxPushCount
	}
	return result
}

func (cfg *Config) ResolveReplyDelivery(channel, accountID string) ReplyDeliveryConfig {
	result := ReplyDeliveryConfig{}
	if cfg == nil {
		return result
	}

	result = MergeReplyDeliveryConfig(result, cfg.ReplyDelivery)
	channel = normalizeReplyDeliveryChannel(channel)
	accountID = strings.TrimSpace(accountID)

	switch channel {
	case "telegram":
		result = MergeReplyDeliveryConfig(result, cfg.Channels.Telegram.ReplyDelivery)
		if account, ok := cfg.Channels.Telegram.Accounts[accountID]; ok {
			result = MergeReplyDeliveryConfig(result, account.ReplyDelivery)
		}
	case "whatsapp":
		result = MergeReplyDeliveryConfig(result, cfg.Channels.WhatsApp.ReplyDelivery)
		if account, ok := cfg.Channels.WhatsApp.Accounts[accountID]; ok {
			result = MergeReplyDeliveryConfig(result, account.ReplyDelivery)
		}
	case "weixin":
		result = MergeReplyDeliveryConfig(result, cfg.Channels.Weixin.ReplyDelivery)
		if account, ok := cfg.Channels.Weixin.Accounts[accountID]; ok {
			result = MergeReplyDeliveryConfig(result, account.ReplyDelivery)
		}
	case "imessage":
		result = MergeReplyDeliveryConfig(result, cfg.Channels.IMessage.ReplyDelivery)
		if account, ok := cfg.Channels.IMessage.Accounts[accountID]; ok {
			result = MergeReplyDeliveryConfig(result, account.ReplyDelivery)
		}
	case "feishu":
		result = MergeReplyDeliveryConfig(result, cfg.Channels.Feishu.ReplyDelivery)
		if account, ok := cfg.Channels.Feishu.Accounts[accountID]; ok {
			result = MergeReplyDeliveryConfig(result, account.ReplyDelivery)
		}
	case "qq":
		result = MergeReplyDeliveryConfig(result, cfg.Channels.QQ.ReplyDelivery)
		if account, ok := cfg.Channels.QQ.Accounts[accountID]; ok {
			result = MergeReplyDeliveryConfig(result, account.ReplyDelivery)
		}
	case "wework":
		result = MergeReplyDeliveryConfig(result, cfg.Channels.WeWork.ReplyDelivery)
		if account, ok := cfg.Channels.WeWork.Accounts[accountID]; ok {
			result = MergeReplyDeliveryConfig(result, account.ReplyDelivery)
		}
	case "dingtalk":
		result = MergeReplyDeliveryConfig(result, cfg.Channels.DingTalk.ReplyDelivery)
		if account, ok := cfg.Channels.DingTalk.Accounts[accountID]; ok {
			result = MergeReplyDeliveryConfig(result, account.ReplyDelivery)
		}
	case "infoflow":
		result = MergeReplyDeliveryConfig(result, cfg.Channels.Infoflow.ReplyDelivery)
		if account, ok := cfg.Channels.Infoflow.Accounts[accountID]; ok {
			result = MergeReplyDeliveryConfig(result, account.ReplyDelivery)
		}
	case "gotify":
		result = MergeReplyDeliveryConfig(result, cfg.Channels.Gotify.ReplyDelivery)
		if account, ok := cfg.Channels.Gotify.Accounts[accountID]; ok {
			result = MergeReplyDeliveryConfig(result, account.ReplyDelivery)
		}
	}

	return result
}

func normalizeReplyDeliveryChannel(channel string) string {
	channel = strings.TrimSpace(channel)
	if idx := strings.Index(channel, ":"); idx > 0 {
		return channel[:idx]
	}
	return channel
}
