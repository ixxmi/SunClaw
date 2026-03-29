package providers

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"strings"
	"time"

	anthropicsdk "github.com/anthropics/anthropic-sdk-go"
	"github.com/anthropics/anthropic-sdk-go/option"
	"github.com/smallnest/goclaw/internal/logger"
	"go.uber.org/zap"
)

// AnthropicProvider 使用 Anthropic 官方 SDK，支持自定义 BaseURL（中转站）
type AnthropicProvider struct {
	client    *anthropicsdk.Client
	model     string
	maxTokens int
	timeout   time.Duration
}

func NewAnthropicProvider(apiKey, baseURL, model string, maxTokens int) (*AnthropicProvider, error) {
	return NewAnthropicProviderWithTimeout(apiKey, baseURL, model, maxTokens, 600*time.Second)
}

func NewAnthropicProviderWithTimeout(apiKey, baseURL, model string, maxTokens int, timeout time.Duration) (*AnthropicProvider, error) {
	if apiKey == "" {
		return nil, fmt.Errorf("anthropic: API key is required")
	}
	if model == "" {
		model = "claude-opus-4-6"
	}

	base := normalizeAnthropicBaseURL(baseURL)

	opts := []option.RequestOption{
		option.WithAPIKey(apiKey),
	}
	if base != "" && base != "https://api.anthropic.com" {
		opts = append(opts, option.WithBaseURL(base))
	}

	client := anthropicsdk.NewClient(opts...)
	logger.Info("Anthropic provider initialized",
		zap.String("model", model),
		zap.String("base_url", base),
		zap.Duration("timeout", timeout))

	return &AnthropicProvider{
		client:    &client,
		model:     model,
		maxTokens: maxTokens,
		timeout:   timeout,
	}, nil
}

// normalizeAnthropicBaseURL 归一化 base URL：去掉末尾斜杠和 /v1 后缀
func normalizeAnthropicBaseURL(apiBase string) string {
	base := strings.TrimSpace(apiBase)
	if base == "" {
		return "https://api.anthropic.com"
	}
	base = strings.TrimRight(base, "/")
	if before, ok := strings.CutSuffix(base, "/v1"); ok {
		base = before
	}
	if base == "" {
		return "https://api.anthropic.com"
	}
	return base
}

// ---------- 参数构建 ----------

func buildAnthropicParams(messages []Message, tools []ToolDefinition, opts *ChatOptions, model string) anthropicsdk.MessageNewParams {
	var system []anthropicsdk.TextBlockParam
	var anthropicMsgs []anthropicsdk.MessageParam

	for _, msg := range messages {
		switch msg.Role {
		case "system":
			system = append(system, anthropicsdk.TextBlockParam{Text: msg.Content})

		case "user":
			// tool result 消息通过 user role + ToolCallID 标识
			if msg.ToolCallID != "" {
				anthropicMsgs = append(anthropicMsgs,
					anthropicsdk.NewUserMessage(
						anthropicsdk.NewToolResultBlock(msg.ToolCallID, msg.Content, false),
					),
				)
			} else {
				anthropicMsgs = append(anthropicMsgs,
					anthropicsdk.NewUserMessage(anthropicsdk.NewTextBlock(msg.Content)),
				)
			}

		case "assistant":
			if len(msg.ToolCalls) > 0 {
				var blocks []anthropicsdk.ContentBlockParamUnion
				if msg.Content != "" {
					blocks = append(blocks, anthropicsdk.NewTextBlock(msg.Content))
				}
				for _, tc := range msg.ToolCalls {
					args := tc.Params
					if args == nil {
						args = map[string]interface{}{}
					}
					blocks = append(blocks, anthropicsdk.NewToolUseBlock(tc.ID, args, tc.Name))
				}
				anthropicMsgs = append(anthropicMsgs, anthropicsdk.NewAssistantMessage(blocks...))
			} else {
				anthropicMsgs = append(anthropicMsgs,
					anthropicsdk.NewAssistantMessage(anthropicsdk.NewTextBlock(msg.Content)),
				)
			}

		case "tool":
			// tool role：工具执行结果
			anthropicMsgs = append(anthropicMsgs,
				anthropicsdk.NewUserMessage(
					anthropicsdk.NewToolResultBlock(msg.ToolCallID, msg.Content, false),
				),
			)
		}
	}

	// model 名称：API 用连字符，配置可能用点号（claude-sonnet-4.6 → claude-sonnet-4-6）
	apiModel := strings.ReplaceAll(model, ".", "-")

	maxTokens := int64(opts.MaxTokens)
	if maxTokens <= 0 {
		maxTokens = 4096
	}

	params := anthropicsdk.MessageNewParams{
		Model:     anthropicsdk.Model(apiModel),
		Messages:  anthropicMsgs,
		MaxTokens: maxTokens,
	}
	if len(system) > 0 {
		params.System = system
	}
	if opts.Temperature > 0 {
		params.Temperature = anthropicsdk.Float(float64(opts.Temperature))
	}
	if len(tools) > 0 {
		params.Tools = buildAnthropicTools(tools)
	}

	// Extended Thinking
	if strings.TrimSpace(opts.Thinking) != "" {
		level := strings.TrimSpace(opts.Thinking)
		if level != "" && level != "off" {
			applyAnthropicThinking(&params, level)
		}
	}

	return params
}

// applyAnthropicThinking 参考 picoclaw 实现：
//   - "adaptive" → adaptive thinking（Claude 4.6+）
//   - "low/medium/high/xhigh" → budget_tokens
//
// Anthropic 约束：开启 thinking 时 temperature 必须清零；budget_tokens < max_tokens。
func applyAnthropicThinking(params *anthropicsdk.MessageNewParams, level string) {
	if params.Temperature.Valid() {
		log.Printf("anthropic: temperature cleared because thinking is enabled (level=%s)", level)
	}
	params.Temperature = anthropicsdk.MessageNewParams{}.Temperature

	if level == "adaptive" {
		adaptive := anthropicsdk.NewThinkingConfigAdaptiveParam()
		params.Thinking = anthropicsdk.ThinkingConfigParamUnion{OfAdaptive: &adaptive}
		return
	}

	budgetMap := map[string]int64{
		"low":    4096,
		"medium": 16384,
		"high":   32000,
		"xhigh":  64000,
	}
	budget, ok := budgetMap[level]
	if !ok || budget <= 0 {
		return
	}
	// budget_tokens 必须 < max_tokens
	if budget >= params.MaxTokens {
		log.Printf("anthropic: budget_tokens (%d) clamped to %d (max_tokens-1)", budget, params.MaxTokens-1)
		budget = params.MaxTokens - 1
	}
	params.Thinking = anthropicsdk.ThinkingConfigParamOfEnabled(budget)
}

func buildAnthropicTools(tools []ToolDefinition) []anthropicsdk.ToolUnionParam {
	result := make([]anthropicsdk.ToolUnionParam, 0, len(tools))
	for _, t := range tools {
		tool := anthropicsdk.ToolParam{
			Name: t.Name,
			InputSchema: anthropicsdk.ToolInputSchemaParam{
				Properties: t.Parameters["properties"],
			},
		}
		if t.Description != "" {
			tool.Description = anthropicsdk.String(t.Description)
		}
		if req, ok := t.Parameters["required"].([]interface{}); ok {
			required := make([]string, 0, len(req))
			for _, r := range req {
				if s, ok := r.(string); ok {
					required = append(required, s)
				}
			}
			tool.InputSchema.Required = required
		}
		result = append(result, anthropicsdk.ToolUnionParam{OfTool: &tool})
	}
	return result
}

// ---------- 响应解析 ----------

func parseAnthropicResponse(resp *anthropicsdk.Message) *Response {
	var textParts []string
	var reasoningParts []string
	var toolCalls []ToolCall

	for _, block := range resp.Content {
		switch block.Type {
		case "thinking":
			// Extended Thinking 块：收集推理过程
			tb := block.AsThinking()
			if tb.Thinking != "" {
				reasoningParts = append(reasoningParts, tb.Thinking)
			}
		case "text":
			tb := block.AsText()
			if tb.Text != "" {
				textParts = append(textParts, tb.Text)
			}
		case "tool_use":
			tu := block.AsToolUse()
			var params map[string]interface{}
			if err := json.Unmarshal(tu.Input, &params); err != nil {
				logger.Warn("anthropic: failed to decode tool input",
					zap.String("tool", tu.Name), zap.Error(err))
				params = map[string]interface{}{"raw": string(tu.Input)}
			}
			toolCalls = append(toolCalls, ToolCall{
				ID:     tu.ID,
				Name:   tu.Name,
				Params: params,
			})
		}
	}

	finishReason := "stop"
	switch resp.StopReason {
	case anthropicsdk.StopReasonToolUse:
		finishReason = "tool_calls"
	case anthropicsdk.StopReasonMaxTokens:
		finishReason = "length"
	case anthropicsdk.StopReasonEndTurn:
		finishReason = "stop"
	}

	return &Response{
		Content:      strings.Join(textParts, ""),
		Reasoning:    strings.Join(reasoningParts, ""),
		ToolCalls:    toolCalls,
		FinishReason: finishReason,
		Usage: Usage{
			PromptTokens:     int(resp.Usage.InputTokens),
			CompletionTokens: int(resp.Usage.OutputTokens),
			TotalTokens:      int(resp.Usage.InputTokens + resp.Usage.OutputTokens),
		},
	}
}

// ---------- Provider 接口实现 ----------

func (p *AnthropicProvider) Chat(ctx context.Context, messages []Message, tools []ToolDefinition, options ...ChatOption) (*Response, error) {
	opts := &ChatOptions{Temperature: 0.7, MaxTokens: p.maxTokens}
	for _, o := range options {
		o(opts)
	}

	model := p.model
	if strings.TrimSpace(opts.Model) != "" {
		model = strings.TrimSpace(opts.Model)
	}

	params := buildAnthropicParams(messages, tools, opts, model)

	resp, err := p.client.Messages.New(ctx, params)
	if err != nil {
		return nil, fmt.Errorf("failed to generate content: %w", err)
	}

	result := parseAnthropicResponse(resp)
	logger.Debug("Anthropic response received",
		zap.Int("tool_calls", len(result.ToolCalls)),
		zap.Int("content_len", len(result.Content)),
		zap.Int("reasoning_len", len(result.Reasoning)))
	return result, nil
}

func (p *AnthropicProvider) ChatWithTools(ctx context.Context, messages []Message, tools []ToolDefinition, options ...ChatOption) (*Response, error) {
	return p.Chat(ctx, messages, tools, options...)
}

func (p *AnthropicProvider) Close() error { return nil }

// ---------- StreamingProvider 接口实现 ----------

func (p *AnthropicProvider) ChatStream(ctx context.Context, messages []Message, tools []ToolDefinition, callback StreamCallback, options ...ChatOption) error {
	opts := &ChatOptions{Temperature: 0.7, MaxTokens: p.maxTokens}
	for _, o := range options {
		o(opts)
	}

	model := p.model
	if strings.TrimSpace(opts.Model) != "" {
		model = strings.TrimSpace(opts.Model)
	}

	params := buildAnthropicParams(messages, tools, opts, model)

	stream := p.client.Messages.NewStreaming(ctx, params)
	defer stream.Close()

	type pendingTool struct {
		id    string
		name  string
		input strings.Builder
	}
	toolMap := make(map[int64]*pendingTool)

	for stream.Next() {
		event := stream.Current()

		switch event.Type {
		case "content_block_start":
			e := event.AsContentBlockStart()
			switch e.ContentBlock.Type {
			case "tool_use":
				toolMap[e.Index] = &pendingTool{
					id:   e.ContentBlock.ID,
					name: e.ContentBlock.Name,
				}
			case "thinking":
				// thinking 块开始，后续 delta 会通过 thinking_delta 推送
			}

		case "content_block_delta":
			e := event.AsContentBlockDelta()
			switch e.Delta.Type {
			case "text_delta":
				if txt := e.Delta.AsTextDelta().Text; txt != "" {
					callback(StreamChunk{Content: txt})
				}
			case "thinking_delta":
				// Extended Thinking 实时流式推送推理过程
				if thinking := e.Delta.AsThinkingDelta().Thinking; thinking != "" {
					callback(StreamChunk{Content: thinking, IsThinking: true})
				}
			case "input_json_delta":
				if pt, ok := toolMap[e.Index]; ok {
					pt.input.WriteString(e.Delta.AsInputJSONDelta().PartialJSON)
				}
			}

		case "content_block_stop":
			e := event.AsContentBlockStop()
			if pt, ok := toolMap[e.Index]; ok {
				var tcParams map[string]interface{}
				if s := pt.input.String(); s != "" {
					if err := json.Unmarshal([]byte(s), &tcParams); err != nil {
						logger.Warn("anthropic stream: failed to parse tool input JSON",
							zap.String("tool", pt.name), zap.Error(err))
						tcParams = map[string]interface{}{"raw": s}
					}
				}
				if tcParams == nil {
					tcParams = map[string]interface{}{}
				}
				callback(StreamChunk{
					ToolCall: &ToolCall{
						ID:     pt.id,
						Name:   pt.name,
						Params: tcParams,
					},
				})
				delete(toolMap, e.Index)
			}
		}
	}

	if err := stream.Err(); err != nil {
		callback(StreamChunk{Done: true, Error: err})
		return fmt.Errorf("anthropic stream error: %w", err)
	}

	callback(StreamChunk{Done: true, IsFinal: true})
	return nil
}
