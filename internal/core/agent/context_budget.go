package agent

import (
	"encoding/json"
	"strings"
	"unicode/utf8"

	"github.com/smallnest/goclaw/internal/core/providers"
	"github.com/smallnest/goclaw/internal/core/session"
)

type roleAtFunc func(index int) string

func parseTurnBoundariesFor(length int, roleAt roleAtFunc) []int {
	starts := make([]int, 0, length/2)
	for i := 0; i < length; i++ {
		if roleAt(i) == string(RoleUser) {
			starts = append(starts, i)
		}
	}
	return starts
}

func findSafeBoundaryFor(length int, roleAt roleAtFunc, targetIndex int) int {
	if length == 0 {
		return 0
	}
	if targetIndex <= 0 {
		return 0
	}
	if targetIndex >= length {
		return length
	}

	turns := parseTurnBoundariesFor(length, roleAt)
	if len(turns) == 0 {
		return targetIndex
	}

	backward := -1
	for _, turn := range turns {
		if turn <= targetIndex {
			backward = turn
		}
	}
	if backward > 0 {
		return backward
	}

	for _, turn := range turns {
		if turn > targetIndex {
			return turn
		}
	}

	return 0
}

func parseTurnBoundaries(history []providers.Message) []int {
	return parseTurnBoundariesFor(len(history), func(index int) string {
		return history[index].Role
	})
}

func findSafeBoundary(history []providers.Message, targetIndex int) int {
	return findSafeBoundaryFor(len(history), func(index int) string {
		return history[index].Role
	}, targetIndex)
}

func findSafeBoundaryForAgentMessages(history []AgentMessage, targetIndex int) int {
	return findSafeBoundaryFor(len(history), func(index int) string {
		return string(history[index].Role)
	}, targetIndex)
}

func findSafeBoundaryForSessionMessages(history []session.Message, targetIndex int) int {
	return findSafeBoundaryFor(len(history), func(index int) string {
		return history[index].Role
	}, targetIndex)
}

// estimateMessageTokens estimates message tokens with a lightweight heuristic.
// It intentionally over-counts a bit so compression triggers earlier.
func estimateMessageTokens(msg providers.Message) int {
	chars := utf8.RuneCountInString(msg.Content)
	chars += utf8.RuneCountInString(msg.ReasoningContent)

	for _, img := range msg.Images {
		chars += utf8.RuneCountInString(img)
	}

	for _, tc := range msg.ToolCalls {
		chars += utf8.RuneCountInString(tc.ID)
		chars += utf8.RuneCountInString(tc.Name)
		if len(tc.Params) > 0 {
			if data, err := json.Marshal(tc.Params); err == nil {
				chars += len(data)
			}
		}
		chars += utf8.RuneCountInString(tc.Response)
	}

	chars += utf8.RuneCountInString(msg.ToolCallID)
	chars += utf8.RuneCountInString(msg.ToolName)

	const messageOverhead = 12
	chars += messageOverhead

	return chars * 2 / 5
}

func estimateToolDefsTokens(defs []providers.ToolDefinition) int {
	if len(defs) == 0 {
		return 0
	}

	totalChars := 0
	for _, def := range defs {
		totalChars += len(def.Name) + len(def.Description) + 20
		if def.Parameters != nil {
			if data, err := json.Marshal(def.Parameters); err == nil {
				totalChars += len(data)
			}
		}
	}

	return totalChars * 2 / 5
}

func isOverContextBudget(contextWindow int, messages []providers.Message, toolDefs []providers.ToolDefinition, maxTokens int) bool {
	if contextWindow <= 0 {
		return false
	}

	return estimateContextUsageTokens(messages, toolDefs, maxTokens) > contextWindow
}

func estimateContextUsageTokens(messages []providers.Message, toolDefs []providers.ToolDefinition, maxTokens int) int {
	total := 0
	for _, msg := range messages {
		total += estimateMessageTokens(msg)
	}
	total += estimateToolDefsTokens(toolDefs)
	total += maxTokens
	return total
}

func guessContextWindow(model string) int {
	model = strings.ToLower(strings.TrimSpace(model))
	switch {
	case model == "":
		return 128000
	case strings.Contains(model, "gpt-5"), strings.Contains(model, "gpt-4.1"):
		return 400000
	case strings.Contains(model, "claude"):
		return 200000
	case strings.Contains(model, "gemini"):
		return 1000000
	case strings.Contains(model, "qwen"), strings.Contains(model, "deepseek"), strings.Contains(model, "minimax"):
		return 128000
	default:
		return 128000
	}
}

// GuessContextWindowForModel exposes the heuristic context-window resolver for callers
// that instantiate agents outside the core package.
func GuessContextWindowForModel(model string) int {
	return guessContextWindow(model)
}
