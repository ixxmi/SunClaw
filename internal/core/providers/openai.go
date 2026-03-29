package providers

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/smallnest/goclaw/internal/logger"
	"go.uber.org/zap"
)

const (
	defaultOpenAITimeout         = 600 * time.Second
	defaultOpenAIMaxAttempts     = 2
	defaultOpenAIRetryDelay      = 750 * time.Millisecond
	defaultOpenAIRetryMaxElapsed = 15 * time.Second
)

// OpenAIProvider 使用原生 HTTP 调用 OpenAI 兼容接口，支持 Gemini / codex 等中转站
type OpenAIProvider struct {
	apiKey         string
	baseURL        string
	model          string
	maxTokens      int
	timeout        time.Duration
	maxTokensField string // 可配置的 max tokens 字段名，空则自动检测
	httpClient     *http.Client
}

// OpenAIOption 构造选项
type OpenAIOption func(*OpenAIProvider)

// WithOpenAIMaxTokensField 指定 max tokens 字段名（如 "max_completion_tokens"）
func WithOpenAIMaxTokensField(field string) OpenAIOption {
	return func(p *OpenAIProvider) {
		p.maxTokensField = field
	}
}

// WithOpenAIProxy 设置代理
func WithOpenAIProxy(proxyURL string) OpenAIOption {
	return func(p *OpenAIProvider) {
		if proxyURL == "" {
			return
		}
		parsed, err := url.Parse(proxyURL)
		if err != nil {
			logger.Warn("openai: invalid proxy URL", zap.String("proxy", proxyURL), zap.Error(err))
			return
		}
		p.httpClient.Transport = &http.Transport{Proxy: http.ProxyURL(parsed)}
	}
}

// WithOpenAITimeout 覆盖超时时间
func WithOpenAITimeout(d time.Duration) OpenAIOption {
	return func(p *OpenAIProvider) {
		if d > 0 {
			p.timeout = d
			p.httpClient.Timeout = d
		}
	}
}

func NewOpenAIProvider(apiKey, baseURL, model string, maxTokens int) (*OpenAIProvider, error) {
	return NewOpenAIProviderWithOptions(apiKey, baseURL, model, maxTokens, defaultOpenAITimeout)
}

func NewOpenAIProviderWithTimeout(apiKey, baseURL, model string, maxTokens int, timeout time.Duration) (*OpenAIProvider, error) {
	return NewOpenAIProviderWithOptions(apiKey, baseURL, model, maxTokens, timeout)
}

func NewOpenAIProviderWithOptions(apiKey, baseURL, model string, maxTokens int, timeout time.Duration, opts ...OpenAIOption) (*OpenAIProvider, error) {
	if apiKey == "" {
		return nil, fmt.Errorf("openai: API key is required")
	}
	if model == "" {
		model = "gpt-4"
	}
	if timeout <= 0 {
		timeout = defaultOpenAITimeout
	}

	base := strings.TrimRight(strings.TrimSpace(baseURL), "/")

	p := &OpenAIProvider{
		apiKey:     apiKey,
		baseURL:    base,
		model:      model,
		maxTokens:  maxTokens,
		timeout:    timeout,
		httpClient: &http.Client{Timeout: timeout},
	}

	for _, opt := range opts {
		if opt != nil {
			opt(p)
		}
	}

	logger.Info("OpenAI provider initialized",
		zap.String("model", model),
		zap.String("base_url", base),
		zap.Duration("timeout", timeout))

	return p, nil
}

// ---------- 请求序列化（参考 picoclaw openai_compat） ----------

type openaiWireMessage struct {
	Role             string      `json:"role"`
	Content          interface{} `json:"content"`
	ReasoningContent string      `json:"reasoning_content,omitempty"`
	ToolCalls        interface{} `json:"tool_calls,omitempty"`
	ToolCallID       string      `json:"tool_call_id,omitempty"`
}

type openaiToolCall struct {
	ID       string `json:"id"`
	Type     string `json:"type"`
	Function struct {
		Name      string `json:"name"`
		Arguments string `json:"arguments"`
	} `json:"function"`
}

// serializeOpenAIMessages 转换内部 Message 为 OpenAI wire 格式
// - assistant 带 tool_calls 序列化为标准 OpenAI 格式
// - 支持 ReasoningContent（deepseek/qwen 等）
// - 支持 Images（multipart content）
func serializeOpenAIMessages(messages []Message) []interface{} {
	out := make([]interface{}, 0, len(messages))
	for _, m := range messages {
		// 有图片：multipart content 格式
		if len(m.Images) > 0 {
			parts := make([]map[string]interface{}, 0, 1+len(m.Images))
			if m.Content != "" {
				parts = append(parts, map[string]interface{}{
					"type": "text",
					"text": m.Content,
				})
			}
			for _, imgURL := range m.Images {
				if strings.HasPrefix(imgURL, "data:image/") || strings.HasPrefix(imgURL, "http") {
					parts = append(parts, map[string]interface{}{
						"type":      "image_url",
						"image_url": map[string]interface{}{"url": imgURL},
					})
				}
			}
			msg := map[string]interface{}{
				"role":    m.Role,
				"content": parts,
			}
			if m.ToolCallID != "" {
				msg["tool_call_id"] = m.ToolCallID
			}
			out = append(out, msg)
			continue
		}

		switch m.Role {
		case "assistant":
			if len(m.ToolCalls) > 0 {
				wireCalls := make([]openaiToolCall, 0, len(m.ToolCalls))
				for _, tc := range m.ToolCalls {
					args, _ := json.Marshal(tc.Params)
					wireCalls = append(wireCalls, openaiToolCall{
						ID:   tc.ID,
						Type: "function",
						Function: struct {
							Name      string `json:"name"`
							Arguments string `json:"arguments"`
						}{Name: tc.Name, Arguments: string(args)},
					})
				}
				out = append(out, map[string]interface{}{
					"role":       "assistant",
					"content":    m.Content,
					"tool_calls": wireCalls,
				})
				continue
			}
			out = append(out, openaiWireMessage{
				Role:             m.Role,
				Content:          m.Content,
				ReasoningContent: m.ReasoningContent,
			})

		case "tool":
			out = append(out, openaiWireMessage{
				Role:       "tool",
				Content:    m.Content,
				ToolCallID: m.ToolCallID,
			})

		default:
			out = append(out, openaiWireMessage{Role: m.Role, Content: m.Content})
		}
	}
	return out
}

// maxTokensFieldName 根据配置或模型名自动选择字段名
func (p *OpenAIProvider) maxTokensFieldName(model string) string {
	if p.maxTokensField != "" {
		return p.maxTokensField
	}
	lower := strings.ToLower(model)
	if strings.Contains(lower, "o1") || strings.Contains(lower, "gpt-5") ||
		strings.Contains(lower, "glm") {
		return "max_completion_tokens"
	}
	return "max_tokens"
}

func (p *OpenAIProvider) buildRequest(model string, messages []Message, tools []ToolDefinition, opts *ChatOptions) map[string]interface{} {
	body := map[string]interface{}{
		"model":    normalizeOpenAIModel(model, p.baseURL),
		"messages": serializeOpenAIMessages(messages),
		"stream":   false,
	}

	maxTok := opts.MaxTokens
	if maxTok <= 0 {
		maxTok = p.maxTokens
	}
	if maxTok <= 0 {
		maxTok = 4096
	}
	body[p.maxTokensFieldName(model)] = maxTok

	if opts.Temperature > 0 {
		lower := strings.ToLower(model)
		// kimi-k2 只支持 temperature=1
		if strings.Contains(lower, "kimi") && strings.Contains(lower, "k2") {
			body["temperature"] = 1.0
		} else {
			body["temperature"] = opts.Temperature
		}
	}

	if len(tools) > 0 {
		openaiTools := make([]map[string]interface{}, 0, len(tools))
		for _, t := range tools {
			openaiTools = append(openaiTools, map[string]interface{}{
				"type": "function",
				"function": map[string]interface{}{
					"name":        t.Name,
					"description": t.Description,
					"parameters":  t.Parameters,
				},
			})
		}
		body["tools"] = openaiTools
		body["tool_choice"] = "auto"
	}

	return body
}

// normalizeOpenAIModel 剥离 provider 前缀（如 "google/gemini-2.5-pro" → "gemini-2.5-pro"）
// 参考 picoclaw normalizeModel
func normalizeOpenAIModel(model, apiBase string) string {
	before, after, ok := strings.Cut(model, "/")
	if !ok {
		return model
	}
	if strings.Contains(strings.ToLower(apiBase), "openrouter.ai") {
		return model
	}
	switch strings.ToLower(before) {
	case "litellm", "moonshot", "nvidia", "groq", "ollama", "deepseek",
		"google", "openrouter", "zhipu", "mistral", "vivgrid":
		return after
	default:
		return model
	}
}

// ---------- HTTP 请求 ----------

func (p *OpenAIProvider) completionsURL() string {
	return p.baseURL + "/chat/completions"
}

func (p *OpenAIProvider) doRequest(ctx context.Context, body map[string]interface{}) (*Response, error) {
	jsonData, err := json.Marshal(body)
	if err != nil {
		return nil, fmt.Errorf("openai: marshal request: %w", err)
	}

	var lastErr error
	for attempt := 1; attempt <= defaultOpenAIMaxAttempts; attempt++ {
		attemptStarted := time.Now()
		resp, err := p.doRequestOnce(ctx, jsonData, body, attempt)
		if err == nil {
			return resp, nil
		}

		lastErr = err
		if attempt == defaultOpenAIMaxAttempts || !shouldRetryOpenAIRequest(err, time.Since(attemptStarted)) {
			return nil, err
		}

		delay := time.Duration(attempt) * defaultOpenAIRetryDelay
		logger.Warn("Retrying OpenAI request after transient failure",
			zap.String("url", p.completionsURL()),
			zap.String("base_url", p.baseURL),
			zap.Any("model", body["model"]),
			zap.Int("attempt", attempt),
			zap.Int("max_attempts", defaultOpenAIMaxAttempts),
			zap.Duration("retry_delay", delay),
			zap.Error(err))

		timer := time.NewTimer(delay)
		select {
		case <-ctx.Done():
			timer.Stop()
			return nil, ctx.Err()
		case <-timer.C:
		}
	}

	return nil, lastErr
}

type openAIHTTPStatusError struct {
	statusCode  int
	statusText  string
	bodyPreview string
	retryable   bool
}

func (e *openAIHTTPStatusError) Error() string {
	if e == nil {
		return ""
	}
	if e.statusText != "" {
		return fmt.Sprintf("failed to generate content: HTTP %d (%s): %s", e.statusCode, e.statusText, e.bodyPreview)
	}
	return fmt.Sprintf("failed to generate content: HTTP %d: %s", e.statusCode, e.bodyPreview)
}

func shouldRetryOpenAIRequest(err error, elapsed time.Duration) bool {
	if err == nil || elapsed > defaultOpenAIRetryMaxElapsed {
		return false
	}

	var httpErr *openAIHTTPStatusError
	if errors.As(err, &httpErr) {
		return httpErr.retryable
	}

	return isRetryableOpenAITransportError(err)
}

func isRetryableOpenAITransportError(err error) bool {
	if err == nil {
		return false
	}
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return false
	}

	var netErr net.Error
	return errors.As(err, &netErr)
}

func isRetryableOpenAIStatus(statusCode int) bool {
	switch statusCode {
	case http.StatusRequestTimeout,
		http.StatusConflict,
		http.StatusTooEarly,
		http.StatusInternalServerError,
		http.StatusBadGateway,
		http.StatusServiceUnavailable:
		return true
	default:
		return false
	}
}

func (p *OpenAIProvider) doRequestOnce(ctx context.Context, jsonData []byte, body map[string]interface{}, attempt int) (*Response, error) {
	startedAt := time.Now()

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, p.completionsURL(), bytes.NewReader(jsonData))
	if err != nil {
		return nil, fmt.Errorf("openai: new request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+p.apiKey)

	logger.Debug("OpenAI request",
		zap.String("url", p.completionsURL()),
		zap.Any("model", body["model"]),
		zap.Int("attempt", attempt),
		zap.Int("request_bytes", len(jsonData)),
		zap.String("request_preview", openaiResponsePreview(jsonData, 1024)))

	resp, err := p.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("openai: http request: %w", err)
	}
	defer resp.Body.Close()

	contentType := resp.Header.Get("Content-Type")
	elapsed := time.Since(startedAt)

	if resp.StatusCode != http.StatusOK {
		bodyBytes, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		logger.Error("OpenAI HTTP request failed",
			zap.String("url", p.completionsURL()),
			zap.String("base_url", p.baseURL),
			zap.Any("model", body["model"]),
			zap.Int("attempt", attempt),
			zap.Duration("elapsed", elapsed),
			zap.Int("status_code", resp.StatusCode),
			zap.String("content_type", contentType),
			zap.Any("response_headers", openAIHeaderMap(resp.Header)),
			zap.Int("request_bytes", len(jsonData)),
			zap.String("request_preview", openaiResponsePreview(jsonData, 1024)),
			zap.String("response_preview", openaiResponsePreview(bodyBytes, 512)))
		if openaiLooksLikeHTML(bodyBytes, contentType) {
			return nil, fmt.Errorf(
				"openai: %s returned HTML instead of JSON (check base_url). status=%d body=%s",
				p.baseURL, resp.StatusCode, openaiResponsePreview(bodyBytes, 128))
		}
		return nil, &openAIHTTPStatusError{
			statusCode:  resp.StatusCode,
			statusText:  http.StatusText(resp.StatusCode),
			bodyPreview: openaiResponsePreview(bodyBytes, 256),
			retryable:   isRetryableOpenAIStatus(resp.StatusCode),
		}
	}

	// 服务端返回 SSE（忽略了 stream:false）→ 走 SSE 累积解析
	if strings.Contains(contentType, "text/event-stream") {
		return parseOpenAISSEResponse(resp.Body)
	}

	reader := bufio.NewReader(resp.Body)
	prefix, peekErr := reader.Peek(256)
	if peekErr != nil && peekErr != io.EOF && peekErr != bufio.ErrBufferFull {
		return nil, fmt.Errorf("openai: peek response: %w", peekErr)
	}
	if openaiLooksLikeHTML(prefix, contentType) {
		logger.Error("OpenAI HTTP response returned HTML on success status",
			zap.String("url", p.completionsURL()),
			zap.String("base_url", p.baseURL),
			zap.Any("model", body["model"]),
			zap.Int("status_code", resp.StatusCode),
			zap.String("content_type", contentType),
			zap.Any("response_headers", openAIHeaderMap(resp.Header)),
			zap.Int("request_bytes", len(jsonData)),
			zap.String("request_preview", openaiResponsePreview(jsonData, 1024)),
			zap.String("response_preview", openaiResponsePreview(prefix, 512)))
		return nil, fmt.Errorf(
			"openai: %s returned HTML instead of JSON (check base_url). body=%s",
			p.baseURL, openaiResponsePreview(prefix, 128))
	}
	// 前缀是 SSE 格式（data: 开头）→ 走 SSE 解析
	if bytes.HasPrefix(bytes.TrimSpace(prefix), []byte("data:")) {
		return parseOpenAISSEResponse(reader)
	}

	return parseOpenAIResponse(reader)
}

// ---------- 响应解析 ----------

// parseOpenAIResponse 解析标准 JSON 响应，支持 reasoning_content / reasoning
func parseOpenAIResponse(body io.Reader) (*Response, error) {
	var apiResp struct {
		Choices []struct {
			Message struct {
				Content          string `json:"content"`
				ReasoningContent string `json:"reasoning_content"`
				Reasoning        string `json:"reasoning"`
				ToolCalls        []struct {
					ID       string `json:"id"`
					Type     string `json:"type"`
					Function *struct {
						Name      string `json:"name"`
						Arguments string `json:"arguments"`
					} `json:"function"`
				} `json:"tool_calls"`
			} `json:"message"`
			FinishReason string `json:"finish_reason"`
		} `json:"choices"`
		Usage *struct {
			PromptTokens     int `json:"prompt_tokens"`
			CompletionTokens int `json:"completion_tokens"`
			TotalTokens      int `json:"total_tokens"`
		} `json:"usage"`
	}

	if err := json.NewDecoder(body).Decode(&apiResp); err != nil {
		return nil, fmt.Errorf("failed to generate content: decode response: %w", err)
	}
	if len(apiResp.Choices) == 0 {
		return &Response{Content: "", FinishReason: "stop"}, nil
	}

	choice := apiResp.Choices[0]
	if len(choice.Message.ToolCalls) > 0 {
		logger.Debug("OpenAI tool calls received", zap.Int("count", len(choice.Message.ToolCalls)))
	}

	var toolCalls []ToolCall
	for _, tc := range choice.Message.ToolCalls {
		var params map[string]interface{}
		if tc.Function != nil && tc.Function.Arguments != "" {
			if err := json.Unmarshal([]byte(tc.Function.Arguments), &params); err != nil {
				logger.Error("Failed to unmarshal tool arguments",
					zap.String("tool", tc.Function.Name),
					zap.String("id", tc.ID),
					zap.Error(err))
				params = map[string]interface{}{
					"__raw_arguments__": tc.Function.Arguments,
				}
			}
		}
		name := ""
		if tc.Function != nil {
			name = tc.Function.Name
		}
		toolCalls = append(toolCalls, ToolCall{ID: tc.ID, Name: name, Params: params})
	}

	// 合并 reasoning：reasoning_content 优先，其次 reasoning 字段
	reasoning := choice.Message.ReasoningContent
	if reasoning == "" {
		reasoning = choice.Message.Reasoning
	}

	result := &Response{
		Content:      choice.Message.Content,
		Reasoning:    reasoning,
		ToolCalls:    toolCalls,
		FinishReason: choice.FinishReason,
	}
	if apiResp.Usage != nil {
		result.Usage = Usage{
			PromptTokens:     apiResp.Usage.PromptTokens,
			CompletionTokens: apiResp.Usage.CompletionTokens,
			TotalTokens:      apiResp.Usage.TotalTokens,
		}
	}
	return result, nil
}

// parseOpenAISSEResponse 将 SSE 流（data: {...}\n\n）累积成一个完整 Response
func parseOpenAISSEResponse(body io.Reader) (*Response, error) {
	scanner := bufio.NewScanner(body)
	scanner.Buffer(make([]byte, 1024*1024), 1024*1024)

	var contentBuf strings.Builder
	var reasoningBuf strings.Builder
	toolCallMap := make(map[int]*ToolCall)
	toolArgsBuf := make(map[int]*strings.Builder)
	finishReason := "stop"

	type sseChunk struct {
		Choices []struct {
			Delta struct {
				Content          string `json:"content"`
				ReasoningContent string `json:"reasoning_content"`
				Reasoning        string `json:"reasoning"`
				ToolCalls        []struct {
					Index    int    `json:"index"`
					ID       string `json:"id"`
					Function *struct {
						Name      string `json:"name"`
						Arguments string `json:"arguments"`
					} `json:"function"`
				} `json:"tool_calls"`
			} `json:"delta"`
			FinishReason string `json:"finish_reason"`
		} `json:"choices"`
	}

	for scanner.Scan() {
		line := scanner.Text()
		if !strings.HasPrefix(line, "data:") {
			continue
		}
		data := strings.TrimSpace(strings.TrimPrefix(line, "data:"))
		if data == "[DONE]" || data == "" {
			break
		}
		var chunk sseChunk
		if err := json.Unmarshal([]byte(data), &chunk); err != nil {
			continue
		}
		for _, choice := range chunk.Choices {
			if choice.FinishReason != "" {
				finishReason = choice.FinishReason
			}
			contentBuf.WriteString(choice.Delta.Content)
			rc := choice.Delta.ReasoningContent
			if rc == "" {
				rc = choice.Delta.Reasoning
			}
			reasoningBuf.WriteString(rc)
			for _, tc := range choice.Delta.ToolCalls {
				if _, ok := toolCallMap[tc.Index]; !ok {
					toolCallMap[tc.Index] = &ToolCall{ID: tc.ID}
					toolArgsBuf[tc.Index] = &strings.Builder{}
				}
				if tc.Function != nil {
					if tc.Function.Name != "" {
						toolCallMap[tc.Index].Name = tc.Function.Name
					}
					toolArgsBuf[tc.Index].WriteString(tc.Function.Arguments)
				}
			}
		}
	}
	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("openai: read SSE stream: %w", err)
	}

	toolCalls := make([]ToolCall, 0, len(toolCallMap))
	for i := 0; i < len(toolCallMap); i++ {
		tc := toolCallMap[i]
		if tc == nil {
			continue
		}
		var params map[string]interface{}
		if argStr := toolArgsBuf[i].String(); argStr != "" {
			_ = json.Unmarshal([]byte(argStr), &params)
		}
		tc.Params = params
		toolCalls = append(toolCalls, *tc)
	}

	return &Response{
		Content:      contentBuf.String(),
		Reasoning:    reasoningBuf.String(),
		ToolCalls:    toolCalls,
		FinishReason: finishReason,
	}, nil
}

// ---------- HTML 检测 ----------

func openaiLooksLikeHTML(body []byte, contentType string) bool {
	ct := strings.ToLower(strings.TrimSpace(contentType))
	if strings.Contains(ct, "text/html") || strings.Contains(ct, "application/xhtml") {
		return true
	}
	i := 0
	for i < len(body) && (body[i] == ' ' || body[i] == '\t' || body[i] == '\n' || body[i] == '\r') {
		i++
	}
	if i >= len(body) {
		return false
	}
	lower := strings.ToLower(string(body[i:min(i+32, len(body))]))
	return strings.HasPrefix(lower, "<!doctype html") ||
		strings.HasPrefix(lower, "<html") ||
		strings.HasPrefix(lower, "<head") ||
		strings.HasPrefix(lower, "<body")
}

func openaiResponsePreview(body []byte, maxLen int) string {
	trimmed := bytes.TrimSpace(body)
	if len(trimmed) == 0 {
		return "<empty>"
	}
	if len(trimmed) <= maxLen {
		return string(trimmed)
	}
	return string(trimmed[:maxLen]) + "..."
}

func openAIHeaderMap(header http.Header) map[string]string {
	if len(header) == 0 {
		return nil
	}

	out := make(map[string]string, len(header))
	for k, values := range header {
		out[k] = strings.Join(values, ", ")
	}
	return out
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}

// ---------- Provider 接口实现 ----------

func (p *OpenAIProvider) Chat(ctx context.Context, messages []Message, tools []ToolDefinition, options ...ChatOption) (*Response, error) {
	opts := &ChatOptions{Temperature: 0.7, MaxTokens: p.maxTokens}
	for _, o := range options {
		o(opts)
	}
	model := p.model
	if strings.TrimSpace(opts.Model) != "" {
		model = strings.TrimSpace(opts.Model)
	}
	return p.doRequest(ctx, p.buildRequest(model, messages, tools, opts))
}

func (p *OpenAIProvider) ChatWithTools(ctx context.Context, messages []Message, tools []ToolDefinition, options ...ChatOption) (*Response, error) {
	return p.Chat(ctx, messages, tools, options...)
}

func (p *OpenAIProvider) Close() error { return nil }

// NewOpenAIProviderFromLangChain 兼容旧调用
func NewOpenAIProviderFromLangChain(apiKey, baseURL, model string, maxTokens int) (Provider, error) {
	return NewOpenAIProvider(apiKey, baseURL, model, maxTokens)
}
