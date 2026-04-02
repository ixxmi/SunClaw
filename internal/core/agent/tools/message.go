package tools

import (
	"context"
	"encoding/base64"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/smallnest/goclaw/internal/core/agent/tooltypes"
	"github.com/smallnest/goclaw/internal/core/bus"
	"github.com/smallnest/goclaw/internal/core/channels"
	"github.com/smallnest/goclaw/internal/core/execution"
	"github.com/smallnest/goclaw/internal/logger"
	"go.uber.org/zap"
)

// MessageTool 消息工具
type MessageTool struct {
	bus          *bus.MessageBus
	currentChan  string
	currentChat  string
	workspace    string
	allowedPaths []string
	deniedPaths  []string
}

// NewMessageTool 创建消息工具
func NewMessageTool(bus *bus.MessageBus, workspace string, allowedPaths, deniedPaths []string) *MessageTool {
	return &MessageTool{
		bus:          bus,
		workspace:    workspace,
		allowedPaths: append([]string(nil), allowedPaths...),
		deniedPaths:  append([]string(nil), deniedPaths...),
	}
}

// SetCurrent 设置当前通道和聊天
func (t *MessageTool) SetCurrent(channel, chatID string) {
	t.currentChan = channel
	t.currentChat = chatID
}

// SendMessage 发送消息
func (t *MessageTool) SendMessage(ctx context.Context, params map[string]interface{}) (string, error) {
	content, ok := params["content"].(string)
	if !ok || strings.TrimSpace(content) == "" {
		return "", fmt.Errorf("content parameter is required")
	}

	// 过滤中间态错误和拒绝消息
	if isFilteredContent(content) {
		logger.Warn("Message tool send was filtered out",
			zap.Int("content_length", len(content)))
		// 返回成功但不实际发送消息
		return "Message was filtered and not sent", nil
	}

	channel, accountID, chatID, replyTo, err := t.resolveTarget(ctx, params)
	if err != nil {
		return "", err
	}

	// 发送消息
	msg := &bus.OutboundMessage{
		Channel:   channel,
		AccountID: accountID,
		ChatID:    chatID,
		Content:   strings.TrimSpace(content),
		ReplyTo:   replyTo,
		Timestamp: time.Now(),
	}

	if err := t.bus.PublishOutbound(ctx, msg); err != nil {
		return "", fmt.Errorf("failed to send message: %w", err)
	}

	return fmt.Sprintf("Message sent to %s:%s", channel, chatID), nil
}

// SendFile 发送图片或文件
func (t *MessageTool) SendFile(ctx context.Context, params map[string]interface{}) (string, error) {
	channel, accountID, chatID, replyTo, err := t.resolveTarget(ctx, params)
	if err != nil {
		return "", err
	}

	content, _ := params["content"].(string)
	content = strings.TrimSpace(content)
	if content != "" && isFilteredContent(content) {
		logger.Warn("File tool caption was filtered out",
			zap.Int("content_length", len(content)))
		content = ""
	}

	media, err := t.buildOutboundMedia(ctx, params)
	if err != nil {
		return "", err
	}

	msg := &bus.OutboundMessage{
		Channel:   channel,
		AccountID: accountID,
		ChatID:    chatID,
		Content:   content,
		Media:     []bus.Media{media},
		ReplyTo:   replyTo,
		Timestamp: time.Now(),
	}

	if err := t.bus.PublishOutbound(ctx, msg); err != nil {
		return "", fmt.Errorf("failed to send file: %w", err)
	}

	return fmt.Sprintf("%s sent to %s:%s", media.Type, channel, chatID), nil
}

func (t *MessageTool) resolveTarget(ctx context.Context, params map[string]interface{}) (channel, accountID, chatID, replyTo string, err error) {
	channel = t.currentChan
	if ch := contextString(ctx, "channel"); ch != "" {
		channel = ch
	}
	if ch, ok := params["channel"].(string); ok && strings.TrimSpace(ch) != "" {
		channel = strings.TrimSpace(ch)
	}

	chatID = t.currentChat
	if cid := contextString(ctx, "chat_id"); cid != "" {
		chatID = cid
	}
	if cid, ok := params["chat_id"].(string); ok && strings.TrimSpace(cid) != "" {
		chatID = strings.TrimSpace(cid)
	}

	accountID = contextString(ctx, "account_id")
	if aid, ok := params["account_id"].(string); ok && strings.TrimSpace(aid) != "" {
		accountID = strings.TrimSpace(aid)
	}

	if rid, ok := params["reply_to"].(string); ok && strings.TrimSpace(rid) != "" {
		replyTo = strings.TrimSpace(rid)
	}

	if channel == "" || chatID == "" {
		return "", "", "", "", fmt.Errorf("channel and chat_id are required")
	}

	return channel, accountID, chatID, replyTo, nil
}

func (t *MessageTool) buildOutboundMedia(ctx context.Context, params map[string]interface{}) (bus.Media, error) {
	mediaType, err := normalizeMessageToolMediaType(stringParam(params, "media_type"))
	if err != nil {
		return bus.Media{}, err
	}

	filePath := stringParam(params, "file_path")
	fileURL := stringParam(params, "file_url")
	base64Data := stringParam(params, "base64_data")

	sourceCount := 0
	if filePath != "" {
		sourceCount++
	}
	if fileURL != "" {
		sourceCount++
	}
	if base64Data != "" {
		sourceCount++
	}
	if sourceCount != 1 {
		return bus.Media{}, fmt.Errorf("exactly one of file_path, file_url, or base64_data is required")
	}

	media := bus.Media{
		Type:     mediaType,
		Name:     stringParam(params, "file_name"),
		MimeType: stringParam(params, "mime_type"),
	}

	switch {
	case filePath != "":
		resolvedPath, err := t.resolveFilePath(ctx, filePath)
		if err != nil {
			return bus.Media{}, err
		}
		data, err := os.ReadFile(resolvedPath)
		if err != nil {
			return bus.Media{}, fmt.Errorf("failed to read file_path %s: %w", resolvedPath, err)
		}
		media.Base64 = base64.StdEncoding.EncodeToString(data)
		if media.Name == "" {
			media.Name = filepath.Base(resolvedPath)
		}
		if media.MimeType == "" && len(data) > 0 {
			media.MimeType = http.DetectContentType(data)
		}
	case fileURL != "":
		media.URL = fileURL
	case base64Data != "":
		decoded, err := channels.DecodeBase64Media(base64Data)
		if err != nil {
			return bus.Media{}, fmt.Errorf("invalid base64_data: %w", err)
		}
		media.Base64 = base64Data
		if media.MimeType == "" && len(decoded) > 0 {
			media.MimeType = http.DetectContentType(decoded)
		}
	}

	if media.Name == "" {
		fallbackName := "attachment"
		if media.Type == channels.UnifiedMediaImage {
			fallbackName = "image.jpg"
		}
		media.Name = channels.InferMediaFileName(media, fallbackName)
	}

	return media, nil
}

func (t *MessageTool) resolveFilePath(ctx context.Context, rawPath string) (string, error) {
	trimmed := strings.TrimSpace(rawPath)
	if trimmed == "" {
		return "", fmt.Errorf("file_path cannot be empty")
	}

	if !filepath.IsAbs(trimmed) {
		base := strings.TrimSpace(t.workspace)
		if root := contextString(ctx, "workspace_root"); root != "" {
			base = root
		}
		if strings.TrimSpace(base) == "" {
			var err error
			base, err = os.Getwd()
			if err != nil {
				return "", fmt.Errorf("failed to resolve working directory: %w", err)
			}
		}
		trimmed = filepath.Join(base, trimmed)
	}

	resolvedPath, err := filepath.Abs(trimmed)
	if err != nil {
		return "", fmt.Errorf("failed to resolve file_path: %w", err)
	}
	if !t.isAllowed(resolvedPath) {
		return "", fmt.Errorf("access to file_path %s is not allowed", resolvedPath)
	}
	return resolvedPath, nil
}

func (t *MessageTool) isAllowed(path string) bool {
	absPath, err := filepath.Abs(path)
	if err != nil {
		return false
	}

	for _, denied := range t.deniedPaths {
		absDenied, err := filepath.Abs(denied)
		if err == nil && strings.HasPrefix(absPath, absDenied) {
			return false
		}
	}

	if len(t.allowedPaths) == 0 {
		return true
	}

	for _, allowed := range t.allowedPaths {
		absAllowed, err := filepath.Abs(allowed)
		if err == nil && strings.HasPrefix(absPath, absAllowed) {
			return true
		}
	}

	return false
}

func stringParam(params map[string]interface{}, key string) string {
	value, _ := params[key].(string)
	return strings.TrimSpace(value)
}

func normalizeMessageToolMediaType(raw string) (string, error) {
	normalized := channels.NormalizeMediaType(raw)
	if normalized == "" {
		return channels.UnifiedMediaFile, nil
	}
	switch normalized {
	case channels.UnifiedMediaImage, channels.UnifiedMediaFile:
		return normalized, nil
	default:
		return "", fmt.Errorf("unsupported media_type %q, must be image or file", raw)
	}
}

func contextString(ctx context.Context, key string) string {
	switch key {
	case "session_key":
		return execution.SessionKey(ctx)
	case "agent_id":
		return execution.AgentID(ctx)
	case "bootstrap_owner_id":
		return execution.BootstrapOwnerID(ctx)
	case "workspace_root":
		return execution.WorkspaceRoot(ctx)
	case "channel":
		return execution.Channel(ctx)
	case "account_id":
		return execution.AccountID(ctx)
	case "chat_id":
		return execution.ChatID(ctx)
	case "sender_id":
		return execution.SenderID(ctx)
	case "tenant_id":
		return execution.TenantID(ctx)
	case "chat_type":
		return execution.ChatType(ctx)
	case "thread_id":
		return execution.ThreadID(ctx)
	default:
		if ctx == nil {
			return ""
		}
		value, ok := ctx.Value(key).(string)
		if !ok {
			return ""
		}
		return strings.TrimSpace(value)
	}
}

// isFilteredContent 检查内容是否应该被过滤
func isFilteredContent(content string) bool {
	if content == "" {
		return false
	}

	// 检测常见的 LLM 拒绝消息模式（中英文）
	rejectionPatterns := []string{
		"作为一个人工智能语言模型",
		"作为AI语言模型",
		"作为一个AI",
		"作为一个人工智能",
		"我还没有学习",
		"我还没学习",
		"我无法回答",
		"我不能回答",
		"I'm sorry, but I cannot",
		"As an AI language model",
		"As an AI assistant",
		"I cannot answer",
		"I'm not able to answer",
		"I cannot provide",
	}

	contentLower := strings.ToLower(content)
	for _, pattern := range rejectionPatterns {
		if strings.Contains(content, pattern) || strings.Contains(contentLower, strings.ToLower(pattern)) {
			return true
		}
	}

	// 检测中间态错误消息（包含 "An unknown error occurred" 的）
	if strings.Contains(content, "An unknown error occurred") {
		return true
	}

	// 检测工具执行失败的消息（这些应该被 LLM 处理后返回更好的答案）
	if strings.Contains(content, "工具执行失败") ||
		strings.Contains(content, "Tool execution failed") ||
		(strings.Contains(content, "## ") && strings.Contains(content, "**错误**")) {
		return true
	}

	// 检测纯技术错误消息
	techErrorPatterns := []string{
		"context deadline exceeded",
		"context canceled",
		"connection refused",
		"network error",
	}

	for _, pattern := range techErrorPatterns {
		if strings.Contains(contentLower, pattern) {
			return true
		}
	}

	return false
}

// GetTools 获取所有消息工具
func (t *MessageTool) GetTools() []Tool {
	return []Tool{
		NewBaseToolWithSpec(
			"send_message",
			"Send a proactive text message to the current or specified chat. Use this for progress updates or user-visible notifications.",
			map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"content": map[string]interface{}{
						"type":        "string",
						"description": "Message content to send",
					},
					"channel": map[string]interface{}{
						"type":        "string",
						"description": "Target channel (default: current)",
					},
					"chat_id": map[string]interface{}{
						"type":        "string",
						"description": "Target chat ID (default: current)",
					},
					"account_id": map[string]interface{}{
						"type":        "string",
						"description": "Target account ID (default: current inbound account)",
					},
					"reply_to": map[string]interface{}{
						"type":        "string",
						"description": "Optional message ID to reply to",
					},
				},
				"required": []string{"content"},
			},
			tooltypes.ToolSpec{
				Concurrency: tooltypes.ConcurrencyExclusive,
				Mutation:    tooltypes.MutationSideEffect,
				Risk:        tooltypes.RiskMedium,
				Tags:        []string{"message", "channel", "notify"},
			},
			t.SendMessage,
		),
		NewBaseToolWithSpec(
			"send_file",
			"Send one image or file to the current or specified chat. Supports a local file path, a remote URL, or base64 data.",
			map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"content": map[string]interface{}{
						"type":        "string",
						"description": "Optional text to send alongside the file",
					},
					"media_type": map[string]interface{}{
						"type":        "string",
						"description": "Type of media to send: image or file. Defaults to file.",
						"enum":        []string{"image", "file"},
					},
					"file_path": map[string]interface{}{
						"type":        "string",
						"description": "Local file path to upload. Exactly one of file_path, file_url, or base64_data is required.",
					},
					"file_url": map[string]interface{}{
						"type":        "string",
						"description": "Remote file URL to send. Exactly one of file_path, file_url, or base64_data is required.",
					},
					"base64_data": map[string]interface{}{
						"type":        "string",
						"description": "Base64 encoded file content. Exactly one of file_path, file_url, or base64_data is required.",
					},
					"file_name": map[string]interface{}{
						"type":        "string",
						"description": "Optional file name override",
					},
					"mime_type": map[string]interface{}{
						"type":        "string",
						"description": "Optional MIME type override",
					},
					"channel": map[string]interface{}{
						"type":        "string",
						"description": "Target channel (default: current)",
					},
					"chat_id": map[string]interface{}{
						"type":        "string",
						"description": "Target chat ID (default: current)",
					},
					"account_id": map[string]interface{}{
						"type":        "string",
						"description": "Target account ID (default: current inbound account)",
					},
					"reply_to": map[string]interface{}{
						"type":        "string",
						"description": "Optional message ID to reply to",
					},
				},
			},
			tooltypes.ToolSpec{
				Concurrency: tooltypes.ConcurrencyExclusive,
				Mutation:    tooltypes.MutationSideEffect,
				Risk:        tooltypes.RiskMedium,
				Tags:        []string{"message", "file", "channel"},
			},
			t.SendFile,
		),
		NewBaseToolWithSpec(
			"message",
			"Legacy alias for send_message. Prefer send_message for new tool calls.",
			map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"content": map[string]interface{}{
						"type":        "string",
						"description": "Message content to send",
					},
					"channel": map[string]interface{}{
						"type":        "string",
						"description": "Target channel (default: current)",
					},
					"chat_id": map[string]interface{}{
						"type":        "string",
						"description": "Target chat ID (default: current)",
					},
					"account_id": map[string]interface{}{
						"type":        "string",
						"description": "Target account ID (default: current inbound account)",
					},
					"reply_to": map[string]interface{}{
						"type":        "string",
						"description": "Optional message ID to reply to",
					},
				},
				"required": []string{"content"},
			},
			tooltypes.ToolSpec{
				Concurrency: tooltypes.ConcurrencyExclusive,
				Mutation:    tooltypes.MutationSideEffect,
				Risk:        tooltypes.RiskMedium,
				Tags:        []string{"message", "legacy-alias"},
			},
			t.SendMessage,
		),
	}
}
