package agent

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"unicode"

	"github.com/smallnest/goclaw/internal/logger"
	"go.uber.org/zap"
)

// SessionAgentRouter 会话级 Agent 路由器
// 记录每个 channel 会话当前绑定的 Agent ID，支持运行时动态切换。
type SessionAgentRouter struct {
	mu       sync.RWMutex
	routes   map[string]string // sessionKey -> agentID
	dataPath string            // 持久化文件路径
}

// NewSessionAgentRouter 创建会话 Agent 路由器
func NewSessionAgentRouter(dataDir string) *SessionAgentRouter {
	r := &SessionAgentRouter{
		routes: make(map[string]string),
	}
	if dataDir != "" {
		r.dataPath = filepath.Join(dataDir, "session_agent_routes.json")
		if err := r.loadFromDisk(); err != nil {
			logger.Warn("Failed to load session agent routes from disk", zap.Error(err))
		}
	}
	return r
}

// GetAgentID 获取会话绑定的 Agent ID，未绑定时返回空字符串
func (r *SessionAgentRouter) GetAgentID(sessionKey string) string {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.routes[sessionKey]
}

// SetAgentID 为会话绑定 Agent ID
func (r *SessionAgentRouter) SetAgentID(sessionKey, agentID string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.routes[sessionKey] = agentID
	_ = r.saveToDisk()
}

// ClearAgentID 清除会话绑定，回退到默认 Agent
func (r *SessionAgentRouter) ClearAgentID(sessionKey string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	delete(r.routes, sessionKey)
	_ = r.saveToDisk()
}

// loadFromDisk 从磁盘加载路由表
func (r *SessionAgentRouter) loadFromDisk() error {
	if r.dataPath == "" {
		return nil
	}
	data, err := os.ReadFile(r.dataPath)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return err
	}
	return json.Unmarshal(data, &r.routes)
}

// saveToDisk 持久化路由表到磁盘（调用方已持有写锁）
func (r *SessionAgentRouter) saveToDisk() error {
	if r.dataPath == "" {
		return nil
	}
	if err := os.MkdirAll(filepath.Dir(r.dataPath), 0o755); err != nil {
		return err
	}
	data, err := json.MarshalIndent(r.routes, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(r.dataPath, data, 0o644)
}

// agentSwitchResult 切换指令解析结果
type agentSwitchResult struct {
	IsSwitch bool   // 是否是切换指令
	AgentID  string // 目标 Agent ID（"/agent" 时为空，表示查询当前）
	IsClear  bool   // 是否是 "/agent clear"，清除会话级切换
	IsQuery  bool   // 是否是查询当前 Agent 的指令
}

// parseAgentSwitchCommand 解析切换指令
// 支持格式：
//
//	/agent              -> 查询当前使用的 Agent
//	/agent list         -> 列出所有可用 Agent
//	/agent <id>         -> 切换到指定 Agent
//	/agent default      -> 切换到 ID 为 default 的 Agent
//	/agent clear        -> 清除会话级切换，恢复自动路由（binding > default）
func parseAgentSwitchCommand(content string) *agentSwitchResult {
	trimmed := sanitizeSlashCommandContent(content)
	if trimmed == "" {
		return &agentSwitchResult{}
	}

	parts := strings.Fields(trimmed)
	if len(parts) == 0 || parts[0] != "/agent" {
		return &agentSwitchResult{}
	}

	if len(parts) == 1 {
		// "/agent" 单独使用 -> 查询当前
		return &agentSwitchResult{IsSwitch: true, IsQuery: true}
	}

	sub := strings.ToLower(parts[1])
	switch sub {
	case "default":
		return &agentSwitchResult{IsSwitch: true, AgentID: "default"}
	case "clear":
		return &agentSwitchResult{IsSwitch: true, IsClear: true}
	case "list":
		// list 由调用方处理，这里只标记为切换指令入口
		return &agentSwitchResult{IsSwitch: true, AgentID: "list"}
	default:
		return &agentSwitchResult{IsSwitch: true, AgentID: parts[1]}
	}
}

func sanitizeSlashCommandContent(content string) string {
	cleaned := strings.Map(func(r rune) rune {
		switch {
		case unicode.IsSpace(r):
			return ' '
		case unicode.Is(unicode.Cf, r):
			return -1
		case unicode.IsControl(r):
			return -1
		default:
			return r
		}
	}, content)

	return strings.TrimSpace(cleaned)
}

func formatAgentRouteSuffix(source string) string {
	switch source {
	case "session":
		return "（会话切换）"
	case "binding":
		return "（绑定）"
	case "default":
		return "（默认）"
	default:
		return ""
	}
}

// buildAgentSwitchReply 构建切换指令的回复文本
func buildAgentSwitchReply(cmd *agentSwitchResult, currentAgentID, currentSource, defaultAgentID string, allAgents []string) string {
	if cmd.IsQuery {
		active := currentAgentID
		if active == "" {
			active = defaultAgentID
			currentSource = "default"
		}
		return fmt.Sprintf("当前会话使用的 Agent：`%s`%s", active, formatAgentRouteSuffix(currentSource))
	}

	if cmd.IsClear {
		active := currentAgentID
		if active == "" {
			active = defaultAgentID
			currentSource = "default"
		}
		return fmt.Sprintf("已清除会话级 Agent 切换，当前路由到 Agent：`%s`%s", active, formatAgentRouteSuffix(currentSource))
	}

	if cmd.AgentID == "list" {
		if len(allAgents) == 0 {
			return "暂无可用 Agent。"
		}
		lines := []string{"**可用 Agent 列表：**"}
		for _, id := range allAgents {
			mark := ""
			if id == currentAgentID {
				suffix := formatAgentRouteSuffix(currentSource)
				if suffix == "" {
					mark = " ← 当前"
				} else {
					mark = " ← 当前" + suffix
				}
			}
			lines = append(lines, fmt.Sprintf("- `%s`%s", id, mark))
		}
		lines = append(lines, "\n使用 `/agent <id>` 切换，`/agent clear` 清除会话级切换。")
		return strings.Join(lines, "\n")
	}

	return fmt.Sprintf("已切换到 Agent：`%s`", cmd.AgentID)
}
