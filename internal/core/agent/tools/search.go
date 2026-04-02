package tools

import (
	"bufio"
	"bytes"
	"context"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"github.com/smallnest/goclaw/internal/core/agent/tooltypes"
)

const (
	defaultGlobHeadLimit    = 40
	defaultGrepHeadLimit    = 40
	defaultGrepContentLimit = 20
	maxSearchLineRunes      = 220
)

// SearchTool provides fast codebase search primitives.
//
// Strategy:
// - Prefer ripgrep when available for speed and mature glob/regex semantics
// - Fall back to Go filesystem walking and regex scanning when rg is unavailable
type SearchTool struct {
	allowedPaths []string
	deniedPaths  []string
	workspace    string
	rgPath       string
}

func NewSearchTool(allowedPaths, deniedPaths []string, workspace string) *SearchTool {
	rgPath, _ := exec.LookPath("rg")
	return &SearchTool{
		allowedPaths: append([]string(nil), allowedPaths...),
		deniedPaths:  append([]string(nil), deniedPaths...),
		workspace:    strings.TrimSpace(workspace),
		rgPath:       strings.TrimSpace(rgPath),
	}
}

func (t *SearchTool) GetTools() []Tool {
	return []Tool{
		NewBaseToolWithSpec(
			"glob_files",
			"Find files by path/name pattern. Use this first to narrow the candidate files before reading them.",
			map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"pattern": map[string]interface{}{
						"type":        "string",
						"description": "Glob-like file pattern such as '**/*.go', 'internal/**/router*.go', or '*.md'.",
					},
					"path": map[string]interface{}{
						"type":        "string",
						"description": "Optional directory to search within. Defaults to the current workspace.",
					},
					"head_limit": map[string]interface{}{
						"type":        "integer",
						"description": "Maximum number of file paths to return. Defaults to 40.",
						"minimum":     1,
					},
					"offset": map[string]interface{}{
						"type":        "integer",
						"description": "Skip the first N matches before returning results.",
						"minimum":     0,
					},
				},
				"required": []string{"pattern"},
			},
			tooltypes.ToolSpec{
				Concurrency: tooltypes.ConcurrencyConcurrent,
				Mutation:    tooltypes.MutationRead,
				Risk:        tooltypes.RiskLow,
				Tags:        []string{"search", "glob", "filesystem"},
			},
			t.GlobFiles,
		),
		NewBaseToolWithSpec(
			"grep_content",
			"Search file contents with a regex-like pattern and return matching files or lines with line numbers. Use this after glob_files to find the exact implementation site. Defaults to files_with_matches to avoid dumping too much code into context.",
			map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"pattern": map[string]interface{}{
						"type":        "string",
						"description": "Pattern to search for inside files. Treated as a regular expression.",
					},
					"path": map[string]interface{}{
						"type":        "string",
						"description": "Optional directory or file to search in. Defaults to the current workspace.",
					},
					"glob": map[string]interface{}{
						"type":        "string",
						"description": "Optional file filter such as '*.go' or '**/*_test.go'.",
					},
					"ignore_case": map[string]interface{}{
						"type":        "boolean",
						"description": "Whether to search case-insensitively.",
					},
					"output_mode": map[string]interface{}{
						"type":        "string",
						"enum":        []string{"content", "files_with_matches", "count"},
						"description": "content returns file:line:text, files_with_matches returns only matching files, count returns per-file match counts. Default is files_with_matches.",
					},
					"context_lines": map[string]interface{}{
						"type":        "integer",
						"description": "Number of context lines before/after each match in content mode.",
						"minimum":     0,
					},
					"head_limit": map[string]interface{}{
						"type":        "integer",
						"description": "Maximum number of result entries to return. Defaults to 40 for files/count and 20 for content mode.",
						"minimum":     1,
					},
					"offset": map[string]interface{}{
						"type":        "integer",
						"description": "Skip the first N result entries before returning results.",
						"minimum":     0,
					},
				},
				"required": []string{"pattern"},
			},
			tooltypes.ToolSpec{
				Concurrency: tooltypes.ConcurrencyConcurrent,
				Mutation:    tooltypes.MutationRead,
				Risk:        tooltypes.RiskLow,
				Tags:        []string{"search", "grep", "content"},
			},
			t.GrepContent,
		),
	}
}

func (t *SearchTool) GlobFiles(ctx context.Context, params map[string]interface{}) (string, error) {
	pattern := strings.TrimSpace(stringParamValue(params, "pattern"))
	if pattern == "" {
		return "", fmt.Errorf("pattern parameter is required")
	}

	root, err := t.resolveRoot(stringParamValue(params, "path"))
	if err != nil {
		return "", err
	}

	headLimit := intParamValue(params, "head_limit", defaultGlobHeadLimit)
	offset := intParamValue(params, "offset", 0)

	var matches []string
	if t.rgPath != "" {
		matches, err = t.globWithRipgrep(ctx, root, pattern)
	} else {
		matches, err = t.globFallback(ctx, root, pattern)
	}
	if err != nil {
		return "", err
	}

	matches = applyOffsetAndLimit(matches, offset, headLimit)
	return formatGlobResult(root, pattern, matches, offset, headLimit), nil
}

func (t *SearchTool) GrepContent(ctx context.Context, params map[string]interface{}) (string, error) {
	pattern := strings.TrimSpace(stringParamValue(params, "pattern"))
	if pattern == "" {
		return "", fmt.Errorf("pattern parameter is required")
	}

	root, err := t.resolveRoot(stringParamValue(params, "path"))
	if err != nil {
		return "", err
	}

	filter := strings.TrimSpace(stringParamValue(params, "glob"))
	ignoreCase := boolParamValue(params, "ignore_case")
	outputMode := strings.TrimSpace(stringParamValue(params, "output_mode"))
	if outputMode == "" {
		outputMode = "files_with_matches"
	}
	contextLines := intParamValue(params, "context_lines", 0)
	headLimit := intParamValue(params, "head_limit", grepHeadLimitForMode(outputMode))
	offset := intParamValue(params, "offset", 0)

	var lines []string
	if t.rgPath != "" {
		lines, err = t.grepWithRipgrep(ctx, root, pattern, filter, ignoreCase, outputMode, contextLines)
	} else {
		lines, err = t.grepFallback(ctx, root, pattern, filter, ignoreCase, outputMode, contextLines)
	}
	if err != nil {
		return "", err
	}

	lines = applyOffsetAndLimit(lines, offset, headLimit)
	return formatGrepResult(root, pattern, filter, outputMode, lines, offset, headLimit), nil
}

func (t *SearchTool) resolveRoot(raw string) (string, error) {
	root := strings.TrimSpace(raw)
	if root == "" {
		root = t.workspace
	}
	if root == "" {
		var err error
		root, err = os.Getwd()
		if err != nil {
			return "", fmt.Errorf("failed to resolve working directory: %w", err)
		}
	}

	if !filepath.IsAbs(root) {
		root = filepath.Join(t.workspace, root)
	}

	absRoot, err := filepath.Abs(root)
	if err != nil {
		return "", fmt.Errorf("failed to resolve search root: %w", err)
	}
	if !t.isAllowed(absRoot) {
		return "", fmt.Errorf("access to path %s is not allowed", absRoot)
	}

	info, err := os.Stat(absRoot)
	if err != nil {
		return "", fmt.Errorf("failed to stat search root %s: %w", absRoot, err)
	}
	if !info.IsDir() && !info.Mode().IsRegular() {
		return "", fmt.Errorf("path %s is not a searchable file or directory", absRoot)
	}
	return absRoot, nil
}

func (t *SearchTool) isAllowed(path string) bool {
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

func (t *SearchTool) globWithRipgrep(ctx context.Context, root, pattern string) ([]string, error) {
	args := []string{"--files", "--hidden", "-g", pattern, "-g", "!**/.git/**", root}
	cmd := exec.CommandContext(ctx, t.rgPath, args...)
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		if stdout.Len() == 0 {
			return nil, fmt.Errorf("glob_files failed via ripgrep: %s", strings.TrimSpace(stderr.String()))
		}
	}

	var matches []string
	scanner := bufio.NewScanner(bytes.NewReader(stdout.Bytes()))
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}
		matches = append(matches, normalizeRelative(root, line))
	}
	sort.Strings(matches)
	return matches, nil
}

func (t *SearchTool) globFallback(ctx context.Context, root, pattern string) ([]string, error) {
	matcher, err := compileGlobPattern(pattern)
	if err != nil {
		return nil, err
	}
	var matches []string
	err = filepath.WalkDir(root, func(path string, d fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}
		if d.IsDir() && d.Name() == ".git" {
			return filepath.SkipDir
		}
		if d.IsDir() {
			return nil
		}
		rel := normalizeRelative(root, path)
		if matcher.MatchString(rel) {
			matches = append(matches, rel)
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	sort.Strings(matches)
	return matches, nil
}

func (t *SearchTool) grepWithRipgrep(ctx context.Context, root, pattern, glob string, ignoreCase bool, outputMode string, contextLines int) ([]string, error) {
	args := []string{"--hidden", "--color", "never", "--no-heading", "-g", "!**/.git/**"}
	if ignoreCase {
		args = append(args, "-i")
	}
	if glob != "" {
		args = append(args, "-g", glob)
	}
	switch outputMode {
	case "files_with_matches":
		args = append(args, "-l")
	case "count":
		args = append(args, "-c")
	default:
		args = append(args, "-n")
		if contextLines > 0 {
			args = append(args, "-C", strconv.Itoa(contextLines))
		}
	}
	args = append(args, pattern, root)

	cmd := exec.CommandContext(ctx, t.rgPath, args...)
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	err := cmd.Run()
	if err != nil {
		// ripgrep exit 1 means "no matches"
		if exitErr, ok := err.(*exec.ExitError); ok && exitErr.ExitCode() == 1 {
			return []string{}, nil
		}
		return nil, fmt.Errorf("grep_content failed via ripgrep: %s", strings.TrimSpace(stderr.String()))
	}

	var lines []string
	scanner := bufio.NewScanner(bytes.NewReader(stdout.Bytes()))
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}
		lines = append(lines, normalizeSearchOutput(root, line, outputMode))
	}
	return lines, nil
}

func (t *SearchTool) grepFallback(ctx context.Context, root, pattern, glob string, ignoreCase bool, outputMode string, contextLines int) ([]string, error) {
	re, err := compileContentPattern(pattern, ignoreCase)
	if err != nil {
		return nil, err
	}

	var globMatcher *regexp.Regexp
	if glob != "" {
		globMatcher, err = compileGlobPattern(glob)
		if err != nil {
			return nil, err
		}
	}

	paths, err := t.globFallback(ctx, root, fallbackPattern(glob))
	if err != nil {
		return nil, err
	}

	var result []string
	for _, relPath := range paths {
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		default:
		}
		if globMatcher != nil && !globMatcher.MatchString(relPath) {
			continue
		}

		absPath := filepath.Join(root, filepath.FromSlash(relPath))
		content, err := os.ReadFile(absPath)
		if err != nil {
			continue
		}

		lines := strings.Split(string(content), "\n")
		switch outputMode {
		case "files_with_matches":
			if fileContainsMatch(lines, re) {
				result = append(result, relPath)
			}
		case "count":
			count := countMatches(lines, re)
			if count > 0 {
				result = append(result, fmt.Sprintf("%s:%d", relPath, count))
			}
		default:
			result = append(result, collectMatchingLines(relPath, lines, re, contextLines)...)
		}
	}

	return result, nil
}

func fileContainsMatch(lines []string, re *regexp.Regexp) bool {
	for _, line := range lines {
		if re.MatchString(line) {
			return true
		}
	}
	return false
}

func countMatches(lines []string, re *regexp.Regexp) int {
	total := 0
	for _, line := range lines {
		if re.MatchString(line) {
			total++
		}
	}
	return total
}

func collectMatchingLines(relPath string, lines []string, re *regexp.Regexp, contextLines int) []string {
	if contextLines <= 0 {
		var out []string
		for idx, line := range lines {
			if re.MatchString(line) {
				out = append(out, fmt.Sprintf("%s:%d:%s", relPath, idx+1, truncateSearchLine(line)))
			}
		}
		return out
	}

	type lineRange struct{ start, end int }
	var ranges []lineRange
	for idx, line := range lines {
		if !re.MatchString(line) {
			continue
		}
		start := idx - contextLines
		if start < 0 {
			start = 0
		}
		end := idx + contextLines
		if end >= len(lines) {
			end = len(lines) - 1
		}
		ranges = append(ranges, lineRange{start: start, end: end})
	}
	if len(ranges) == 0 {
		return nil
	}

	var merged []lineRange
	for _, current := range ranges {
		if len(merged) == 0 {
			merged = append(merged, current)
			continue
		}
		last := &merged[len(merged)-1]
		if current.start <= last.end+1 {
			if current.end > last.end {
				last.end = current.end
			}
			continue
		}
		merged = append(merged, current)
	}

	var out []string
	for _, r := range merged {
		for idx := r.start; idx <= r.end; idx++ {
			out = append(out, fmt.Sprintf("%s:%d:%s", relPath, idx+1, truncateSearchLine(lines[idx])))
		}
	}
	return out
}

func compileContentPattern(pattern string, ignoreCase bool) (*regexp.Regexp, error) {
	if ignoreCase {
		pattern = "(?i)" + pattern
	}
	re, err := regexp.Compile(pattern)
	if err != nil {
		return nil, fmt.Errorf("invalid pattern: %w", err)
	}
	return re, nil
}

func compileGlobPattern(pattern string) (*regexp.Regexp, error) {
	var b strings.Builder
	b.WriteString("^")
	normalized := filepath.ToSlash(strings.TrimSpace(pattern))
	for i := 0; i < len(normalized); i++ {
		ch := normalized[i]
		switch ch {
		case '*':
			if i+1 < len(normalized) && normalized[i+1] == '*' {
				if i+2 < len(normalized) && normalized[i+2] == '/' {
					b.WriteString(`(?:.*/)?`)
					i += 2
				} else {
					b.WriteString(".*")
					i++
				}
			} else {
				b.WriteString(`[^/]*`)
			}
		case '?':
			b.WriteString(`[^/]`)
		case '.', '+', '(', ')', '|', '^', '$', '{', '}', '[', ']', '\\':
			b.WriteString(`\`)
			b.WriteByte(ch)
		default:
			b.WriteByte(ch)
		}
	}
	b.WriteString("$")
	return regexp.Compile(b.String())
}

func fallbackPattern(glob string) string {
	if strings.TrimSpace(glob) == "" {
		return "**"
	}
	return glob
}

func normalizeRelative(root, path string) string {
	if rel, err := filepath.Rel(root, path); err == nil {
		return filepath.ToSlash(rel)
	}
	return filepath.ToSlash(path)
}

func normalizeSearchOutput(root, line, outputMode string) string {
	switch outputMode {
	case "files_with_matches", "count":
		parts := strings.SplitN(line, ":", 2)
		if len(parts) == 2 && outputMode == "count" {
			return fmt.Sprintf("%s:%s", normalizeRelative(root, parts[0]), parts[1])
		}
		return normalizeRelative(root, line)
	default:
		parts := strings.SplitN(line, ":", 3)
		if len(parts) < 3 {
			return line
		}
		return fmt.Sprintf("%s:%s:%s", normalizeRelative(root, parts[0]), parts[1], truncateSearchLine(parts[2]))
	}
}

func applyOffsetAndLimit(items []string, offset, limit int) []string {
	if offset < 0 {
		offset = 0
	}
	if offset >= len(items) {
		return []string{}
	}
	items = items[offset:]
	if limit <= 0 || limit >= len(items) {
		return items
	}
	return items[:limit]
}

func formatGlobResult(root, pattern string, matches []string, offset, limit int) string {
	var b strings.Builder
	b.WriteString(fmt.Sprintf("[glob_files] root: %s\n", root))
	b.WriteString(fmt.Sprintf("[glob_files] pattern: %s\n", pattern))
	b.WriteString(fmt.Sprintf("[glob_files] returned: %d (offset=%d, limit=%d)\n", len(matches), offset, limit))
	if len(matches) == 0 {
		b.WriteString("[glob_files] no matches\n")
		return b.String()
	}
	for _, match := range matches {
		b.WriteString(match)
		b.WriteByte('\n')
	}
	return strings.TrimRight(b.String(), "\n")
}

func formatGrepResult(root, pattern, glob, outputMode string, lines []string, offset, limit int) string {
	var b strings.Builder
	b.WriteString(fmt.Sprintf("[grep_content] root: %s\n", root))
	b.WriteString(fmt.Sprintf("[grep_content] pattern: %s\n", pattern))
	if glob != "" {
		b.WriteString(fmt.Sprintf("[grep_content] glob: %s\n", glob))
	}
	b.WriteString(fmt.Sprintf("[grep_content] mode: %s\n", outputMode))
	b.WriteString(fmt.Sprintf("[grep_content] returned: %d (offset=%d, limit=%d)\n", len(lines), offset, limit))
	if len(lines) == 0 {
		b.WriteString("[grep_content] no matches\n")
		return b.String()
	}
	for _, line := range lines {
		b.WriteString(line)
		b.WriteByte('\n')
	}
	return strings.TrimRight(b.String(), "\n")
}

func grepHeadLimitForMode(outputMode string) int {
	if outputMode == "content" {
		return defaultGrepContentLimit
	}
	return defaultGrepHeadLimit
}

func truncateSearchLine(line string) string {
	line = strings.TrimRight(line, "\r")
	runes := []rune(line)
	if len(runes) <= maxSearchLineRunes {
		return line
	}
	return string(runes[:maxSearchLineRunes]) + "...(truncated)"
}

func stringParamValue(params map[string]interface{}, key string) string {
	value, _ := params[key].(string)
	return strings.TrimSpace(value)
}

func intParamValue(params map[string]interface{}, key string, fallback int) int {
	raw, ok := params[key]
	if !ok || raw == nil {
		return fallback
	}
	switch value := raw.(type) {
	case int:
		return value
	case int32:
		return int(value)
	case int64:
		return int(value)
	case float64:
		return int(value)
	case string:
		if n, err := strconv.Atoi(strings.TrimSpace(value)); err == nil {
			return n
		}
	}
	return fallback
}

func boolParamValue(params map[string]interface{}, key string) bool {
	raw, ok := params[key]
	if !ok || raw == nil {
		return false
	}
	switch value := raw.(type) {
	case bool:
		return value
	case string:
		switch strings.ToLower(strings.TrimSpace(value)) {
		case "true", "1", "yes":
			return true
		}
	}
	return false
}
