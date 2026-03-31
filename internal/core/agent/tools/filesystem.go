package tools

import (
	"bufio"
	"bytes"
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/smallnest/goclaw/internal/core/namespaces"
	"github.com/smallnest/goclaw/internal/workspace"
)

const (
	readFileInlineMaxBytes      = 24 * 1024
	readFileInlineMaxLines      = 120
	readFileRangeDefaultLines   = 160
	readFileRangeMaxLines       = 200
	readFilePreviewHeadLines    = 80
	readFilePreviewTailLines    = 20
	readFileSummaryMaxLineRunes = 220
	readFileSummaryHeadLines    = 80
	readFileSummaryTailLines    = 20
	readFileBinaryProbeBytes    = 4096
	readFileControlByteFraction = 0.10
)

// FileSystemTool 文件系统工具
type FileSystemTool struct {
	allowedPaths        []string
	deniedPaths         []string
	workspace           string // 默认工作区路径
	configDirResolverFn func(agentID string) string
}

// NewFileSystemTool 创建文件系统工具
func NewFileSystemTool(allowedPaths, deniedPaths []string, workspace string) *FileSystemTool {
	return &FileSystemTool{
		allowedPaths: allowedPaths,
		deniedPaths:  deniedPaths,
		workspace:    workspace,
	}
}

// SetConfigDirResolver 设置认知配置目录解析器。
// 参数是 bootstrap owner，即主 agent 的 ID。
func (t *FileSystemTool) SetConfigDirResolver(resolver func(ownerID string) string) {
	t.configDirResolverFn = resolver
}

// ReadFile 读取文件
func (t *FileSystemTool) ReadFile(ctx context.Context, params map[string]interface{}) (string, error) {
	path, ok := params["path"].(string)
	if !ok {
		return "", fmt.Errorf("path parameter is required")
	}

	// 检查路径权限
	if !t.isAllowed(path) {
		return "", fmt.Errorf("access to path %s is not allowed", path)
	}

	startLine, hasStart, err := optionalPositiveInt(params, "start_line")
	if err != nil {
		return "", err
	}
	endLine, hasEnd, err := optionalPositiveInt(params, "end_line")
	if err != nil {
		return "", err
	}
	if hasStart || hasEnd {
		requestedStartLine := startLine
		requestedEndLine := endLine
		startLine, endLine, rangeCapped := normalizeReadFileLineRange(startLine, endLine)
		return readFileLineRange(path, startLine, endLine, requestedStartLine, requestedEndLine, rangeCapped)
	}

	info, err := os.Stat(path)
	if err != nil {
		return "", err
	}
	if info.IsDir() {
		return "", fmt.Errorf("%s is a directory", path)
	}

	probe, err := readFilePrefix(path, readFileBinaryProbeBytes)
	if err != nil {
		return "", err
	}
	if looksLikeBinary(probe) {
		return formatBinaryFileNotice(path, info.Size()), nil
	}

	if info.Size() <= readFileInlineMaxBytes {
		content, err := os.ReadFile(path)
		if err != nil {
			return "", err
		}
		if looksLikeBinary(content) {
			return formatBinaryFileNotice(path, info.Size()), nil
		}
		return renderSmallTextFile(path, info.Size(), string(content)), nil
	}

	return buildLargeFilePreview(path, info.Size())
}

// WriteFile 写入文件
func (t *FileSystemTool) WriteFile(ctx context.Context, params map[string]interface{}) (string, error) {
	path, ok := params["path"].(string)
	if !ok {
		return "", fmt.Errorf("path parameter is required")
	}

	content, ok := params["content"].(string)
	if !ok {
		return "", fmt.Errorf("content parameter is required")
	}

	// 检查路径权限
	if !t.isAllowed(path) {
		return "", fmt.Errorf("access to path %s is not allowed", path)
	}

	// 确保目录存在
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return "", err
	}

	// 写入文件
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		return "", err
	}

	return fmt.Sprintf("Successfully wrote %d bytes to %s", len(content), path), nil
}

// EditFile 编辑文件（精确字符串替换）
func (t *FileSystemTool) EditFile(ctx context.Context, params map[string]interface{}) (string, error) {
	path, ok := params["path"].(string)
	if !ok {
		return "", fmt.Errorf("path parameter is required")
	}

	oldStr, ok := params["old_string"].(string)
	if !ok {
		return "", fmt.Errorf("old_string parameter is required")
	}

	newStr, ok := params["new_string"].(string)
	if !ok {
		return "", fmt.Errorf("new_string parameter is required")
	}

	// 检查路径权限
	if !t.isAllowed(path) {
		return "", fmt.Errorf("access to path %s is not allowed", path)
	}

	// 读取文件内容
	content, err := os.ReadFile(path)
	if err != nil {
		return "", fmt.Errorf("failed to read file: %w", err)
	}

	fileContent := string(content)

	// 检查旧字符串是否存在
	if !strings.Contains(fileContent, oldStr) {
		return "", fmt.Errorf("old_string not found in file. Please verify the exact text to replace.")
	}

	// 计算替换次数
	occurrences := strings.Count(fileContent, oldStr)

	// 执行替换
	newContent := strings.ReplaceAll(fileContent, oldStr, newStr)

	// 写入文件
	if err := os.WriteFile(path, []byte(newContent), 0644); err != nil {
		return "", fmt.Errorf("failed to write file: %w", err)
	}

	return fmt.Sprintf("Successfully replaced %d occurrence(s) in %s", occurrences, path), nil
}

// ListDir 列出目录
func (t *FileSystemTool) ListDir(ctx context.Context, params map[string]interface{}) (string, error) {
	path, ok := params["path"].(string)
	if !ok {
		return "", fmt.Errorf("path parameter is required")
	}

	// 检查路径权限
	if !t.isAllowed(path) {
		return "", fmt.Errorf("access to path %s is not allowed", path)
	}

	entries, err := os.ReadDir(path)
	if err != nil {
		return "", err
	}

	var result []string
	for _, entry := range entries {
		info := ""
		if entry.IsDir() {
			info = "[DIR] "
		}
		result = append(result, info+entry.Name())
	}

	return strings.Join(result, "\n"), nil
}

// isAllowed 检查路径是否允许访问
func (t *FileSystemTool) isAllowed(path string) bool {
	absPath, err := filepath.Abs(path)
	if err != nil {
		return false
	}

	// 检查拒绝列表（转换为绝对路径）
	for _, denied := range t.deniedPaths {
		absDenied, err := filepath.Abs(denied)
		if err == nil && strings.HasPrefix(absPath, absDenied) {
			return false
		}
	}

	// 如果没有允许列表，允许所有路径
	if len(t.allowedPaths) == 0 {
		return true
	}

	// 检查允许列表（转换为绝对路径）
	for _, allowed := range t.allowedPaths {
		absAllowed, err := filepath.Abs(allowed)
		if err == nil && strings.HasPrefix(absPath, absAllowed) {
			return true
		}
	}

	return false
}

// UpdateConfig 更新配置文件
func (t *FileSystemTool) UpdateConfig(ctx context.Context, params map[string]interface{}) (string, error) {
	fileType, ok := params["file"].(string)
	if !ok {
		return "", fmt.Errorf("file parameter is required (identity, agents, soul, or user)")
	}

	content, ok := params["content"].(string)
	if !ok {
		return "", fmt.Errorf("content parameter is required")
	}

	// 验证文件类型
	validFiles := map[string]string{
		"identity": "IDENTITY.md",
		"agents":   "AGENTS.md",
		"soul":     "SOUL.md",
		"user":     "USER.md",
	}

	filename, valid := validFiles[fileType]
	if !valid {
		return "", fmt.Errorf("invalid file type: %s (must be one of: identity, agents, soul, user)", fileType)
	}

	configDir := t.resolveConfigDir(ctx)
	if configDir == "" {
		return "", fmt.Errorf("workspace path is not configured")
	}
	path := filepath.Join(configDir, filename)

	// 确保目录存在
	if err := os.MkdirAll(configDir, 0755); err != nil {
		return "", fmt.Errorf("failed to create workspace directory: %w", err)
	}

	// 写入文件
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		return "", fmt.Errorf("failed to write config file: %w", err)
	}

	return fmt.Sprintf("Successfully updated %s\n\nThe changes will take effect in the next conversation.", filename), nil
}

// ReadConfig 读取配置文件
func (t *FileSystemTool) ReadConfig(ctx context.Context, params map[string]interface{}) (string, error) {
	fileType, ok := params["file"].(string)
	if !ok {
		return "", fmt.Errorf("file parameter is required (identity, agents, soul, or user)")
	}

	// 验证文件类型
	validFiles := map[string]string{
		"identity": "IDENTITY.md",
		"agents":   "AGENTS.md",
		"soul":     "SOUL.md",
		"user":     "USER.md",
	}

	filename, valid := validFiles[fileType]
	if !valid {
		return "", fmt.Errorf("invalid file type: %s (must be one of: identity, agents, soul, user)", fileType)
	}

	configDir := t.resolveConfigDir(ctx)
	if configDir == "" {
		return "", fmt.Errorf("workspace path is not configured")
	}
	path := filepath.Join(configDir, filename)

	// 读取文件
	content, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return fmt.Sprintf("Config file %s does not exist yet. Use update_config to create it.", filename), nil
		}
		return "", err
	}

	return string(content), nil
}

func (t *FileSystemTool) resolveConfigDir(ctx context.Context) string {
	explicitWorkspaceRoot := t.resolveWorkspaceRoot(ctx)

	if ctx != nil {
		if ownerID, ok := ctx.Value("bootstrap_owner_id").(string); ok && strings.TrimSpace(ownerID) != "" {
			ownerID = strings.TrimSpace(ownerID)
			if explicitWorkspaceRoot != "" {
				return workspace.AgentBootstrapDir(explicitWorkspaceRoot, ownerID)
			}
			if t.configDirResolverFn != nil {
				if resolved := strings.TrimSpace(t.configDirResolverFn(ownerID)); resolved != "" {
					return resolved
				}
			}
		}
		if agentID, ok := ctx.Value("agent_id").(string); ok && strings.TrimSpace(agentID) != "" {
			agentID = strings.TrimSpace(agentID)
			if explicitWorkspaceRoot != "" {
				return workspace.AgentBootstrapDir(explicitWorkspaceRoot, agentID)
			}
			if t.configDirResolverFn != nil {
				if resolved := strings.TrimSpace(t.configDirResolverFn(agentID)); resolved != "" {
					return resolved
				}
			}
		}
	}

	if explicitWorkspaceRoot != "" {
		return explicitWorkspaceRoot
	}
	return strings.TrimSpace(t.workspace)
}

func (t *FileSystemTool) resolveWorkspaceRoot(ctx context.Context) string {
	if ctx == nil {
		return ""
	}

	if root, ok := ctx.Value("workspace_root").(string); ok && strings.TrimSpace(root) != "" {
		return strings.TrimSpace(root)
	}

	channel, _ := ctx.Value("channel").(string)
	accountID, _ := ctx.Value("account_id").(string)
	senderID, _ := ctx.Value("sender_id").(string)
	tenantID, _ := ctx.Value("tenant_id").(string)
	channel = strings.TrimSpace(channel)
	accountID = strings.TrimSpace(accountID)
	senderID = strings.TrimSpace(senderID)
	tenantID = strings.TrimSpace(tenantID)
	if channel == "" || senderID == "" {
		return ""
	}

	return strings.TrimSpace((namespaces.Identity{
		TenantID:  tenantID,
		Channel:   channel,
		AccountID: accountID,
		SenderID:  senderID,
	}).WorkspaceDir(t.workspace))
}

// GetTools 获取所有文件系统工具
func (t *FileSystemTool) GetTools() []Tool {
	tools := []Tool{
		NewBaseTool(
			"read_file",
			"Read a file. Small/simple text files may be returned in full; dense or large files return a compact preview. Supports start_line/end_line, but wide ranges are capped.",
			map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"path": map[string]interface{}{
						"type":        "string",
						"description": "Path to the file to read",
					},
					"start_line": map[string]interface{}{
						"type":        "integer",
						"description": "Optional 1-based start line. If omitted but end_line is set, a window ending at end_line is returned.",
						"minimum":     1,
					},
					"end_line": map[string]interface{}{
						"type":        "integer",
						"description": "Optional 1-based end line. If omitted but start_line is set, a default window starting at start_line is returned.",
						"minimum":     1,
					},
				},
				"required": []string{"path"},
			},
			t.ReadFile,
		),
		NewBaseTool(
			"write_file",
			"Create a new file or fully overwrite an existing file with the complete target content. Use this when you intend to replace the whole file. Do NOT use run_shell for ordinary file writing when this tool fits.",
			map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"path": map[string]interface{}{
						"type":        "string",
						"description": "Path to the file to write",
					},
					"content": map[string]interface{}{
						"type":        "string",
						"description": "Content to write to the file",
					},
				},
				"required": []string{"path", "content"},
			},
			t.WriteFile,
		),
		NewBaseTool(
			"edit_file",
			"Edit an existing file by replacing exact text matches. Preferred tool for precise code or text changes in existing files. Do NOT use run_shell for normal source-file edits when this tool fits.",
			map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"path": map[string]interface{}{
						"type":        "string",
						"description": "Path to the file to edit",
					},
					"old_string": map[string]interface{}{
						"type":        "string",
						"description": "The exact text to be replaced (must match exactly)",
					},
					"new_string": map[string]interface{}{
						"type":        "string",
						"description": "The new text to replace old_string with",
					},
				},
				"required": []string{"path", "old_string", "new_string"},
			},
			t.EditFile,
		),
		NewBaseTool(
			"list_dir",
			"List contents of a directory",
			map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"path": map[string]interface{}{
						"type":        "string",
						"description": "Path to the directory",
					},
				},
				"required": []string{"path"},
			},
			t.ListDir,
		),
	}

	// 添加配置文件管理工具
	if t.workspace != "" {
		tools = append(tools,
			NewBaseTool(
				"update_config",
				"Update the current agent's configuration file (IDENTITY.md, AGENTS.md, SOUL.md, or USER.md). Changes take effect in the next conversation.",
				map[string]interface{}{
					"type": "object",
					"properties": map[string]interface{}{
						"file": map[string]interface{}{
							"type":        "string",
							"enum":        []string{"identity", "agents", "soul", "user"},
							"description": "The config file to update: identity (IDENTITY.md), agents (AGENTS.md), soul (SOUL.md), or user (USER.md)",
						},
						"content": map[string]interface{}{
							"type":        "string",
							"description": "The new content for the config file (Markdown format)",
						},
					},
					"required": []string{"file", "content"},
				},
				t.UpdateConfig,
			),
			NewBaseTool(
				"read_config",
				"Read the current agent's configuration file (IDENTITY.md, AGENTS.md, SOUL.md, or USER.md)",
				map[string]interface{}{
					"type": "object",
					"properties": map[string]interface{}{
						"file": map[string]interface{}{
							"type":        "string",
							"enum":        []string{"identity", "agents", "soul", "user"},
							"description": "The config file to read: identity (IDENTITY.md), agents (AGENTS.md), soul (SOUL.md), or user (USER.md)",
						},
					},
					"required": []string{"file"},
				},
				t.ReadConfig,
			),
		)
	}

	return tools
}

func optionalPositiveInt(params map[string]interface{}, key string) (int, bool, error) {
	raw, exists := params[key]
	if !exists || raw == nil {
		return 0, false, nil
	}

	switch v := raw.(type) {
	case int:
		if v < 1 {
			return 0, false, fmt.Errorf("%s must be >= 1", key)
		}
		return v, true, nil
	case int32:
		if v < 1 {
			return 0, false, fmt.Errorf("%s must be >= 1", key)
		}
		return int(v), true, nil
	case int64:
		if v < 1 {
			return 0, false, fmt.Errorf("%s must be >= 1", key)
		}
		return int(v), true, nil
	case float64:
		if v < 1 || v != float64(int(v)) {
			return 0, false, fmt.Errorf("%s must be a positive integer", key)
		}
		return int(v), true, nil
	case string:
		n, err := strconv.Atoi(strings.TrimSpace(v))
		if err != nil || n < 1 {
			return 0, false, fmt.Errorf("%s must be a positive integer", key)
		}
		return n, true, nil
	default:
		return 0, false, fmt.Errorf("%s must be a positive integer", key)
	}
}

func normalizeReadFileLineRange(startLine, endLine int) (int, int, bool) {
	capped := false
	if startLine <= 0 && endLine <= 0 {
		return 1, readFileRangeDefaultLines, false
	}
	if startLine <= 0 {
		startLine = endLine - readFileRangeDefaultLines + 1
		if startLine < 1 {
			startLine = 1
		}
	}
	if endLine <= 0 {
		endLine = startLine + readFileRangeDefaultLines - 1
	}
	if endLine-startLine+1 > readFileRangeMaxLines {
		endLine = startLine + readFileRangeMaxLines - 1
		capped = true
	}
	return startLine, endLine, capped
}

func readFilePrefix(path string, limit int) ([]byte, error) {
	if limit <= 0 {
		return nil, nil
	}

	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()

	data, err := io.ReadAll(io.LimitReader(f, int64(limit)))
	if err != nil {
		return nil, err
	}
	return data, nil
}

func looksLikeBinary(data []byte) bool {
	if len(data) == 0 {
		return false
	}
	if bytes.IndexByte(data, 0) >= 0 {
		return true
	}
	if utf8.Valid(data) {
		return false
	}

	controlBytes := 0
	for _, b := range data {
		switch {
		case b == '\n', b == '\r', b == '\t':
		case b < 0x20:
			controlBytes++
		}
	}

	return float64(controlBytes)/float64(len(data)) >= readFileControlByteFraction
}

func readFileLineRange(path string, startLine, endLine, requestedStartLine, requestedEndLine int, rangeCapped bool) (string, error) {
	if startLine < 1 || endLine < startLine {
		return "", fmt.Errorf("invalid line range: %d-%d", startLine, endLine)
	}

	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()

	reader := bufio.NewReader(f)
	selectedLines := make([]string, 0, minInt(endLine-startLine+1, readFileRangeMaxLines))
	totalLines := 0
	actualStart := 0
	actualEnd := 0

	for {
		line, readErr := reader.ReadString('\n')
		if readErr != nil && readErr != io.EOF {
			return "", readErr
		}

		if len(line) > 0 {
			totalLines++
			if totalLines >= startLine && totalLines <= endLine {
				if actualStart == 0 {
					actualStart = totalLines
				}
				actualEnd = totalLines
				selectedLines = append(selectedLines, line)
			}
		}

		if readErr == io.EOF {
			break
		}
		if totalLines > endLine {
			break
		}
	}

	if totalLines == 0 {
		return fmt.Sprintf("[read_file] %s is empty", path), nil
	}
	if actualStart == 0 {
		return fmt.Sprintf("[read_file] requested lines %d-%d, but %s only has %d lines", startLine, endLine, path, totalLines), nil
	}

	var result strings.Builder
	result.WriteString(fmt.Sprintf("[read_file] %s lines %d-%d\n", path, actualStart, actualEnd))
	if requestedStartLine > 0 || requestedEndLine > 0 {
		normalizedRequestedStart := startLine
		normalizedRequestedEnd := endLine
		if requestedStartLine > 0 {
			normalizedRequestedStart = requestedStartLine
		}
		if requestedEndLine > 0 {
			normalizedRequestedEnd = requestedEndLine
		}
		if rangeCapped {
			result.WriteString(fmt.Sprintf("[read_file] Requested range %d-%d was capped to %d-%d (max %d lines).\n", normalizedRequestedStart, normalizedRequestedEnd, startLine, endLine, readFileRangeMaxLines))
		}
	}
	result.WriteString(renderReadFileBody(selectedLines, readFileInlineMaxLines, readFileSummaryHeadLines, readFileSummaryTailLines))
	return result.String(), nil
}

func buildLargeFilePreview(path string, fileSize int64) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()

	reader := bufio.NewReader(f)
	head := make([]string, 0, readFilePreviewHeadLines)
	tail := make([]string, 0, readFilePreviewTailLines)
	totalLines := 0

	for {
		line, readErr := reader.ReadString('\n')
		if readErr != nil && readErr != io.EOF {
			return "", readErr
		}

		if len(line) > 0 {
			totalLines++
			if len(head) < readFilePreviewHeadLines {
				head = append(head, line)
			}
			if readFilePreviewTailLines > 0 {
				if len(tail) < readFilePreviewTailLines {
					tail = append(tail, line)
				} else {
					copy(tail, tail[1:])
					tail[len(tail)-1] = line
				}
			}
		}

		if readErr == io.EOF {
			break
		}
	}

	omittedLines := totalLines - len(head) - len(tail)
	if omittedLines < 0 {
		omittedLines = 0
	}

	var result strings.Builder
	result.WriteString(fmt.Sprintf("[read_file] %s is large (%s, %d lines). Returning a preview.\n", path, formatByteSize(fileSize), totalLines))
	result.WriteString(fmt.Sprintf("[read_file] Use start_line/end_line to fetch a narrower range. Default range window is %d lines, max %d lines.\n\n", readFileRangeDefaultLines, readFileRangeMaxLines))
	result.WriteString(fmt.Sprintf("--- file head (lines 1-%d) ---\n", len(head)))
	result.WriteString(renderReadFileLines(head))

	if omittedLines > 0 {
		current := result.String()
		if current != "" && !strings.HasSuffix(current, "\n") {
			result.WriteString("\n")
		}
		result.WriteString(fmt.Sprintf("\n--- omitted %d lines ---\n", omittedLines))
		result.WriteString(fmt.Sprintf("--- file tail (lines %d-%d) ---\n", totalLines-len(tail)+1, totalLines))
		result.WriteString(renderReadFileLines(tail))
	}

	return result.String(), nil
}

func renderSmallTextFile(path string, fileSize int64, content string) string {
	lines := splitReadFileLines(content)
	if shouldReturnRawReadFile(content, lines) {
		return content
	}

	var result strings.Builder
	result.WriteString(fmt.Sprintf("[read_file] %s is compact in bytes (%s) but dense in content. Returning a compact preview.\n", path, formatByteSize(fileSize)))
	result.WriteString(fmt.Sprintf("[read_file] Use start_line/end_line to fetch a narrower range. Default range window is %d lines, max %d lines.\n\n", readFileRangeDefaultLines, readFileRangeMaxLines))
	result.WriteString(renderReadFileBody(lines, readFileInlineMaxLines, readFileSummaryHeadLines, readFileSummaryTailLines))
	return result.String()
}

func shouldReturnRawReadFile(content string, lines []string) bool {
	if len(content) > readFileInlineMaxBytes {
		return false
	}
	if len(lines) > readFileInlineMaxLines {
		return false
	}
	for _, line := range lines {
		if runeCountWithoutTrailingNewline(line) > readFileSummaryMaxLineRunes {
			return false
		}
	}
	return true
}

func renderReadFileBody(lines []string, fullLimit, headLimit, tailLimit int) string {
	if len(lines) <= fullLimit {
		return renderReadFileLines(lines)
	}

	var result strings.Builder
	result.WriteString(renderReadFileLines(lines[:headLimit]))
	if result.Len() > 0 && !strings.HasSuffix(result.String(), "\n") {
		result.WriteString("\n")
	}
	result.WriteString(fmt.Sprintf("\n--- omitted %d lines ---\n", len(lines)-headLimit-tailLimit))
	result.WriteString(renderReadFileLines(lines[len(lines)-tailLimit:]))
	return result.String()
}

func renderReadFileLines(lines []string) string {
	var result strings.Builder
	truncatedLines := 0
	for _, line := range lines {
		shortened, changed := compactReadFileLine(line)
		if changed {
			truncatedLines++
		}
		result.WriteString(shortened)
	}

	if truncatedLines > 0 {
		if result.Len() > 0 && !strings.HasSuffix(result.String(), "\n") {
			result.WriteString("\n")
		}
		result.WriteString(fmt.Sprintf("\n--- %d long line(s) truncated to %d chars ---\n", truncatedLines, readFileSummaryMaxLineRunes))
	}

	return result.String()
}

func compactReadFileLine(line string) (string, bool) {
	hasNewline := strings.HasSuffix(line, "\n")
	core := strings.TrimSuffix(line, "\n")
	runes := []rune(core)
	if len(runes) <= readFileSummaryMaxLineRunes {
		return line, false
	}

	shortened := string(runes[:readFileSummaryMaxLineRunes]) + "...(truncated)"
	if hasNewline {
		shortened += "\n"
	}
	return shortened, true
}

func splitReadFileLines(content string) []string {
	if content == "" {
		return nil
	}

	lines := strings.SplitAfter(content, "\n")
	if len(lines) > 0 && lines[len(lines)-1] == "" {
		lines = lines[:len(lines)-1]
	}
	return lines
}

func formatBinaryFileNotice(path string, size int64) string {
	return fmt.Sprintf("[read_file] %s appears to be a binary file (%s). Use a more specific tool or command to inspect it.", path, formatByteSize(size))
}

func formatByteSize(size int64) string {
	if size < 1024 {
		return fmt.Sprintf("%d B", size)
	}
	if size < 1024*1024 {
		return fmt.Sprintf("%.1f KB", float64(size)/1024)
	}
	return fmt.Sprintf("%.1f MB", float64(size)/(1024*1024))
}

func minInt(a, b int) int {
	if a < b {
		return a
	}
	return b
}
