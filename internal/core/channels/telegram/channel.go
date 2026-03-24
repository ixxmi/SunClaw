package telegram

import (
	"context"
	"fmt"
	"github.com/smallnest/goclaw/internal/core/channels/shared"
	"io"
	"net/http"
	neturl "net/url"
	"strconv"
	"strings"
	"time"

	telegrambot "github.com/go-telegram-bot-api/telegram-bot-api/v5"
	"github.com/smallnest/goclaw/internal/core/bus"
	"github.com/smallnest/goclaw/internal/logger"
	"go.uber.org/zap"
)

// TelegramChannel Telegram 通道
type TelegramChannel struct {
	*shared.BaseChannelImpl
	bot                *telegrambot.BotAPI
	token              string
	inlineButtonsScope TelegramInlineButtonsScope
}

// TelegramConfig Telegram 配置
type TelegramConfig struct {
	shared.BaseChannelConfig
	Token              string `mapstructure:"token" json:"token"`
	InlineButtonsScope string `mapstructure:"inline_buttons_scope" json:"inline_buttons_scope"`
}

// TelegramInlineButtonsScope controls inline button availability
type TelegramInlineButtonsScope string

const (
	// TelegramInlineButtonsOff disables inline buttons
	TelegramInlineButtonsOff TelegramInlineButtonsScope = "off"
	// TelegramInlineButtonsDM enables inline buttons only in direct messages
	TelegramInlineButtonsDM TelegramInlineButtonsScope = "dm"
	// TelegramInlineButtonsGroup enables inline buttons only in groups
	TelegramInlineButtonsGroup TelegramInlineButtonsScope = "group"
	// TelegramInlineButtonsAll enables inline buttons everywhere
	TelegramInlineButtonsAll TelegramInlineButtonsScope = "all"
	// TelegramInlineButtonsAllowlist enables inline buttons only for whitelisted chats
	TelegramInlineButtonsAllowlist TelegramInlineButtonsScope = "allowlist"
)

// NewTelegramChannel 创建 Telegram 通道
func NewTelegramChannel(accountID string, cfg TelegramConfig, bus *bus.MessageBus) (*TelegramChannel, error) {
	if cfg.Token == "" {
		return nil, fmt.Errorf("telegram token is required")
	}

	bot, err := telegrambot.NewBotAPI(cfg.Token)
	if err != nil {
		return nil, fmt.Errorf("failed to create telegram bot: %w", err)
	}

	// Parse inline buttons scope
	var inlineScope TelegramInlineButtonsScope
	switch strings.ToLower(strings.TrimSpace(cfg.InlineButtonsScope)) {
	case "dm":
		inlineScope = TelegramInlineButtonsDM
	case "group":
		inlineScope = TelegramInlineButtonsGroup
	case "all":
		inlineScope = TelegramInlineButtonsAll
	case "allowlist":
		inlineScope = TelegramInlineButtonsAllowlist
	default:
		inlineScope = TelegramInlineButtonsOff
	}

	return &TelegramChannel{
		BaseChannelImpl:    shared.NewBaseChannelImpl("telegram", accountID, cfg.BaseChannelConfig, bus),
		bot:                bot,
		token:              cfg.Token,
		inlineButtonsScope: inlineScope,
	}, nil
}

// Start 启动 Telegram 通道
func (c *TelegramChannel) Start(ctx context.Context) error {
	if err := c.BaseChannelImpl.Start(ctx); err != nil {
		return err
	}

	logger.Info("Starting Telegram channel",
		zap.String("account_id", c.AccountID()))

	// 获取 bot 信息
	bot, err := c.bot.GetMe()
	if err != nil {
		return fmt.Errorf("failed to get bot info: %w", err)
	}

	logger.Info("Telegram bot started",
		zap.String("bot_name", bot.UserName),
		zap.String("bot_id", strconv.FormatInt(bot.ID, 10)),
	)

	// Telegram long polling cannot work while a webhook is configured.
	// Always clear any stale webhook before starting getUpdates.
	deleteWebhook := telegrambot.DeleteWebhookConfig{
		DropPendingUpdates: false,
	}
	if _, err := c.bot.Request(deleteWebhook); err != nil {
		return fmt.Errorf("failed to clear telegram webhook before long polling: %w", err)
	}

	// 启动消息处理
	go c.receiveUpdates(ctx)

	return nil
}

// Stop 停止 Telegram 通道
func (c *TelegramChannel) Stop() error {
	if c.bot != nil {
		c.bot.StopReceivingUpdates()
	}
	return c.BaseChannelImpl.Stop()
}

func (c *TelegramChannel) SupportsReplyStreamEdit() bool {
	return true
}

// receiveUpdates 接收更新
func (c *TelegramChannel) receiveUpdates(ctx context.Context) {
	u := telegrambot.NewUpdate(0)
	u.Timeout = 60

	updates := c.bot.GetUpdatesChan(u)

	for {
		select {
		case <-ctx.Done():
			logger.Info("Telegram channel stopped by context")
			return
		case <-c.WaitForStop():
			logger.Info("Telegram channel stopped")
			return
		case update := <-updates:
			if err := c.handleUpdate(ctx, &update); err != nil {
				logger.Error("Failed to handle update",
					zap.Error(err),
				)
			}
		}
	}
}

// handleUpdate 处理更新
func (c *TelegramChannel) handleUpdate(ctx context.Context, update *telegrambot.Update) error {
	if update.Message == nil {
		return nil
	}

	message := update.Message

	// 检查权限
	senderID := strconv.FormatInt(message.From.ID, 10)
	if !c.IsAllowed(senderID) {
		logger.Warn("Telegram message from unauthorized sender",
			zap.Int64("sender_id", message.From.ID),
			zap.String("sender_name", message.From.UserName),
		)
		return nil
	}

	// 提取文本内容
	content := ""
	if message.Text != "" {
		content = message.Text
	} else if message.Caption != "" {
		content = message.Caption
	}

	// 处理命令
	if strings.HasPrefix(content, "/") {
		return c.handleCommand(ctx, message, content)
	}

	// 构建入站消息
	msg := &bus.InboundMessage{
		Channel:   c.Name(),
		AccountID: c.AccountID(),
		SenderID:  senderID,
		ChatID:    strconv.FormatInt(message.Chat.ID, 10),
		Content:   content,
		Media:     c.extractMedia(message),
		Metadata: map[string]interface{}{
			"message_id": message.MessageID,
			"from_user":  message.From.UserName,
			"from_name":  message.From.FirstName,
			"chat_type":  message.Chat.Type,
			"reply_to":   message.ReplyToMessage,
		},
		Timestamp: time.Now(),
	}

	return c.PublishInbound(ctx, msg)
}

// handleCommand 处理命令
func (c *TelegramChannel) handleCommand(ctx context.Context, message *telegrambot.Message, command string) error {
	chatID := message.Chat.ID

	switch command {
	case "/start":
		msg := telegrambot.NewMessage(chatID, "👋 欢迎使用 goclaw!\n\n我可以帮助你完成各种任务。发送 /help 查看可用命令。")
		if _, err := c.bot.Send(msg); err != nil {
			return err
		}
	case "/help":
		helpText := `🐾 goclaw 命令列表：

/start - 开始使用
/help - 显示帮助

你可以直接与我对话，我会尽力帮助你！`
		msg := telegrambot.NewMessage(chatID, helpText)
		if _, err := c.bot.Send(msg); err != nil {
			return err
		}
	case "/status":
		statusText := fmt.Sprintf("✅ goclaw 运行中\n\n通道状态: %s", map[bool]string{true: "🟢 在线", false: "🔴 离线"}[c.IsRunning()])
		msg := telegrambot.NewMessage(chatID, statusText)
		if _, err := c.bot.Send(msg); err != nil {
			return err
		}
	}

	return nil
}

// extractMedia 提取媒体
func (c *TelegramChannel) extractMedia(message *telegrambot.Message) []bus.Media {
	var media []bus.Media

	if len(message.Photo) > 0 {
		// 获取最大尺寸的照片
		_ = message.Photo[len(message.Photo)-1]
		media = append(media, bus.Media{
			Type:     "image",
			MimeType: "image/jpeg",
		})
	}

	if message.Document != nil {
		media = append(media, bus.Media{
			Type:     "document",
			MimeType: message.Document.MimeType,
		})
	}

	if message.Voice != nil {
		media = append(media, bus.Media{
			Type:     "audio",
			MimeType: message.Voice.MimeType,
		})
	}

	if message.Video != nil {
		media = append(media, bus.Media{
			Type:     "video",
			MimeType: message.Video.MimeType,
		})
	}

	return media
}

// Send 发送消息
func (c *TelegramChannel) Send(msg *bus.OutboundMessage) error {
	if !c.IsRunning() {
		return fmt.Errorf("telegram channel is not running")
	}

	// 解析 ChatID
	chatID, err := strconv.ParseInt(msg.ChatID, 10, 64)
	if err != nil {
		return fmt.Errorf("invalid chat id: %w", err)
	}

	selectedMedia, hasMedia := shared.SelectFirstSupportedMedia(msg.Media, map[string]bool{
		shared.UnifiedMediaImage: true,
		shared.UnifiedMediaFile:  true,
	})

	chatActionType := telegrambot.ChatTyping
	if hasMedia {
		switch selectedMedia.Type {
		case shared.UnifiedMediaImage:
			chatActionType = telegrambot.ChatUploadPhoto
		case shared.UnifiedMediaFile:
			chatActionType = telegrambot.ChatUploadDocument
		}
	}

	// 发送 chat action，让用户知道 bot 正在处理请求
	action := telegrambot.NewChatAction(chatID, chatActionType)
	if _, err := c.bot.Request(action); err != nil {
		// 忽略 chat action 发送失败，不影响主消息
		logger.Debug("Failed to send telegram chat action", zap.Error(err))
	}

	// 统一媒体发送：Telegram 支持 image/file
	if hasMedia {
		if err := c.sendUnifiedMediaMessage(chatID, selectedMedia, msg.Content, msg.ReplyTo); err == nil {
			logger.Info("Telegram media message sent",
				zap.Int64("chat_id", chatID),
				zap.String("media_type", selectedMedia.Type),
			)
			return nil
		} else {
			logger.Warn("Failed to send telegram media, fallback to text", zap.Error(err))
		}
	}

	// 仅文本发送
	tgMsg := telegrambot.NewMessage(chatID, msg.Content)

	// 解析回复
	if replyToID, ok := parseTelegramReplyToID(msg.ReplyTo); ok {
		tgMsg.ReplyToMessageID = replyToID
	}

	_, err = c.bot.Send(tgMsg)
	if err != nil {
		return fmt.Errorf("failed to send telegram message: %w", err)
	}

	logger.Info("Telegram message sent",
		zap.Int64("chat_id", chatID),
		zap.Int("content_length", len(msg.Content)),
	)

	return nil
}

func (c *TelegramChannel) sendUnifiedMediaMessage(chatID int64, media bus.Media, caption, replyTo string) error {
	replyToID, _ := parseTelegramReplyToID(replyTo)

	t := shared.NormalizeMediaType(media.Type)
	if t == shared.UnifiedMediaImage {
		file, err := c.buildTelegramUpload(media, 10<<20, "image.jpg")
		if err != nil {
			return err
		}
		cfg := telegrambot.NewPhoto(chatID, file)
		cfg.Caption = caption
		if replyToID > 0 {
			cfg.ReplyToMessageID = replyToID
		}
		_, err = c.bot.Send(cfg)
		return err
	}

	if t == shared.UnifiedMediaFile {
		file, err := c.buildTelegramUpload(media, 50<<20, "attachment")
		if err != nil {
			return err
		}
		cfg := telegrambot.NewDocument(chatID, file)
		cfg.Caption = caption
		if replyToID > 0 {
			cfg.ReplyToMessageID = replyToID
		}
		_, err = c.bot.Send(cfg)
		return err
	}

	return fmt.Errorf("unsupported telegram media type: %s", media.Type)
}

func (c *TelegramChannel) buildTelegramUpload(media bus.Media, maxBytes int64, fallbackName string) (telegrambot.RequestFileData, error) {
	data, err := c.materializeTelegramUploadData(media, maxBytes)
	if err != nil {
		return nil, err
	}
	return telegrambot.FileBytes{
		Name:  shared.InferMediaFileName(media, fallbackName),
		Bytes: data,
	}, nil
}

func (c *TelegramChannel) materializeTelegramUploadData(media bus.Media, maxBytes int64) ([]byte, error) {
	if strings.TrimSpace(media.Base64) != "" {
		data, err := shared.DecodeBase64Media(media.Base64)
		if err != nil {
			return nil, err
		}
		if maxBytes > 0 && int64(len(data)) > maxBytes {
			return nil, fmt.Errorf("media exceeds size limit: %d > %d", len(data), maxBytes)
		}
		return data, nil
	}

	rawURL := strings.TrimSpace(media.URL)
	if rawURL == "" {
		return nil, fmt.Errorf("media has neither base64 nor url")
	}

	parsed, err := neturl.Parse(rawURL)
	if err != nil {
		return nil, fmt.Errorf("invalid media url: %w", err)
	}
	if !strings.EqualFold(parsed.Scheme, "http") && !strings.EqualFold(parsed.Scheme, "https") {
		return nil, fmt.Errorf("telegram official Bot API requires uploaded bytes or an http(s) media URL, got %q", rawURL)
	}

	req, err := http.NewRequest(http.MethodGet, rawURL, nil)
	if err != nil {
		return nil, fmt.Errorf("build media download request failed: %w", err)
	}

	client := c.bot.Client
	if client == nil {
		client = http.DefaultClient
	}

	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("download media failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("download media failed with status %d", resp.StatusCode)
	}

	reader := io.Reader(resp.Body)
	if maxBytes > 0 {
		reader = io.LimitReader(resp.Body, maxBytes+1)
	}
	data, err := io.ReadAll(reader)
	if err != nil {
		return nil, fmt.Errorf("read media body failed: %w", err)
	}
	if maxBytes > 0 && int64(len(data)) > maxBytes {
		return nil, fmt.Errorf("media exceeds size limit: %d > %d", len(data), maxBytes)
	}
	return data, nil
}

func parseTelegramReplyToID(replyTo string) (int, bool) {
	trimmed := strings.TrimSpace(replyTo)
	if trimmed == "" {
		return 0, false
	}

	replyToID, err := strconv.Atoi(trimmed)
	if err != nil || replyToID <= 0 {
		logger.Debug("Ignoring non-numeric telegram reply_to",
			zap.String("id", trimmed),
			zap.Error(err))
		return 0, false
	}

	return replyToID, true
}

// SendTypingIndicator 发送正在输入指示器
func (c *TelegramChannel) SendTypingIndicator(chatID int64) error {
	if !c.IsRunning() {
		return fmt.Errorf("telegram channel is not running")
	}

	action := telegrambot.NewChatAction(chatID, telegrambot.ChatTyping)
	_, err := c.bot.Request(action)
	return err
}

// SendStream sends streaming messages (edits original message progressively)
func (c *TelegramChannel) SendStream(chatID string, stream <-chan *bus.StreamMessage) error {
	if !c.IsRunning() {
		return fmt.Errorf("telegram channel is not running")
	}

	parsedChatID, err := strconv.ParseInt(chatID, 10, 64)
	if err != nil {
		return fmt.Errorf("invalid chat id: %w", err)
	}

	var messageID int
	var content strings.Builder
	var replyTo string

	for msg := range stream {
		if msg.Error != "" {
			return fmt.Errorf("stream error: %s", msg.Error)
		}
		if replyTo == "" && msg.Metadata != nil {
			if streamReplyTo, ok := msg.Metadata["reply_to"].(string); ok {
				replyTo = streamReplyTo
			}
		}

		if !msg.IsThinking && !msg.IsFinal {
			content.WriteString(msg.Content)
		}

		if messageID == 0 && content.Len() > 0 {
			// Send initial message
			tgMsg := telegrambot.NewMessage(parsedChatID, content.String())
			if replyToID, ok := parseTelegramReplyToID(replyTo); ok {
				tgMsg.ReplyToMessageID = replyToID
			}

			sentMsg, err := c.bot.Send(tgMsg)
			if err != nil {
				return fmt.Errorf("failed to send initial telegram message: %w", err)
			}
			messageID = sentMsg.MessageID
		} else if messageID != 0 && content.Len() > 0 {
			// Edit the message
			edit := telegrambot.NewEditMessageText(parsedChatID, messageID, content.String())

			if _, err := c.bot.Send(edit); err != nil {
				return fmt.Errorf("failed to update telegram message: %w", err)
			}
		}

		if msg.IsComplete {
			logger.Info("Telegram streaming completed",
				zap.Int64("chat_id", parsedChatID),
				zap.Int("message_id", messageID),
				zap.Int("content_length", content.Len()),
			)
			return nil
		}
	}

	return nil
}

// ============================================
// Telegram Inline Buttons Support
// ============================================

// TelegramInlineButton represents an inline button
type TelegramInlineButton struct {
	// Text is the button label
	Text string `json:"text"`
	// CallbackData is the data sent when button is clicked (for callback buttons)
	CallbackData string `json:"callback_data,omitempty"`
	// URL is the URL to open (for URL buttons)
	URL string `json:"url,omitempty"`
	// WebAppURL is the URL of a Web App to open (for web app buttons)
	WebAppURL string `json:"web_app_url,omitempty"`
}

// TelegramInlineKeyboardRow represents a row of inline buttons
type TelegramInlineKeyboardRow struct {
	Buttons []TelegramInlineButton `json:"buttons"`
}

// SendMessageWithButtons sends a message with inline keyboard buttons
func (c *TelegramChannel) SendMessageWithButtons(
	chatID int64,
	text string,
	keyboard [][]TelegramInlineButton,
	parseMode string,
) error {
	if !c.IsRunning() {
		return fmt.Errorf("telegram channel is not running")
	}

	// Create message with inline keyboard
	msg := telegrambot.NewMessage(chatID, text)
	if parseMode != "" {
		msg.ParseMode = parseMode
	}

	// Build inline keyboard markup
	if len(keyboard) > 0 {
		inlineKeyboard := c.buildInlineKeyboard(keyboard)
		msg.ReplyMarkup = &inlineKeyboard
	}

	_, err := c.bot.Send(msg)
	if err != nil {
		return fmt.Errorf("failed to send message with buttons: %w", err)
	}

	logger.Info("Telegram message with buttons sent",
		zap.Int64("chat_id", chatID),
		zap.Int("button_count", countButtons(keyboard)),
	)

	return nil
}

// EditMessageReplyMarkup edits the reply markup of a message (to update buttons)
func (c *TelegramChannel) EditMessageReplyMarkup(
	chatID int64,
	messageID int,
	keyboard [][]TelegramInlineButton,
) error {
	if !c.IsRunning() {
		return fmt.Errorf("telegram channel is not running")
	}

	if len(keyboard) > 0 {
		replyMarkup := c.buildInlineKeyboard(keyboard)
		edit := telegrambot.NewEditMessageReplyMarkup(chatID, messageID, replyMarkup)
		_, err := c.bot.Send(edit)
		if err != nil {
			return fmt.Errorf("failed to edit message reply markup: %w", err)
		}
	} else {
		// Remove keyboard by passing empty markup
		edit := telegrambot.NewEditMessageReplyMarkup(chatID, messageID, telegrambot.InlineKeyboardMarkup{})
		_, err := c.bot.Send(edit)
		if err != nil {
			return fmt.Errorf("failed to edit message reply markup: %w", err)
		}
	}

	return nil
}

// AnswerCallbackQuery answers a callback query from an inline button
func (c *TelegramChannel) AnswerCallbackQuery(
	callbackQueryID string,
	text string,
	showAlert bool,
) error {
	if !c.IsRunning() {
		return fmt.Errorf("telegram channel is not running")
	}

	callback := telegrambot.NewCallback(callbackQueryID, text)
	callback.ShowAlert = showAlert

	_, err := c.bot.Request(callback)
	if err != nil {
		return fmt.Errorf("failed to answer callback query: %w", err)
	}

	return nil
}

// buildInlineKeyboard builds Telegram inline keyboard from our format
func (c *TelegramChannel) buildInlineKeyboard(keyboard [][]TelegramInlineButton) telegrambot.InlineKeyboardMarkup {
	rows := make([][]telegrambot.InlineKeyboardButton, len(keyboard))

	for i, row := range keyboard {
		buttons := make([]telegrambot.InlineKeyboardButton, len(row))
		for j, btn := range row {
			button := telegrambot.InlineKeyboardButton{
				Text: btn.Text,
			}

			if btn.CallbackData != "" {
				button.CallbackData = &btn.CallbackData
			}

			if btn.URL != "" {
				button.URL = &btn.URL
			}

			buttons[j] = button
		}

		rows[i] = buttons
	}

	return telegrambot.InlineKeyboardMarkup{
		InlineKeyboard: rows,
	}
}

// IsInlineButtonsEnabled checks if inline buttons are enabled for the given chat
func (c *TelegramChannel) IsInlineButtonsEnabled(chatType string, chatID string) bool {
	switch c.inlineButtonsScope {
	case TelegramInlineButtonsOff:
		return false
	case TelegramInlineButtonsDM:
		return chatType == "private"
	case TelegramInlineButtonsGroup:
		return chatType == "group" || chatType == "supergroup"
	case TelegramInlineButtonsAll:
		return true
	case TelegramInlineButtonsAllowlist:
		return c.IsAllowed(chatID)
	default:
		return false
	}
}

// countButtons counts total buttons in keyboard
func countButtons(keyboard [][]TelegramInlineButton) int {
	count := 0
	for _, row := range keyboard {
		count += len(row)
	}
	return count
}
