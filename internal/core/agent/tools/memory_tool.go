package tools

import (
	"context"
	"fmt"
	"strings"

	"github.com/smallnest/goclaw/internal/core/memory"
	"github.com/smallnest/goclaw/internal/core/namespaces"
)

// MemoryTool memory 搜索工具
type MemoryTool struct {
	searchManager memory.MemorySearchManager
	managerPool   *memory.SearchManagerPool
	baseWorkspace string
	name          string
}

// NewMemoryTool 创建 memory 搜索工具
func NewMemoryTool(searchManager memory.MemorySearchManager) *MemoryTool {
	return &MemoryTool{
		searchManager: searchManager,
		name:          "memory_search",
	}
}

// NewNamespacedMemoryTool creates a memory search tool that resolves per-user managers on demand.
func NewNamespacedMemoryTool(baseWorkspace string, managerPool *memory.SearchManagerPool) *MemoryTool {
	return &MemoryTool{
		managerPool:   managerPool,
		baseWorkspace: strings.TrimSpace(baseWorkspace),
		name:          "memory_search",
	}
}

// Name 返回工具名称
func (t *MemoryTool) Name() string {
	return t.name
}

// Description 返回工具描述
func (t *MemoryTool) Description() string {
	return "Search semantic memory for relevant information about past conversations, facts, and context."
}

// Parameters 返回参数定义
func (t *MemoryTool) Parameters() map[string]interface{} {
	return map[string]interface{}{
		"type": "object",
		"properties": map[string]interface{}{
			"query": map[string]interface{}{
				"type":        "string",
				"description": "Search query",
			},
			"limit": map[string]interface{}{
				"type":        "integer",
				"description": "Maximum number of results",
				"default":     6,
			},
		},
		"required": []string{"query"},
	}
}

// Execute 执行工具
func (t *MemoryTool) Execute(ctx context.Context, params map[string]interface{}) (string, error) {
	query, ok := params["query"].(string)
	if !ok {
		return "", fmt.Errorf("query is required and must be a string")
	}

	limit := 6
	if l, ok := params["limit"].(float64); ok {
		limit = int(l)
	}

	opts := memory.DefaultSearchOptions()
	opts.Limit = limit

	searchMgr, err := t.resolveSearchManager(ctx)
	if err != nil {
		return "", err
	}

	results, err := searchMgr.Search(ctx, query, opts)
	if err != nil {
		return "", fmt.Errorf("memory search failed: %w", err)
	}

	return formatSearchResults(query, results), nil
}

// formatSearchResults 格式化搜索结果
func formatSearchResults(query string, results []*memory.SearchResult) string {
	if len(results) == 0 {
		return fmt.Sprintf("No results found for: %s", query)
	}

	var output string
	output += fmt.Sprintf("Found %d result(s) for: %s\n\n", len(results), query)

	for i, result := range results {
		output += fmt.Sprintf("[%d] Score: %.2f\n", i+1, result.Score)
		if result.Source != "" {
			output += fmt.Sprintf("    Source: %s\n", result.Source)
		}
		if result.Type != "" {
			output += fmt.Sprintf("    Type: %s\n", result.Type)
		}
		if result.Metadata.FilePath != "" {
			output += fmt.Sprintf("    File: %s", result.Metadata.FilePath)
			if result.Metadata.LineNumber > 0 {
				output += fmt.Sprintf(":%d", result.Metadata.LineNumber)
			}
			output += "\n"
		}

		// 文本内容
		text := result.Text
		maxLen := 300
		if len(text) > maxLen {
			text = text[:maxLen] + "..."
		}
		output += fmt.Sprintf("    Content: %s\n\n", text)
	}

	return output
}

// MemoryAddTool memory 添加工具
type MemoryAddTool struct {
	searchManager memory.MemorySearchManager
	managerPool   *memory.SearchManagerPool
	baseWorkspace string
	name          string
}

// NewMemoryAddTool 创建 memory 添加工具
func NewMemoryAddTool(searchManager memory.MemorySearchManager) *MemoryAddTool {
	return &MemoryAddTool{
		searchManager: searchManager,
		name:          "memory_add",
	}
}

// NewNamespacedMemoryAddTool creates a memory add tool with per-user manager resolution.
func NewNamespacedMemoryAddTool(baseWorkspace string, managerPool *memory.SearchManagerPool) *MemoryAddTool {
	return &MemoryAddTool{
		managerPool:   managerPool,
		baseWorkspace: strings.TrimSpace(baseWorkspace),
		name:          "memory_add",
	}
}

// Name 返回工具名称
func (t *MemoryAddTool) Name() string {
	return t.name
}

// Description 返回工具描述
func (t *MemoryAddTool) Description() string {
	return "Add information to memory for future reference. Only works with builtin backend."
}

// Parameters 返回参数定义
func (t *MemoryAddTool) Parameters() map[string]interface{} {
	return map[string]interface{}{
		"type": "object",
		"properties": map[string]interface{}{
			"text": map[string]interface{}{
				"type":        "string",
				"description": "The text content to store",
			},
			"source": map[string]interface{}{
				"type":        "string",
				"description": "Source of the memory (longterm, session, daily)",
				"default":     "session",
			},
			"type": map[string]interface{}{
				"type":        "string",
				"description": "Type of memory (fact, preference, context, conversation)",
				"default":     "fact",
			},
		},
		"required": []string{"text"},
	}
}

// Execute 执行工具
func (t *MemoryAddTool) Execute(ctx context.Context, params map[string]interface{}) (string, error) {
	text, ok := params["text"].(string)
	if !ok || text == "" {
		return "", fmt.Errorf("text is required and must be a non-empty string")
	}

	sourceStr := "session"
	if s, ok := params["source"].(string); ok {
		sourceStr = s
	}

	typeStr := "fact"
	if typ, ok := params["type"].(string); ok {
		typeStr = typ
	}

	source := memory.MemorySource(sourceStr)
	memType := memory.MemoryType(typeStr)

	metadata := memory.MemoryMetadata{}

	searchMgr, err := t.resolveSearchManager(ctx)
	if err != nil {
		return "", err
	}

	if err := searchMgr.Add(ctx, text, source, memType, metadata); err != nil {
		return "", fmt.Errorf("failed to add memory: %w", err)
	}

	return "Memory added successfully", nil
}

func (t *MemoryTool) resolveSearchManager(ctx context.Context) (memory.MemorySearchManager, error) {
	if t.searchManager != nil {
		return t.searchManager, nil
	}
	if t.managerPool == nil {
		return nil, fmt.Errorf("memory search manager is unavailable")
	}
	return t.managerPool.Get(resolveMemoryWorkspace(ctx, t.baseWorkspace))
}

func (t *MemoryAddTool) resolveSearchManager(ctx context.Context) (memory.MemorySearchManager, error) {
	if t.searchManager != nil {
		return t.searchManager, nil
	}
	if t.managerPool == nil {
		return nil, fmt.Errorf("memory search manager is unavailable")
	}
	return t.managerPool.Get(resolveMemoryWorkspace(ctx, t.baseWorkspace))
}

func resolveMemoryWorkspace(ctx context.Context, baseWorkspace string) string {
	if ctx != nil {
		if raw, ok := ctx.Value("workspace_root").(string); ok && strings.TrimSpace(raw) != "" {
			return strings.TrimSpace(raw)
		}
		if raw, ok := ctx.Value("session_key").(string); ok {
			if identity, found := namespaces.FromSessionKey(strings.TrimSpace(raw)); found {
				if workspaceRoot := identity.WorkspaceDir(baseWorkspace); strings.TrimSpace(workspaceRoot) != "" {
					return workspaceRoot
				}
			}
		}
	}
	return strings.TrimSpace(baseWorkspace)
}
