package agent

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"unicode/utf8"

	"github.com/smallnest/goclaw/internal/core/providers"
	"github.com/smallnest/goclaw/internal/core/session"
	"github.com/smallnest/goclaw/internal/logger"
	"go.uber.org/zap"
)

var errContextOverflow = errors.New("context overflow")

const (
	maxCompressionAttempts   = 3
	microCompactBudgetPct    = 85
	summaryTextRuneLimit     = 240
	summaryToolParamsPreview = 3
)

type compressionResult struct {
	DroppedMessages   int
	RemainingMessages int
}

func isContextOverflowError(err error) bool {
	if err == nil {
		return false
	}

	text := strings.ToLower(err.Error())
	patterns := []string{
		"context overflow",
		"context length",
		"context window",
		"maximum context length",
		"missing request context",
		"please provide request context",
		"please provide the request context",
		"request too large",
		"prompt is too long",
		"too many tokens",
		"input is too long",
		"token limit",
		"请提供请求的上下文",
		"缺少请求的上下文",
	}
	for _, pattern := range patterns {
		if strings.Contains(text, pattern) {
			return true
		}
	}
	return false
}

func minInt(a, b int) int {
	if a < b {
		return a
	}
	return b
}

func mergeSummary(existing, incoming string) string {
	existing = strings.TrimSpace(existing)
	incoming = strings.TrimSpace(incoming)
	switch {
	case existing == "":
		return incoming
	case incoming == "":
		return existing
	default:
		return existing + "\n\n" + incoming
	}
}

func providerMessagesForSummary(history []providers.Message) []providers.Message {
	filtered := make([]providers.Message, 0, len(history))
	for _, msg := range history {
		normalized, ok := normalizeMessageForSummary(msg)
		if !ok {
			continue
		}
		filtered = append(filtered, normalized)
	}
	return filtered
}

func normalizeMessageForSummary(msg providers.Message) (providers.Message, bool) {
	switch msg.Role {
	case string(RoleUser):
		content := normalizeSummaryText(msg.Content)
		if content == "" {
			return providers.Message{}, false
		}
		return providers.Message{Role: msg.Role, Content: content}, true
	case string(RoleAssistant):
		parts := []string{}
		if content := normalizeSummaryText(msg.Content); content != "" {
			parts = append(parts, content)
		}
		if toolDigest := summarizeAssistantToolCalls(msg.ToolCalls); toolDigest != "" {
			parts = append(parts, toolDigest)
		}
		if len(parts) == 0 {
			return providers.Message{}, false
		}
		return providers.Message{Role: msg.Role, Content: strings.Join(parts, "\n")}, true
	case string(RoleToolResult):
		digest := summarizeToolResult(msg)
		if digest == "" {
			return providers.Message{}, false
		}
		return providers.Message{Role: msg.Role, Content: digest}, true
	default:
		return providers.Message{}, false
	}
}

func summarizeAssistantToolCalls(calls []providers.ToolCall) string {
	if len(calls) == 0 {
		return ""
	}

	parts := make([]string, 0, len(calls))
	for _, tc := range calls {
		name := strings.TrimSpace(tc.Name)
		if name == "" {
			continue
		}
		paramKeys := make([]string, 0, len(tc.Params))
		for key := range tc.Params {
			if strings.TrimSpace(key) != "" {
				paramKeys = append(paramKeys, strings.TrimSpace(key))
			}
		}
		if len(paramKeys) > summaryToolParamsPreview {
			paramKeys = append(paramKeys[:summaryToolParamsPreview], "...")
		}
		if len(paramKeys) > 0 {
			parts = append(parts, fmt.Sprintf("%s(%s)", name, strings.Join(paramKeys, ", ")))
			continue
		}
		parts = append(parts, name)
	}
	if len(parts) == 0 {
		return ""
	}
	return "Tool calls: " + strings.Join(parts, "; ")
}

func summarizeToolResult(msg providers.Message) string {
	toolName := strings.TrimSpace(msg.ToolName)
	if toolName == "" {
		toolName = "tool"
	}

	content := normalizeSummaryText(msg.Content)
	if content == "" {
		return fmt.Sprintf("Tool result from %s.", toolName)
	}
	return fmt.Sprintf("Tool result from %s: %s", toolName, content)
}

func normalizeSummaryText(text string) string {
	text = strings.TrimSpace(text)
	if text == "" {
		return ""
	}

	lines := strings.Fields(strings.ReplaceAll(text, "\n", " "))
	text = strings.Join(lines, " ")
	if text == "" {
		return ""
	}
	if utf8.RuneCountInString(text) <= summaryTextRuneLimit {
		return text
	}

	runes := []rune(text)
	return strings.TrimSpace(string(runes[:summaryTextRuneLimit])) + "..."
}

func nearestUserBoundary(history []providers.Message, target int) int {
	if len(history) == 0 {
		return 0
	}
	if target < 0 {
		target = 0
	}
	if target >= len(history) {
		target = len(history) - 1
	}
	for i := target; i >= 0; i-- {
		if history[i].Role == string(RoleUser) {
			return i
		}
	}
	for i := target + 1; i < len(history); i++ {
		if history[i].Role == string(RoleUser) {
			return i
		}
	}
	return target
}

func summarizeBatch(ctx context.Context, provider providers.Provider, batch []providers.Message, existingSummary string, maxTokens int) string {
	batch = providerMessagesForSummary(batch)
	if len(batch) == 0 {
		return ""
	}

	var prompt strings.Builder
	prompt.WriteString("Summarize this conversation segment concisely. Preserve user goals, decisions, constraints, files, errors, and unresolved items.\n")
	prompt.WriteString("Do not add new facts. Keep it compact and execution-oriented.\n")
	if strings.TrimSpace(existingSummary) != "" {
		prompt.WriteString("\nExisting summary:\n")
		prompt.WriteString(strings.TrimSpace(existingSummary))
		prompt.WriteString("\n")
	}
	prompt.WriteString("\nConversation:\n")
	for _, msg := range batch {
		prompt.WriteString(msg.Role)
		prompt.WriteString(": ")
		prompt.WriteString(msg.Content)
		prompt.WriteString("\n")
	}

	summaryMaxTokens := minInt(maxTokens/4, 768)
	if summaryMaxTokens <= 0 {
		summaryMaxTokens = 512
	}

	resp, err := provider.Chat(
		ctx,
		[]providers.Message{{Role: string(RoleUser), Content: prompt.String()}},
		nil,
		providers.WithMaxTokens(summaryMaxTokens),
		providers.WithTemperature(0.2),
	)
	if err == nil && strings.TrimSpace(resp.Content) != "" {
		return strings.TrimSpace(resp.Content)
	}

	var fallback strings.Builder
	fallback.WriteString("Conversation summary:")
	for _, msg := range batch {
		content := []rune(strings.TrimSpace(msg.Content))
		if len(content) == 0 {
			continue
		}
		limit := minInt(len(content), 200)
		fallback.WriteString(" ")
		fallback.WriteString(msg.Role)
		fallback.WriteString(": ")
		fallback.WriteString(string(content[:limit]))
		if limit < len(content) {
			fallback.WriteString("...")
		}
	}
	return strings.TrimSpace(fallback.String())
}

func summarizeMessages(ctx context.Context, provider providers.Provider, batch []providers.Message, existingSummary string, maxTokens int) string {
	filtered := providerMessagesForSummary(batch)
	if len(filtered) == 0 {
		return ""
	}

	const maxBatchMessages = 12
	if len(filtered) <= maxBatchMessages {
		return summarizeBatch(ctx, provider, filtered, existingSummary, maxTokens)
	}

	mid := nearestUserBoundary(filtered, len(filtered)/2)
	if mid <= 0 || mid >= len(filtered) {
		return summarizeBatch(ctx, provider, filtered, existingSummary, maxTokens)
	}

	first := summarizeBatch(ctx, provider, filtered[:mid], existingSummary, maxTokens)
	second := summarizeBatch(ctx, provider, filtered[mid:], "", maxTokens)
	return mergeSummary(first, second)
}

func (o *Orchestrator) forceCompression(ctx context.Context, state *AgentState) (compressionResult, bool) {
	if state == nil || len(state.Messages) <= 2 {
		return compressionResult{}, false
	}
	if state.CompressionCount >= maxCompressionAttempts {
		return compressionResult{}, false
	}

	history := append([]AgentMessage(nil), state.Messages...)
	turns := parseTurnBoundariesFor(len(history), func(index int) string {
		return string(history[index].Role)
	})

	var cut int
	if len(turns) >= 2 {
		cut = turns[len(turns)/2]
	} else {
		cut = findSafeBoundaryForAgentMessages(history, len(history)/2)
	}

	var kept []AgentMessage
	if cut <= 0 {
		for i := len(history) - 1; i >= 0; i-- {
			if history[i].Role == RoleUser {
				kept = []AgentMessage{history[i]}
				break
			}
		}
	} else {
		kept = append([]AgentMessage(nil), history[cut:]...)
	}
	if len(kept) == 0 {
		return compressionResult{}, false
	}

	dropped := history[:len(history)-len(kept)]
	droppedProviders := convertToProviderMessages(dropped)
	summary := summarizeMessages(ctx, o.config.Provider, droppedProviders, state.ContextSummary, o.config.MaxTokens)
	if summary == "" {
		summary = fmt.Sprintf("[Earlier conversation was compressed after dropping %d messages due to context pressure.]", len(dropped))
		state.ContextSummary = mergeSummary(state.ContextSummary, summary)
	} else {
		state.ContextSummary = strings.TrimSpace(summary)
	}
	state.Messages = kept
	state.CompressionCount++

	logger.Warn("Forced context compression executed",
		zap.String("session_key", state.SessionKey),
		zap.Int("dropped_messages", len(dropped)),
		zap.Int("remaining_messages", len(kept)),
		zap.Int("compression_count", state.CompressionCount))

	return compressionResult{
		DroppedMessages:   len(dropped),
		RemainingMessages: len(kept),
	}, true
}

func (o *Orchestrator) maybeMicroCompact(ctx context.Context, state *AgentState, estimatedTokens int) bool {
	if o == nil || o.config == nil || state == nil {
		return false
	}
	if o.config.ContextWindow <= 0 {
		return false
	}
	if state.CompressionCount >= maxCompressionAttempts {
		return false
	}
	if estimatedTokens < o.config.ContextWindow*microCompactBudgetPct/100 {
		return false
	}
	if len(state.Messages) < 4 {
		return false
	}

	history := append([]AgentMessage(nil), state.Messages...)
	turns := parseTurnBoundariesFor(len(history), func(index int) string {
		return string(history[index].Role)
	})
	if len(turns) < 2 {
		return false
	}

	cut := turns[1]
	if cut <= 0 || cut >= len(history) {
		return false
	}

	dropped := history[:cut]
	kept := append([]AgentMessage(nil), history[cut:]...)
	summary := summarizeMessages(
		ctx,
		o.config.Provider,
		convertToProviderMessages(dropped),
		state.ContextSummary,
		o.config.MaxTokens,
	)
	if summary == "" {
		summary = fmt.Sprintf("[Earlier turn was compacted proactively after dropping %d messages due to context pressure.]", len(dropped))
		state.ContextSummary = mergeSummary(state.ContextSummary, summary)
	} else {
		state.ContextSummary = strings.TrimSpace(summary)
	}
	state.Messages = kept
	state.CompressionCount++

	logger.Info("Proactive micro compaction executed",
		zap.String("session_key", state.SessionKey),
		zap.Int("estimated_tokens", estimatedTokens),
		zap.Int("context_window", o.config.ContextWindow),
		zap.Int("dropped_messages", len(dropped)),
		zap.Int("remaining_messages", len(kept)),
		zap.Int("compression_count", state.CompressionCount))

	return true
}

func maybeCompactSession(ctx context.Context, provider providers.Provider, sess *session.Session, preserveCount int, contextWindow int, maxTokens int) bool {
	if sess == nil || provider == nil {
		return false
	}
	if preserveCount <= 0 {
		preserveCount = 40
	}

	history := sess.GetHistory(0)
	if len(history) <= preserveCount {
		return false
	}

	providerHistory := convertToProviderMessages(sessionMessagesToAgentMessages(history))
	if len(history) <= preserveCount*2 && !isOverContextBudget(contextWindow*80/100, providerHistory, nil, maxTokens) {
		return false
	}

	cut := findSafeBoundaryForSessionMessages(history, len(history)-preserveCount)
	if cut <= 0 {
		return false
	}

	dropped := history[:cut]
	kept := append([]session.Message(nil), history[cut:]...)

	droppedSummary := summarizeMessages(
		ctx,
		provider,
		convertToProviderMessages(sessionMessagesToAgentMessages(dropped)),
		sess.GetSummary(),
		maxTokens,
	)
	if droppedSummary == "" {
		return false
	}

	sess.SetSummary(droppedSummary)
	sess.ReplaceHistory(kept)

	logger.Info("Session compacted after run",
		zap.String("session_key", sess.Key),
		zap.Int("dropped_messages", len(dropped)),
		zap.Int("remaining_messages", len(kept)))

	return true
}
