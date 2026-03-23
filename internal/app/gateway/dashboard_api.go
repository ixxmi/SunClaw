package gateway

import (
	"bufio"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/smallnest/goclaw/internal/core/agent"
	"github.com/smallnest/goclaw/internal/core/config"
	"github.com/smallnest/goclaw/internal/core/cron"
	"gopkg.in/yaml.v3"
)

type dashboardSnapshot struct {
	GeneratedAt string               `json:"generatedAt"`
	Chat        dashboardChat        `json:"chat"`
	Overview    dashboardOverview    `json:"overview"`
	Channels    []dashboardChannel   `json:"channels"`
	Instances   []dashboardInstance  `json:"instances"`
	Sessions    []dashboardSession   `json:"sessions"`
	CronJobs    []dashboardCronJob   `json:"cronJobs"`
	Skills      []dashboardSkill     `json:"skills"`
	Nodes       []dashboardNode      `json:"nodes"`
	Config      dashboardConfigView  `json:"config"`
	Debug       []dashboardDebugItem `json:"debug"`
	Logs        []dashboardLogItem   `json:"logs"`
	Docs        []dashboardDocItem   `json:"docs"`
}

type dashboardChat struct {
	Alerts       []dashboardAlert       `json:"alerts"`
	QuickActions []dashboardQuickAction `json:"quickActions"`
}

type dashboardAlert struct {
	Text string `json:"text"`
	Tone string `json:"tone"`
}

type dashboardQuickAction struct {
	Label string `json:"label"`
	Note  string `json:"note"`
}

type dashboardOverview struct {
	Cards  []dashboardCard          `json:"cards"`
	Panels []dashboardOverviewPanel `json:"panels"`
}

type dashboardCard struct {
	Label string `json:"label"`
	Value string `json:"value"`
	Note  string `json:"note"`
	Tone  string `json:"tone"`
}

type dashboardOverviewPanel struct {
	Title string   `json:"title"`
	Note  string   `json:"note"`
	Items []string `json:"items"`
}

type dashboardChannel struct {
	Name    string `json:"name"`
	Account string `json:"account"`
	Route   string `json:"route"`
	Mode    string `json:"mode"`
	Status  string `json:"status"`
	Health  string `json:"health"`
	Tone    string `json:"tone"`
}

type dashboardInstance struct {
	Name     string `json:"name"`
	Kind     string `json:"kind"`
	Endpoint string `json:"endpoint"`
	Auth     string `json:"auth"`
	Status   string `json:"status"`
	LastSeen string `json:"lastSeen"`
	Tone     string `json:"tone"`
}

type dashboardSession struct {
	Key       string `json:"key"`
	Agent     string `json:"agent"`
	Channel   string `json:"channel"`
	Messages  string `json:"messages"`
	UpdatedAt string `json:"updatedAt"`
	State     string `json:"state"`
	Tone      string `json:"tone"`
}

type dashboardCronJob struct {
	Name     string `json:"name"`
	Schedule string `json:"schedule"`
	Target   string `json:"target"`
	Delivery string `json:"delivery"`
	NextRun  string `json:"nextRun"`
	State    string `json:"state"`
	Tone     string `json:"tone"`
}

type dashboardSkill struct {
	Name     string `json:"name"`
	Source   string `json:"source"`
	Requires string `json:"requires"`
	Scope    string `json:"scope"`
	State    string `json:"state"`
	Tone     string `json:"tone"`
}

type dashboardNode struct {
	Name      string   `json:"name"`
	Role      string   `json:"role"`
	Provider  string   `json:"provider"`
	Workspace string   `json:"workspace"`
	State     string   `json:"state"`
	Tools     []string `json:"tools"`
	Tone      string   `json:"tone"`
}

type dashboardConfigView struct {
	Groups  []dashboardConfigGroup `json:"groups"`
	Preview string                 `json:"preview"`
}

type dashboardConfigGroup struct {
	Title  string                 `json:"title"`
	Fields []dashboardConfigField `json:"fields"`
}

type dashboardConfigField struct {
	Label string `json:"label"`
	Value string `json:"value"`
}

type dashboardDebugItem struct {
	Title       string `json:"title"`
	Description string `json:"description"`
	State       string `json:"state"`
	Tone        string `json:"tone"`
}

type dashboardLogItem struct {
	Time    string `json:"time"`
	Level   string `json:"level"`
	Origin  string `json:"origin"`
	Message string `json:"message"`
	Tone    string `json:"tone"`
}

type dashboardDocItem struct {
	Title       string `json:"title"`
	Path        string `json:"path"`
	Description string `json:"description"`
}

func (s *Server) handleDashboardAPI(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	snapshot, err := s.buildDashboardSnapshot()
	if err != nil {
		http.Error(w, fmt.Sprintf("failed to build dashboard snapshot: %v", err), http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_ = json.NewEncoder(w).Encode(snapshot)
}

func (s *Server) buildDashboardSnapshot() (*dashboardSnapshot, error) {
	channels := s.buildChannelRows()
	cronJobs := s.buildCronRows()
	sessionRows := s.buildSessionRows()
	sessionCount := s.countSessionFiles()

	snapshot := &dashboardSnapshot{
		GeneratedAt: time.Now().Format(time.RFC3339),
		Chat: dashboardChat{
			Alerts: s.buildChatAlerts(),
			QuickActions: []dashboardQuickAction{
				{Label: "Check gateway health", Note: "Verify `/health` and `/rpc` before mutating runtime state."},
				{Label: "Inspect session routes", Note: "Use session and binding views to confirm the active target agent."},
				{Label: "Review pending approvals", Note: "High-risk commands should remain behind manual review."},
			},
		},
		Overview: dashboardOverview{
			Cards: []dashboardCard{
				{
					Label: "Gateway",
					Value: fmt.Sprintf("HTTP %d / WS %d", dashboardHTTPPort(s.config), dashboardWSPort(s.wsConfig)),
					Note:  "HTTP API and WebSocket gateway are exposed as separate control surfaces.",
					Tone:  "sky",
				},
				{
					Label: "Agents",
					Value: fmt.Sprintf("%d Active", len(s.config.Agents.List)),
					Note:  "Derived from the current agents configuration.",
					Tone:  "emerald",
				},
				{
					Label: "Channels",
					Value: fmt.Sprintf("%d Connected", len(channels)),
					Note:  "Current channel set built from active channel manager registrations.",
					Tone:  "amber",
				},
				{
					Label: "Automation",
					Value: fmt.Sprintf("%d Jobs", len(cronJobs)),
					Note:  "Scheduled jobs from the live cron service when available.",
					Tone:  "rose",
				},
			},
			Panels: []dashboardOverviewPanel{
				{
					Title: "Control Plane",
					Note:  "Gateway / auth / approvals",
					Items: []string{
						fmt.Sprintf("HTTP API exposed at http://%s:%d", dashboardHTTPHost(s.config), dashboardHTTPPort(s.config)),
						fmt.Sprintf("WebSocket endpoint exposed at %s", dashboardWSURL(s.wsConfig)),
						fmt.Sprintf("Active WebSocket clients: %d", s.connectionCount()),
					},
				},
				{
					Title: "Runtime Focus",
					Note:  "Sessions / channels / cron",
					Items: []string{
						fmt.Sprintf("Active session files: %d", sessionCount),
						fmt.Sprintf("Registered channels: %d", len(channels)),
						fmt.Sprintf("Enabled cron jobs: %d", countEnabledCronRows(cronJobs)),
					},
				},
			},
		},
		Channels:  channels,
		Instances: s.buildInstanceRows(),
		Sessions:  sessionRows,
		CronJobs:  cronJobs,
		Skills:    s.buildSkillRows(),
		Nodes:     s.buildNodeRows(),
		Config:    s.buildConfigView(),
		Debug:     s.buildDebugRows(),
		Logs:      s.buildLogRows(20),
		Docs:      s.buildDocRows(),
	}

	return snapshot, nil
}

func (s *Server) buildChatAlerts() []dashboardAlert {
	alerts := []dashboardAlert{
		{
			Text: fmt.Sprintf("Gateway HTTP API reachable at http://%s:%d/health.", dashboardHTTPHost(s.config), dashboardHTTPPort(s.config)),
			Tone: "sky",
		},
	}

	if s.wsConfig != nil && s.wsConfig.EnableAuth {
		alerts = append(alerts, dashboardAlert{
			Text: "WebSocket gateway authentication is enabled; provide a token for browser access.",
			Tone: "amber",
		})
	} else {
		alerts = append(alerts, dashboardAlert{
			Text: "WebSocket gateway authentication is currently disabled for local access.",
			Tone: "emerald",
		})
	}

	return alerts
}

func (s *Server) buildChannelRows() []dashboardChannel {
	rows := make([]dashboardChannel, 0)
	registered := make(map[string]map[string]interface{})
	for _, name := range s.channelMgr.List() {
		status, _ := s.channelMgr.Status(name)
		registered[name] = status
	}

	appendRow := func(name, account, mode string) {
		statusText := "Configured"
		health := "configuration only"
		tone := "slate"
		if _, ok := registered[name]; ok {
			statusText = "Connected"
			health = "registered in channel manager"
			tone = "sky"
		}
		route := s.bindingForChannel(name, account)
		if route == "" {
			route = "default route"
		}
		rows = append(rows, dashboardChannel{
			Name:    name,
			Account: account,
			Route:   route,
			Mode:    mode,
			Status:  statusText,
			Health:  health,
			Tone:    tone,
		})
	}

	appendTopLevelChannelRows := func(name, fallbackMode string, enabled bool, accounts map[string]config.ChannelAccountConfig) {
		if len(accounts) > 0 {
			accountNames := make([]string, 0, len(accounts))
			for accountName := range accounts {
				accountNames = append(accountNames, accountName)
			}
			sort.Strings(accountNames)
			for _, accountName := range accountNames {
				accountCfg := accounts[accountName]
				if !accountCfg.Enabled {
					continue
				}
				mode := accountCfg.Mode
				if mode == "" {
					mode = fallbackMode
				}
				appendRow(name, accountName, mode)
			}
			return
		}
		if enabled {
			appendRow(name, "default", fallbackMode)
		}
	}

	appendTopLevelChannelRows("telegram", "bot", s.config.Channels.Telegram.Enabled, s.config.Channels.Telegram.Accounts)
	appendTopLevelChannelRows("whatsapp", "bridge", s.config.Channels.WhatsApp.Enabled, s.config.Channels.WhatsApp.Accounts)
	appendTopLevelChannelRows("weixin", defaultString(s.config.Channels.Weixin.Mode, "bridge"), s.config.Channels.Weixin.Enabled, s.config.Channels.Weixin.Accounts)
	appendTopLevelChannelRows("imessage", "bridge", s.config.Channels.IMessage.Enabled, s.config.Channels.IMessage.Accounts)
	appendTopLevelChannelRows("feishu", "webhook", s.config.Channels.Feishu.Enabled, s.config.Channels.Feishu.Accounts)
	appendTopLevelChannelRows("dingtalk", "oauth", s.config.Channels.DingTalk.Enabled, s.config.Channels.DingTalk.Accounts)
	appendTopLevelChannelRows("qq", "bot", s.config.Channels.QQ.Enabled, s.config.Channels.QQ.Accounts)
	appendTopLevelChannelRows("wework", defaultString(s.config.Channels.WeWork.Mode, "webhook"), s.config.Channels.WeWork.Enabled, s.config.Channels.WeWork.Accounts)
	appendTopLevelChannelRows("infoflow", "webhook", s.config.Channels.Infoflow.Enabled, s.config.Channels.Infoflow.Accounts)
	appendTopLevelChannelRows("gotify", "push", s.config.Channels.Gotify.Enabled, s.config.Channels.Gotify.Accounts)

	sort.Slice(rows, func(i, j int) bool {
		if rows[i].Name == rows[j].Name {
			return rows[i].Account < rows[j].Account
		}
		return rows[i].Name < rows[j].Name
	})

	return rows
}

func (s *Server) buildInstanceRows() []dashboardInstance {
	httpHost := dashboardHTTPHost(s.config)
	httpPort := dashboardHTTPPort(s.config)

	return []dashboardInstance{
		{
			Name:     "HTTP API",
			Kind:     "REST / JSON-RPC",
			Endpoint: fmt.Sprintf("http://%s:%d", httpHost, httpPort),
			Auth:     "local network",
			Status:   "Online",
			LastSeen: "live",
			Tone:     "sky",
		},
		{
			Name:     "Gateway WS",
			Kind:     "WebSocket",
			Endpoint: dashboardWSURL(s.wsConfig),
			Auth:     dashboardWSAuthLabel(s.wsConfig),
			Status:   "Online",
			LastSeen: "live",
			Tone:     "emerald",
		},
		{
			Name:     "Dashboard Clients",
			Kind:     "Active connections",
			Endpoint: fmt.Sprintf("%d active websocket clients", s.connectionCount()),
			Auth:     "runtime state",
			Status:   "Observed",
			LastSeen: time.Now().Format("15:04:05"),
			Tone:     "amber",
		},
	}
}

func (s *Server) buildSessionRows() []dashboardSession {
	return s.buildSessionRowsWithLimit(20)
}

func (s *Server) countSessionFiles() int {
	if s.sessionMgr == nil {
		return 0
	}

	keys, err := s.sessionMgr.List()
	if err != nil {
		return 0
	}

	return len(keys)
}

func (s *Server) buildSessionRowsWithLimit(limit int) []dashboardSession {
	if s.sessionMgr == nil {
		return []dashboardSession{}
	}

	keys, err := s.sessionMgr.List()
	if err != nil {
		return []dashboardSession{}
	}

	type sortableSessionRow struct {
		row       dashboardSession
		updatedAt time.Time
	}

	rows := make([]sortableSessionRow, 0, len(keys))
	for _, key := range keys {
		sess, err := s.sessionMgr.GetOrCreate(key)
		if err != nil {
			continue
		}
		state, tone := classifySessionState(key, sess.UpdatedAt)
		rows = append(rows, sortableSessionRow{
			row: dashboardSession{
				Key:       key,
				Agent:     extractSessionAgent(key),
				Channel:   extractSessionChannel(key),
				Messages:  fmt.Sprintf("%d", len(sess.Messages)),
				UpdatedAt: humanizeSince(sess.UpdatedAt),
				State:     state,
				Tone:      tone,
			},
			updatedAt: sess.UpdatedAt,
		})
	}

	sort.Slice(rows, func(i, j int) bool {
		return rows[i].updatedAt.After(rows[j].updatedAt)
	})

	if limit > 0 && len(rows) > limit {
		rows = rows[:limit]
	}

	result := make([]dashboardSession, 0, len(rows))
	for _, row := range rows {
		result = append(result, row.row)
	}

	return result
}

func (s *Server) buildCronRows() []dashboardCronJob {
	if s.cronSvc == nil {
		return []dashboardCronJob{}
	}

	jobs := s.cronSvc.ListJobs()
	rows := make([]dashboardCronJob, 0, len(jobs))
	for _, job := range jobs {
		if job == nil {
			continue
		}
		nextRun := "-"
		if job.State.NextRunAt != nil {
			nextRun = job.State.NextRunAt.Format("2006-01-02 15:04")
		}
		state := "Disabled"
		tone := "slate"
		if job.State.Enabled {
			state = "Enabled"
			tone = "emerald"
		}
		if job.State.LastStatus == "error" {
			state = "Error"
			tone = "rose"
		}
		rows = append(rows, dashboardCronJob{
			Name:     job.Name,
			Schedule: formatCronSchedule(job),
			Target:   string(job.SessionTarget),
			Delivery: formatCronDelivery(job.Delivery),
			NextRun:  nextRun,
			State:    state,
			Tone:     tone,
		})
	}

	sort.Slice(rows, func(i, j int) bool {
		return rows[i].Name < rows[j].Name
	})

	return rows
}

func (s *Server) buildSkillRows() []dashboardSkill {
	workspacePath, err := config.GetWorkspacePath(s.config)
	if err != nil {
		return []dashboardSkill{}
	}

	loader := agent.NewWorkspaceSkillsLoader(workspacePath)
	if err := loader.Discover(); err != nil {
		return []dashboardSkill{}
	}

	loaded := loader.List()
	sort.Slice(loaded, func(i, j int) bool {
		return loaded[i].Name < loaded[j].Name
	})

	rows := make([]dashboardSkill, 0, len(loaded))
	for _, skill := range loaded {
		if skill == nil {
			continue
		}
		state := "Ready"
		tone := "emerald"
		missing := missingDepsSummary(skill.MissingDeps)
		if missing != "" {
			state = "Needs Setup"
			tone = "amber"
		}
		rows = append(rows, dashboardSkill{
			Name:     skill.Name,
			Source:   "workspace",
			Requires: requirementSummary(skill),
			Scope:    defaultString(skill.Description, "skill"),
			State:    state,
			Tone:     tone,
		})
	}

	return rows
}

func (s *Server) buildNodeRows() []dashboardNode {
	rows := make([]dashboardNode, 0, len(s.config.Agents.List))
	for _, agentCfg := range s.config.Agents.List {
		role := agentCfg.Name
		if agentCfg.Description != "" {
			role = agentCfg.Description
		}
		state := "Ready"
		tone := "emerald"
		if agentCfg.Default {
			state = "Default"
			tone = "sky"
		}
		tools := make([]string, 0)
		if agentCfg.Subagents != nil {
			if len(agentCfg.Subagents.AllowTools) > 0 {
				tools = append(tools, agentCfg.Subagents.AllowTools...)
			}
			if len(agentCfg.Subagents.AllowAgents) > 0 {
				tools = append(tools, "agents:"+strings.Join(agentCfg.Subagents.AllowAgents, ","))
			}
		}
		if len(tools) == 0 {
			tools = append(tools, "default-policy")
		}
		rows = append(rows, dashboardNode{
			Name:      agentCfg.ID,
			Role:      role,
			Provider:  defaultString(agentCfg.Provider, "default"),
			Workspace: defaultString(agentCfg.Workspace, s.config.Workspace.Path),
			State:     state,
			Tools:     tools,
			Tone:      tone,
		})
	}
	return rows
}

func (s *Server) buildConfigView() dashboardConfigView {
	groups := []dashboardConfigGroup{
		{
			Title: "Agents",
			Fields: []dashboardConfigField{
				{Label: "defaults.model", Value: s.config.Agents.Defaults.Model},
				{Label: "defaults.max_iterations", Value: fmt.Sprintf("%d", s.config.Agents.Defaults.MaxIterations)},
				{Label: "defaults.max_history_messages", Value: fmt.Sprintf("%d", s.config.Agents.Defaults.MaxHistoryMessages)},
			},
		},
		{
			Title: "Gateway",
			Fields: []dashboardConfigField{
				{Label: "gateway.host", Value: dashboardHTTPHost(s.config)},
				{Label: "gateway.port", Value: fmt.Sprintf("%d", dashboardHTTPPort(s.config))},
				{Label: "gateway.websocket.path", Value: defaultString(s.wsConfig.Path, "/ws")},
			},
		},
		{
			Title: "Tools",
			Fields: []dashboardConfigField{
				{Label: "tools.shell.enabled", Value: fmt.Sprintf("%t", s.config.Tools.Shell.Enabled)},
				{Label: "tools.web.search_engine", Value: s.config.Tools.Web.SearchEngine},
				{Label: "tools.browser.enabled", Value: fmt.Sprintf("%t", s.config.Tools.Browser.Enabled)},
			},
		},
	}

	previewData, _ := yaml.Marshal(map[string]interface{}{
		"workspace": s.config.Workspace,
		"agents": map[string]interface{}{
			"defaults": s.config.Agents.Defaults,
		},
		"gateway": s.config.Gateway,
		"tools":   s.config.Tools,
	})

	return dashboardConfigView{
		Groups:  groups,
		Preview: string(previewData),
	}
}

func (s *Server) buildDebugRows() []dashboardDebugItem {
	return []dashboardDebugItem{
		{
			Title:       "RPC surface",
			Description: "health, config.get, logs.get, sessions.list, channels.list, cron.list",
			State:       "Available",
			Tone:        "sky",
		},
		{
			Title:       "Approvals",
			Description: fmt.Sprintf("approvals.behavior = %s", defaultString(s.config.Approvals.Behavior, "manual")),
			State:       "Configured",
			Tone:        "amber",
		},
		{
			Title:       "WebSocket auth",
			Description: dashboardWSAuthLabel(s.wsConfig),
			State:       map[bool]string{true: "Enabled", false: "Disabled"}[s.wsConfig != nil && s.wsConfig.EnableAuth],
			Tone:        map[bool]string{true: "amber", false: "emerald"}[s.wsConfig != nil && s.wsConfig.EnableAuth],
		},
	}
}

func (s *Server) buildLogRows(limit int) []dashboardLogItem {
	logPath := latestLogFile(s.config)
	if logPath == "" {
		return []dashboardLogItem{}
	}

	lines := tailFileLines(logPath, limit)
	rows := make([]dashboardLogItem, 0, len(lines))
	for _, line := range lines {
		if strings.TrimSpace(line) == "" {
			continue
		}
		row := parseLogLine(line)
		rows = append(rows, row)
	}
	return rows
}

func (s *Server) buildDocRows() []dashboardDocItem {
	return []dashboardDocItem{
		{Title: "README", Path: "README.md", Description: "Project overview and daily entry points."},
		{Title: "Config Guide", Path: "docs/guide/config_guide.md", Description: "Configuration structure and field meanings."},
		{Title: "Subagent", Path: "docs/subagent.md", Description: "Subagent routing, isolation and lifecycle."},
		{Title: "ACP", Path: "docs/acp.md", Description: "ACP runtime and session orchestration details."},
	}
}

func (s *Server) connectionCount() int {
	s.connectionsMu.RLock()
	defer s.connectionsMu.RUnlock()
	return len(s.connections)
}

func (s *Server) bindingForChannel(channelName, accountID string) string {
	for _, binding := range s.config.Bindings {
		if binding.Match.Channel != channelName {
			continue
		}
		if binding.Match.AccountID == "" || binding.Match.AccountID == accountID {
			return fmt.Sprintf("binding -> %s", binding.AgentID)
		}
	}
	return ""
}

func dashboardHTTPHost(cfg *config.Config) string {
	host := cfg.Gateway.Host
	if host == "" || host == "0.0.0.0" {
		return "127.0.0.1"
	}
	return host
}

func dashboardHTTPPort(cfg *config.Config) int {
	if cfg.Gateway.Port == 0 {
		return 8080
	}
	return cfg.Gateway.Port
}

func dashboardWSPort(wsCfg *WebSocketConfig) int {
	if wsCfg == nil || wsCfg.Port == 0 {
		return 28789
	}
	return wsCfg.Port
}

func dashboardWSURL(wsCfg *WebSocketConfig) string {
	if wsCfg == nil {
		return "ws://127.0.0.1:28789/ws"
	}
	host := wsCfg.Host
	if host == "" || host == "0.0.0.0" {
		host = "127.0.0.1"
	}
	path := wsCfg.Path
	if path == "" {
		path = "/ws"
	}
	return fmt.Sprintf("ws://%s:%d%s", host, dashboardWSPort(wsCfg), path)
}

func dashboardWSAuthLabel(wsCfg *WebSocketConfig) string {
	if wsCfg != nil && wsCfg.EnableAuth {
		return "token required"
	}
	return "no auth"
}

func classifySessionState(key string, updatedAt time.Time) (string, string) {
	if strings.Contains(key, ":subagent:") {
		return "Subagent", "amber"
	}
	if strings.Contains(key, ":acp:") {
		return "ACP", "sky"
	}
	if time.Since(updatedAt) < 2*time.Minute {
		return "Hot", "emerald"
	}
	return "Idle", "slate"
}

func extractSessionAgent(key string) string {
	parts := strings.Split(key, ":")
	if len(parts) > 1 {
		return parts[1]
	}
	return "unknown"
}

func extractSessionChannel(key string) string {
	parts := strings.Split(key, ":")
	for i, part := range parts {
		if part == "thread" && i+1 < len(parts) {
			return parts[i+1]
		}
		if part == "acp" {
			return "acp"
		}
	}
	return "workspace"
}

func humanizeSince(t time.Time) string {
	if t.IsZero() {
		return "-"
	}
	delta := time.Since(t)
	switch {
	case delta < time.Minute:
		return fmt.Sprintf("%ds ago", int(delta.Seconds()))
	case delta < time.Hour:
		return fmt.Sprintf("%dm ago", int(delta.Minutes()))
	case delta < 24*time.Hour:
		return fmt.Sprintf("%dh ago", int(delta.Hours()))
	default:
		return t.Format("2006-01-02 15:04")
	}
}

func formatCronSchedule(job *cron.Job) string {
	switch job.Schedule.Type {
	case cron.ScheduleTypeAt:
		return job.Schedule.At.Format("2006-01-02 15:04")
	case cron.ScheduleTypeEvery:
		return job.Schedule.EveryDuration.String()
	case cron.ScheduleTypeCron:
		return job.Schedule.CronExpression
	default:
		return string(job.Schedule.Type)
	}
}

func formatCronDelivery(delivery *cron.Delivery) string {
	if delivery == nil {
		return "none"
	}
	return string(delivery.Mode)
}

func countEnabledCronRows(rows []dashboardCronJob) int {
	count := 0
	for _, row := range rows {
		if row.State == "Enabled" {
			count++
		}
	}
	return count
}

func requirementSummary(skill *agent.Skill) string {
	parts := make([]string, 0)
	if len(skill.Metadata.OpenClaw.Requires.Bins) > 0 {
		parts = append(parts, strings.Join(skill.Metadata.OpenClaw.Requires.Bins, ", "))
	}
	if len(skill.Metadata.OpenClaw.Requires.AnyBins) > 0 {
		parts = append(parts, strings.Join(skill.Metadata.OpenClaw.Requires.AnyBins, ", "))
	}
	if len(skill.Metadata.OpenClaw.Requires.Env) > 0 {
		parts = append(parts, strings.Join(skill.Metadata.OpenClaw.Requires.Env, ", "))
	}
	if len(parts) == 0 {
		return "none"
	}
	return strings.Join(parts, " / ")
}

func missingDepsSummary(missing *agent.MissingDeps) string {
	if missing == nil {
		return ""
	}
	parts := make([]string, 0)
	parts = append(parts, missing.Bins...)
	parts = append(parts, missing.AnyBins...)
	parts = append(parts, missing.Env...)
	parts = append(parts, missing.PythonPkgs...)
	parts = append(parts, missing.NodePkgs...)
	return strings.Join(parts, ", ")
}

func latestLogFile(cfg *config.Config) string {
	candidates := dashboardLogDirs(cfg)
	type entry struct {
		path    string
		modTime time.Time
	}
	files := make([]entry, 0)

	for _, dir := range candidates {
		entries, err := os.ReadDir(dir)
		if err != nil {
			continue
		}
		for _, item := range entries {
			if item.IsDir() || strings.HasSuffix(item.Name(), ".gz") {
				continue
			}
			info, err := item.Info()
			if err != nil {
				continue
			}
			files = append(files, entry{
				path:    filepath.Join(dir, item.Name()),
				modTime: info.ModTime(),
			})
		}
	}

	sort.Slice(files, func(i, j int) bool {
		return files[i].modTime.After(files[j].modTime)
	})

	if len(files) == 0 {
		return ""
	}
	return files[0].path
}

func dashboardLogDirs(cfg *config.Config) []string {
	dirs := make([]string, 0)
	if cfg.Log.Dir != "" {
		dirs = append(dirs, cfg.Log.Dir)
	}
	if cwd, err := os.Getwd(); err == nil {
		dirs = append(dirs, filepath.Join(cwd, "logs"))
	}
	if home, err := os.UserHomeDir(); err == nil {
		dirs = append(dirs, filepath.Join(home, ".sunclaw", "logs"))
		dirs = append(dirs, filepath.Join(home, ".goclaw", "logs"))
	}
	return dirs
}

func tailFileLines(path string, limit int) []string {
	file, err := os.Open(path)
	if err != nil {
		return nil
	}
	defer file.Close()

	lines := make([]string, 0)
	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		lines = append(lines, scanner.Text())
	}

	if len(lines) > limit {
		lines = lines[len(lines)-limit:]
	}
	return lines
}

func parseLogLine(line string) dashboardLogItem {
	type logLine struct {
		Time   string `json:"time"`
		Level  string `json:"level"`
		Caller string `json:"caller"`
		Msg    string `json:"msg"`
	}

	var parsed logLine
	if err := json.Unmarshal([]byte(line), &parsed); err == nil && parsed.Msg != "" {
		level := strings.ToUpper(parsed.Level)
		return dashboardLogItem{
			Time:    parsed.Time,
			Level:   level,
			Origin:  parsed.Caller,
			Message: parsed.Msg,
			Tone:    toneForLogLevel(level),
		}
	}

	return dashboardLogItem{
		Time:    time.Now().Format("15:04:05"),
		Level:   "INFO",
		Origin:  "runtime",
		Message: line,
		Tone:    "slate",
	}
}

func toneForLogLevel(level string) string {
	switch strings.ToUpper(level) {
	case "ERROR":
		return "rose"
	case "WARN":
		return "amber"
	case "INFO":
		return "sky"
	default:
		return "slate"
	}
}

func defaultString(value, fallback string) string {
	if strings.TrimSpace(value) == "" {
		return fallback
	}
	return value
}
