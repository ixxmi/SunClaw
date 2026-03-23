package channels

import (
	"context"

	"github.com/smallnest/goclaw/internal/core/bus"
	dingtalkchannel "github.com/smallnest/goclaw/internal/core/channels/dingtalk"
	discordchannel "github.com/smallnest/goclaw/internal/core/channels/discord"
	feishuchannel "github.com/smallnest/goclaw/internal/core/channels/feishu"
	googlechatchannel "github.com/smallnest/goclaw/internal/core/channels/googlechat"
	gotifychannel "github.com/smallnest/goclaw/internal/core/channels/gotify"
	imessagechannel "github.com/smallnest/goclaw/internal/core/channels/imessage"
	infoflowchannel "github.com/smallnest/goclaw/internal/core/channels/infoflow"
	qqchannel "github.com/smallnest/goclaw/internal/core/channels/qq"
	slackchannel "github.com/smallnest/goclaw/internal/core/channels/slack"
	teamschannel "github.com/smallnest/goclaw/internal/core/channels/teams"
	telegramchannel "github.com/smallnest/goclaw/internal/core/channels/telegram"
	weixinchannel "github.com/smallnest/goclaw/internal/core/channels/weixin"
	weworkchannel "github.com/smallnest/goclaw/internal/core/channels/wework"
	whatsappchannel "github.com/smallnest/goclaw/internal/core/channels/whatsapp"
	"github.com/smallnest/goclaw/internal/core/config"
)

type DingTalkChannel = dingtalkchannel.DingTalkChannel

func NewDingTalkChannel(cfg config.DingTalkChannelConfig, bus *bus.MessageBus) (*DingTalkChannel, error) {
	return dingtalkchannel.NewDingTalkChannel(cfg, bus)
}

type DiscordChannel = discordchannel.DiscordChannel
type DiscordConfig = discordchannel.DiscordConfig

func NewDiscordChannel(cfg DiscordConfig, bus *bus.MessageBus) (*DiscordChannel, error) {
	return discordchannel.NewDiscordChannel(cfg, bus)
}

type FeishuChannel = feishuchannel.FeishuChannel

func NewFeishuChannel(cfg config.FeishuChannelConfig, bus *bus.MessageBus) (*FeishuChannel, error) {
	return feishuchannel.NewFeishuChannel(cfg, bus)
}

type GoogleChatChannel = googlechatchannel.GoogleChatChannel
type GoogleChatConfig = googlechatchannel.GoogleChatConfig

func NewGoogleChatChannel(cfg GoogleChatConfig, bus *bus.MessageBus) (*GoogleChatChannel, error) {
	return googlechatchannel.NewGoogleChatChannel(cfg, bus)
}

type GotifyChannel = gotifychannel.GotifyChannel
type GotifyConfig = gotifychannel.GotifyConfig

func NewGotifyChannel(accountID string, cfg GotifyConfig, bus *bus.MessageBus) (*GotifyChannel, error) {
	return gotifychannel.NewGotifyChannel(accountID, cfg, bus)
}

type IMessageChannel = imessagechannel.IMessageChannel
type IMessageConfig = imessagechannel.IMessageConfig

func NewIMessageChannel(accountID string, cfg IMessageConfig, bus *bus.MessageBus) (*IMessageChannel, error) {
	return imessagechannel.NewIMessageChannel(accountID, cfg, bus)
}

type InfoflowChannel = infoflowchannel.InfoflowChannel
type InfoflowConfig = infoflowchannel.InfoflowConfig

func NewInfoflowChannel(accountID string, cfg InfoflowConfig, bus *bus.MessageBus) (*InfoflowChannel, error) {
	return infoflowchannel.NewInfoflowChannel(accountID, cfg, bus)
}

type QQChannel = qqchannel.QQChannel

func NewQQChannel(accountID string, cfg config.QQChannelConfig, bus *bus.MessageBus) (*QQChannel, error) {
	return qqchannel.NewQQChannel(accountID, cfg, bus)
}

type SlackChannel = slackchannel.SlackChannel
type SlackConfig = slackchannel.SlackConfig

func NewSlackChannel(cfg SlackConfig, bus *bus.MessageBus) (*SlackChannel, error) {
	return slackchannel.NewSlackChannel(cfg, bus)
}

type TeamsChannel = teamschannel.TeamsChannel
type TeamsConfig = teamschannel.TeamsConfig

func NewTeamsChannel(cfg TeamsConfig, bus *bus.MessageBus) (*TeamsChannel, error) {
	return teamschannel.NewTeamsChannel(cfg, bus)
}

type TelegramChannel = telegramchannel.TelegramChannel
type TelegramConfig = telegramchannel.TelegramConfig
type TelegramInlineButtonsScope = telegramchannel.TelegramInlineButtonsScope

const (
	TelegramInlineButtonsOff       TelegramInlineButtonsScope = telegramchannel.TelegramInlineButtonsOff
	TelegramInlineButtonsDM        TelegramInlineButtonsScope = telegramchannel.TelegramInlineButtonsDM
	TelegramInlineButtonsGroup     TelegramInlineButtonsScope = telegramchannel.TelegramInlineButtonsGroup
	TelegramInlineButtonsAll       TelegramInlineButtonsScope = telegramchannel.TelegramInlineButtonsAll
	TelegramInlineButtonsAllowlist TelegramInlineButtonsScope = telegramchannel.TelegramInlineButtonsAllowlist
)

func NewTelegramChannel(accountID string, cfg TelegramConfig, bus *bus.MessageBus) (*TelegramChannel, error) {
	return telegramchannel.NewTelegramChannel(accountID, cfg, bus)
}

type WeixinChannel = weixinchannel.WeixinChannel
type WeixinConfig = weixinchannel.WeixinConfig
type WeixinBridgeMessage = weixinchannel.WeixinBridgeMessage
type WeixinDirectLoginOptions = weixinchannel.WeixinDirectLoginOptions
type WeixinDirectLoginResult = weixinchannel.WeixinDirectLoginResult

const (
	weixinModeBridge = weixinchannel.ModeBridge
	weixinModeDirect = weixinchannel.ModeDirect
)

func NewWeixinChannel(accountID string, cfg WeixinConfig, bus *bus.MessageBus) (*WeixinChannel, error) {
	return weixinchannel.NewWeixinChannel(accountID, cfg, bus)
}

func resolveWeixinRuntimeMode(cfg WeixinConfig) string {
	return weixinchannel.ResolveRuntimeMode(cfg)
}

func PerformWeixinDirectLogin(ctx context.Context, opts WeixinDirectLoginOptions) (*WeixinDirectLoginResult, error) {
	return weixinchannel.PerformWeixinDirectLogin(ctx, opts)
}

type WeWorkChannel = weworkchannel.WeWorkChannel

func NewWeWorkChannel(accountID string, cfg config.WeWorkChannelConfig, bus *bus.MessageBus) (*WeWorkChannel, error) {
	return weworkchannel.NewWeWorkChannel(accountID, cfg, bus)
}

type WhatsAppChannel = whatsappchannel.WhatsAppChannel
type WhatsAppConfig = whatsappchannel.WhatsAppConfig
type WhatsAppMessage = whatsappchannel.WhatsAppMessage

func NewWhatsAppChannel(cfg WhatsAppConfig, bus *bus.MessageBus) (*WhatsAppChannel, error) {
	return whatsappchannel.NewWhatsAppChannel(cfg, bus)
}
