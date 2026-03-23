package gateway

import (
	"encoding/json"
	"fmt"
	"net/http"
	"sort"
	"strings"
	"time"

	"github.com/smallnest/goclaw/internal/core/config"
)

type controlConfigPayload struct {
	GeneratedAt string                 `json:"generatedAt"`
	Gateway     controlGatewayConfig   `json:"gateway"`
	Channels    []controlChannelConfig `json:"channels"`
	Bindings    []controlBindingConfig `json:"bindings"`
	Agents      []controlAgentOption   `json:"agents"`
}

type controlGatewayConfig struct {
	WebSocketAuthEnabled bool   `json:"webSocketAuthEnabled"`
	WebSocketAuthToken   string `json:"webSocketAuthToken"`
}

type controlChannelConfig struct {
	Channel           string   `json:"channel"`
	AccountID         string   `json:"accountId"`
	Legacy            bool     `json:"legacy"`
	Enabled           bool     `json:"enabled"`
	Name              string   `json:"name"`
	Mode              string   `json:"mode"`
	Token             string   `json:"token"`
	BaseURL           string   `json:"baseUrl"`
	CDNBaseURL        string   `json:"cdnBaseUrl"`
	Proxy             string   `json:"proxy"`
	AppID             string   `json:"appId"`
	AppSecret         string   `json:"appSecret"`
	CorpID            string   `json:"corpId"`
	AgentID           string   `json:"agentId"`
	Secret            string   `json:"secret"`
	BotID             string   `json:"botId"`
	BotSecret         string   `json:"botSecret"`
	ClientID          string   `json:"clientId"`
	ClientSecret      string   `json:"clientSecret"`
	BridgeURL         string   `json:"bridgeUrl"`
	WebhookURL        string   `json:"webhookUrl"`
	WebSocketURL      string   `json:"webSocketUrl"`
	AESKey            string   `json:"aesKey"`
	EncodingAESKey    string   `json:"encodingAESKey"`
	EncryptKey        string   `json:"encryptKey"`
	VerificationToken string   `json:"verificationToken"`
	WebhookPort       int      `json:"webhookPort"`
	ServerURL         string   `json:"serverUrl"`
	AppToken          string   `json:"appToken"`
	Priority          int      `json:"priority"`
	AllowedIDs        []string `json:"allowedIds"`
}

type controlBindingConfig struct {
	Channel   string `json:"channel"`
	AccountID string `json:"accountId"`
	AgentID   string `json:"agentId"`
}

type controlAgentOption struct {
	ID      string `json:"id"`
	Name    string `json:"name"`
	Default bool   `json:"default"`
}

type controlConfigUpdateResult struct {
	Saved       bool   `json:"saved"`
	Applied     bool   `json:"applied"`
	ConfigPath  string `json:"configPath"`
	GeneratedAt string `json:"generatedAt"`
	Message     string `json:"message"`
}

func (s *Server) handleControlConfigAPI(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		s.writeControlConfig(w)
	case http.MethodPut:
		s.updateControlConfig(w, r)
	default:
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
	}
}

func (s *Server) writeControlConfig(w http.ResponseWriter) {
	payload := s.buildControlConfigPayload()
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_ = json.NewEncoder(w).Encode(payload)
}

func (s *Server) updateControlConfig(w http.ResponseWriter, r *http.Request) {
	var payload controlConfigPayload
	if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
		http.Error(w, fmt.Sprintf("invalid control config payload: %v", err), http.StatusBadRequest)
		return
	}

	result, err := s.applyControlConfigPayload(&payload)
	if err != nil {
		http.Error(w, fmt.Sprintf("failed to apply control config: %v", err), http.StatusBadRequest)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_ = json.NewEncoder(w).Encode(result)
}

func (s *Server) buildControlConfigPayload() *controlConfigPayload {
	s.mu.RLock()
	cfg := s.config
	wsCfg := s.wsConfig
	s.mu.RUnlock()

	payload := &controlConfigPayload{
		GeneratedAt: time.Now().Format(time.RFC3339),
		Gateway: controlGatewayConfig{
			WebSocketAuthEnabled: wsCfg != nil && wsCfg.EnableAuth,
			WebSocketAuthToken: defaultString(func() string {
				if wsCfg == nil {
					return ""
				}
				return wsCfg.AuthToken
			}(), ""),
		},
		Channels: buildControlChannelConfigs(cfg),
		Bindings: buildControlBindings(cfg),
		Agents:   buildControlAgentOptions(cfg),
	}

	return payload
}

func (s *Server) applyControlConfigPayload(payload *controlConfigPayload) (*controlConfigUpdateResult, error) {
	s.mu.RLock()
	currentCfg := s.config
	s.mu.RUnlock()

	nextCfg, err := cloneConfig(currentCfg)
	if err != nil {
		return nil, err
	}

	nextCfg.Gateway.WebSocket.EnableAuth = payload.Gateway.WebSocketAuthEnabled
	nextCfg.Gateway.WebSocket.AuthToken = strings.TrimSpace(payload.Gateway.WebSocketAuthToken)

	if err := applyControlChannelConfigs(nextCfg, payload.Channels); err != nil {
		return nil, err
	}

	bindings, err := sanitizeControlBindings(nextCfg, payload.Bindings)
	if err != nil {
		return nil, err
	}
	nextCfg.Bindings = bindings

	if err := config.Validate(nextCfg); err != nil {
		return nil, err
	}

	configPath, err := s.resolveConfigPath()
	if err != nil {
		return nil, err
	}
	if err := config.Save(nextCfg, configPath); err != nil {
		return nil, err
	}

	if err := s.ApplyConfig(nextCfg); err != nil {
		return nil, fmt.Errorf("saved config but runtime apply failed: %w", err)
	}

	return &controlConfigUpdateResult{
		Saved:       true,
		Applied:     true,
		ConfigPath:  configPath,
		GeneratedAt: time.Now().Format(time.RFC3339),
		Message:     "Control config saved and applied.",
	}, nil
}

func (s *Server) resolveConfigPath() (string, error) {
	if path := strings.TrimSpace(s.ConfigPath()); path != "" {
		return path, nil
	}
	return config.GetDefaultConfigPath()
}

func cloneConfig(cfg *config.Config) (*config.Config, error) {
	data, err := json.Marshal(cfg)
	if err != nil {
		return nil, fmt.Errorf("failed to clone config: %w", err)
	}

	var next config.Config
	if err := json.Unmarshal(data, &next); err != nil {
		return nil, fmt.Errorf("failed to decode cloned config: %w", err)
	}

	return &next, nil
}

func buildControlAgentOptions(cfg *config.Config) []controlAgentOption {
	options := make([]controlAgentOption, 0, len(cfg.Agents.List))
	for _, agentCfg := range cfg.Agents.List {
		label := agentCfg.Name
		if label == "" {
			label = agentCfg.ID
		}
		options = append(options, controlAgentOption{
			ID:      agentCfg.ID,
			Name:    label,
			Default: agentCfg.Default,
		})
	}

	sort.Slice(options, func(i, j int) bool {
		if options[i].Default != options[j].Default {
			return options[i].Default
		}
		return options[i].ID < options[j].ID
	})

	return options
}

func buildControlBindings(cfg *config.Config) []controlBindingConfig {
	rows := make([]controlBindingConfig, 0, len(cfg.Bindings))
	for _, binding := range cfg.Bindings {
		rows = append(rows, controlBindingConfig{
			Channel:   binding.Match.Channel,
			AccountID: binding.Match.AccountID,
			AgentID:   binding.AgentID,
		})
	}

	sort.Slice(rows, func(i, j int) bool {
		if rows[i].Channel == rows[j].Channel {
			if rows[i].AccountID == rows[j].AccountID {
				return rows[i].AgentID < rows[j].AgentID
			}
			return rows[i].AccountID < rows[j].AccountID
		}
		return rows[i].Channel < rows[j].Channel
	})

	return rows
}

func buildControlChannelConfigs(cfg *config.Config) []controlChannelConfig {
	rows := make([]controlChannelConfig, 0)

	appendAccountRows := func(channel string, accounts map[string]config.ChannelAccountConfig) {
		accountIDs := make([]string, 0, len(accounts))
		for accountID := range accounts {
			accountIDs = append(accountIDs, accountID)
		}
		sort.Strings(accountIDs)

		for _, accountID := range accountIDs {
			rows = append(rows, controlChannelFromAccount(channel, accountID, accounts[accountID]))
		}
	}

	if len(cfg.Channels.Telegram.Accounts) > 0 {
		appendAccountRows("telegram", cfg.Channels.Telegram.Accounts)
	} else if cfg.Channels.Telegram.Enabled || cfg.Channels.Telegram.Token != "" || len(cfg.Channels.Telegram.AllowedIDs) > 0 {
		rows = append(rows, controlChannelConfig{
			Channel:    "telegram",
			AccountID:  "default",
			Legacy:     true,
			Enabled:    cfg.Channels.Telegram.Enabled,
			Token:      cfg.Channels.Telegram.Token,
			AllowedIDs: cloneStrings(cfg.Channels.Telegram.AllowedIDs),
		})
	}

	if len(cfg.Channels.WhatsApp.Accounts) > 0 {
		appendAccountRows("whatsapp", cfg.Channels.WhatsApp.Accounts)
	} else if cfg.Channels.WhatsApp.Enabled || cfg.Channels.WhatsApp.BridgeURL != "" || len(cfg.Channels.WhatsApp.AllowedIDs) > 0 {
		rows = append(rows, controlChannelConfig{
			Channel:    "whatsapp",
			AccountID:  "default",
			Legacy:     true,
			Enabled:    cfg.Channels.WhatsApp.Enabled,
			BridgeURL:  cfg.Channels.WhatsApp.BridgeURL,
			AllowedIDs: cloneStrings(cfg.Channels.WhatsApp.AllowedIDs),
		})
	}

	if len(cfg.Channels.Weixin.Accounts) > 0 {
		appendAccountRows("weixin", cfg.Channels.Weixin.Accounts)
	} else if cfg.Channels.Weixin.Enabled || cfg.Channels.Weixin.BridgeURL != "" || cfg.Channels.Weixin.Token != "" || len(cfg.Channels.Weixin.AllowedIDs) > 0 {
		rows = append(rows, controlChannelConfig{
			Channel:    "weixin",
			AccountID:  "default",
			Legacy:     true,
			Enabled:    cfg.Channels.Weixin.Enabled,
			Mode:       cfg.Channels.Weixin.Mode,
			Token:      cfg.Channels.Weixin.Token,
			BaseURL:    cfg.Channels.Weixin.BaseURL,
			CDNBaseURL: cfg.Channels.Weixin.CDNBaseURL,
			Proxy:      cfg.Channels.Weixin.Proxy,
			BridgeURL:  cfg.Channels.Weixin.BridgeURL,
			AllowedIDs: cloneStrings(cfg.Channels.Weixin.AllowedIDs),
		})
	}

	if len(cfg.Channels.IMessage.Accounts) > 0 {
		appendAccountRows("imessage", cfg.Channels.IMessage.Accounts)
	} else if cfg.Channels.IMessage.Enabled || cfg.Channels.IMessage.BridgeURL != "" || len(cfg.Channels.IMessage.AllowedIDs) > 0 {
		rows = append(rows, controlChannelConfig{
			Channel:    "imessage",
			AccountID:  "default",
			Legacy:     true,
			Enabled:    cfg.Channels.IMessage.Enabled,
			BridgeURL:  cfg.Channels.IMessage.BridgeURL,
			AllowedIDs: cloneStrings(cfg.Channels.IMessage.AllowedIDs),
		})
	}

	if len(cfg.Channels.Feishu.Accounts) > 0 {
		appendAccountRows("feishu", cfg.Channels.Feishu.Accounts)
	} else if cfg.Channels.Feishu.Enabled || cfg.Channels.Feishu.AppID != "" || cfg.Channels.Feishu.AppSecret != "" {
		rows = append(rows, controlChannelConfig{
			Channel:           "feishu",
			AccountID:         "default",
			Legacy:            true,
			Enabled:           cfg.Channels.Feishu.Enabled,
			AppID:             cfg.Channels.Feishu.AppID,
			AppSecret:         cfg.Channels.Feishu.AppSecret,
			EncryptKey:        cfg.Channels.Feishu.EncryptKey,
			VerificationToken: cfg.Channels.Feishu.VerificationToken,
			WebhookPort:       cfg.Channels.Feishu.WebhookPort,
			AllowedIDs:        cloneStrings(cfg.Channels.Feishu.AllowedIDs),
		})
	}

	if len(cfg.Channels.QQ.Accounts) > 0 {
		appendAccountRows("qq", cfg.Channels.QQ.Accounts)
	} else if cfg.Channels.QQ.Enabled || cfg.Channels.QQ.AppID != "" || cfg.Channels.QQ.AppSecret != "" {
		rows = append(rows, controlChannelConfig{
			Channel:    "qq",
			AccountID:  "default",
			Legacy:     true,
			Enabled:    cfg.Channels.QQ.Enabled,
			AppID:      cfg.Channels.QQ.AppID,
			AppSecret:  cfg.Channels.QQ.AppSecret,
			AllowedIDs: cloneStrings(cfg.Channels.QQ.AllowedIDs),
		})
	}

	if len(cfg.Channels.WeWork.Accounts) > 0 {
		appendAccountRows("wework", cfg.Channels.WeWork.Accounts)
	} else if cfg.Channels.WeWork.Enabled || cfg.Channels.WeWork.CorpID != "" || cfg.Channels.WeWork.BotID != "" {
		rows = append(rows, controlChannelConfig{
			Channel:        "wework",
			AccountID:      "default",
			Legacy:         true,
			Enabled:        cfg.Channels.WeWork.Enabled,
			Mode:           cfg.Channels.WeWork.Mode,
			CorpID:         cfg.Channels.WeWork.CorpID,
			AgentID:        cfg.Channels.WeWork.AgentID,
			Secret:         cfg.Channels.WeWork.Secret,
			BotID:          cfg.Channels.WeWork.BotID,
			BotSecret:      cfg.Channels.WeWork.BotSecret,
			WebSocketURL:   cfg.Channels.WeWork.WebSocketURL,
			Token:          cfg.Channels.WeWork.Token,
			EncodingAESKey: cfg.Channels.WeWork.EncodingAESKey,
			WebhookPort:    cfg.Channels.WeWork.WebhookPort,
			AllowedIDs:     cloneStrings(cfg.Channels.WeWork.AllowedIDs),
		})
	}

	if len(cfg.Channels.DingTalk.Accounts) > 0 {
		appendAccountRows("dingtalk", cfg.Channels.DingTalk.Accounts)
	} else if cfg.Channels.DingTalk.Enabled || cfg.Channels.DingTalk.ClientID != "" || cfg.Channels.DingTalk.ClientSecret != "" {
		rows = append(rows, controlChannelConfig{
			Channel:      "dingtalk",
			AccountID:    "default",
			Legacy:       true,
			Enabled:      cfg.Channels.DingTalk.Enabled,
			ClientID:     cfg.Channels.DingTalk.ClientID,
			ClientSecret: cfg.Channels.DingTalk.ClientSecret,
			AllowedIDs:   cloneStrings(cfg.Channels.DingTalk.AllowedIDs),
		})
	}

	if len(cfg.Channels.Infoflow.Accounts) > 0 {
		appendAccountRows("infoflow", cfg.Channels.Infoflow.Accounts)
	} else if cfg.Channels.Infoflow.Enabled || cfg.Channels.Infoflow.WebhookURL != "" || cfg.Channels.Infoflow.Token != "" {
		rows = append(rows, controlChannelConfig{
			Channel:     "infoflow",
			AccountID:   "default",
			Legacy:      true,
			Enabled:     cfg.Channels.Infoflow.Enabled,
			WebhookURL:  cfg.Channels.Infoflow.WebhookURL,
			Token:       cfg.Channels.Infoflow.Token,
			AESKey:      cfg.Channels.Infoflow.AESKey,
			WebhookPort: cfg.Channels.Infoflow.WebhookPort,
			AllowedIDs:  cloneStrings(cfg.Channels.Infoflow.AllowedIDs),
		})
	}

	if len(cfg.Channels.Gotify.Accounts) > 0 {
		appendAccountRows("gotify", cfg.Channels.Gotify.Accounts)
	} else if cfg.Channels.Gotify.Enabled || cfg.Channels.Gotify.ServerURL != "" || cfg.Channels.Gotify.AppToken != "" {
		rows = append(rows, controlChannelConfig{
			Channel:    "gotify",
			AccountID:  "default",
			Legacy:     true,
			Enabled:    cfg.Channels.Gotify.Enabled,
			ServerURL:  cfg.Channels.Gotify.ServerURL,
			AppToken:   cfg.Channels.Gotify.AppToken,
			Priority:   cfg.Channels.Gotify.Priority,
			AllowedIDs: cloneStrings(cfg.Channels.Gotify.AllowedIDs),
		})
	}

	sort.Slice(rows, func(i, j int) bool {
		if rows[i].Channel == rows[j].Channel {
			return rows[i].AccountID < rows[j].AccountID
		}
		return rows[i].Channel < rows[j].Channel
	})

	return rows
}

func controlChannelFromAccount(channel, accountID string, cfg config.ChannelAccountConfig) controlChannelConfig {
	row := controlChannelConfig{
		Channel:           channel,
		AccountID:         accountID,
		Legacy:            false,
		Enabled:           cfg.Enabled,
		Name:              cfg.Name,
		Mode:              cfg.Mode,
		Token:             cfg.Token,
		BaseURL:           cfg.BaseURL,
		CDNBaseURL:        cfg.CDNBaseURL,
		Proxy:             cfg.Proxy,
		AppID:             cfg.AppID,
		AppSecret:         cfg.AppSecret,
		CorpID:            cfg.CorpID,
		AgentID:           cfg.AgentID,
		BotID:             cfg.BotID,
		BotSecret:         cfg.BotSecret,
		ClientID:          cfg.ClientID,
		ClientSecret:      cfg.ClientSecret,
		BridgeURL:         cfg.BridgeURL,
		WebhookURL:        cfg.WebhookURL,
		WebSocketURL:      cfg.WebSocketURL,
		AESKey:            cfg.AESKey,
		EncodingAESKey:    cfg.EncodingAESKey,
		EncryptKey:        cfg.EncryptKey,
		VerificationToken: cfg.VerificationToken,
		WebhookPort:       cfg.WebhookPort,
		ServerURL:         cfg.ServerURL,
		AppToken:          cfg.AppToken,
		Priority:          cfg.Priority,
		AllowedIDs:        cloneStrings(cfg.AllowedIDs),
	}

	if channel == "wework" {
		row.Secret = cfg.AppSecret
	}

	return row
}

func normalizeControlChannelRow(row *controlChannelConfig) {
	row.Channel = strings.TrimSpace(strings.ToLower(row.Channel))
	row.AccountID = strings.TrimSpace(row.AccountID)
	row.Name = strings.TrimSpace(row.Name)
	row.Mode = strings.TrimSpace(strings.ToLower(row.Mode))
	row.Token = strings.TrimSpace(row.Token)
	row.BaseURL = strings.TrimSpace(row.BaseURL)
	row.CDNBaseURL = strings.TrimSpace(row.CDNBaseURL)
	row.Proxy = strings.TrimSpace(row.Proxy)
	row.AppID = strings.TrimSpace(row.AppID)
	row.AppSecret = strings.TrimSpace(row.AppSecret)
	row.CorpID = strings.TrimSpace(row.CorpID)
	row.AgentID = strings.TrimSpace(row.AgentID)
	row.Secret = strings.TrimSpace(row.Secret)
	row.BotID = strings.TrimSpace(row.BotID)
	row.BotSecret = strings.TrimSpace(row.BotSecret)
	row.ClientID = strings.TrimSpace(row.ClientID)
	row.ClientSecret = strings.TrimSpace(row.ClientSecret)
	row.BridgeURL = strings.TrimSpace(row.BridgeURL)
	row.WebhookURL = strings.TrimSpace(row.WebhookURL)
	row.WebSocketURL = strings.TrimSpace(row.WebSocketURL)
	row.AESKey = strings.TrimSpace(row.AESKey)
	row.EncodingAESKey = strings.TrimSpace(row.EncodingAESKey)
	row.EncryptKey = strings.TrimSpace(row.EncryptKey)
	row.VerificationToken = strings.TrimSpace(row.VerificationToken)
	row.ServerURL = strings.TrimSpace(row.ServerURL)
	row.AppToken = strings.TrimSpace(row.AppToken)
	row.AllowedIDs = sanitizeStringList(row.AllowedIDs)

	if row.Legacy && row.AccountID == "" {
		row.AccountID = "default"
	}

	if row.Channel == "wework" {
		row.Secret = defaultString(row.Secret, row.AppSecret)
		row.AppSecret = defaultString(row.AppSecret, row.Secret)
	}
}

func applyControlChannelConfigs(cfg *config.Config, rows []controlChannelConfig) error {
	for _, row := range rows {
		normalizeControlChannelRow(&row)
		if row.Channel == "" {
			return fmt.Errorf("channel type is required")
		}
		if !row.Legacy && row.AccountID == "" {
			return fmt.Errorf("account ID is required for %s multi-account config", row.Channel)
		}

		switch row.Channel {
		case "telegram":
			if row.Legacy {
				cfg.Channels.Telegram.Enabled = row.Enabled
				cfg.Channels.Telegram.Token = row.Token
				cfg.Channels.Telegram.AllowedIDs = row.AllowedIDs
			} else {
				if cfg.Channels.Telegram.Accounts == nil {
					cfg.Channels.Telegram.Accounts = make(map[string]config.ChannelAccountConfig)
				}
				account := cfg.Channels.Telegram.Accounts[row.AccountID]
				account.Enabled = row.Enabled
				account.Name = row.Name
				account.Mode = row.Mode
				account.Token = row.Token
				account.AllowedIDs = row.AllowedIDs
				cfg.Channels.Telegram.Accounts[row.AccountID] = account
			}
		case "whatsapp":
			if row.Legacy {
				cfg.Channels.WhatsApp.Enabled = row.Enabled
				cfg.Channels.WhatsApp.BridgeURL = row.BridgeURL
				cfg.Channels.WhatsApp.AllowedIDs = row.AllowedIDs
			} else {
				ensureChannelAccountMap(&cfg.Channels.WhatsApp.Accounts)
				account := cfg.Channels.WhatsApp.Accounts[row.AccountID]
				account.Enabled = row.Enabled
				account.Name = row.Name
				account.Mode = row.Mode
				account.BridgeURL = row.BridgeURL
				account.AllowedIDs = row.AllowedIDs
				cfg.Channels.WhatsApp.Accounts[row.AccountID] = account
			}
		case "weixin":
			if row.Legacy {
				cfg.Channels.Weixin.Enabled = row.Enabled
				cfg.Channels.Weixin.Mode = row.Mode
				cfg.Channels.Weixin.Token = row.Token
				cfg.Channels.Weixin.BaseURL = row.BaseURL
				cfg.Channels.Weixin.CDNBaseURL = row.CDNBaseURL
				cfg.Channels.Weixin.Proxy = row.Proxy
				cfg.Channels.Weixin.BridgeURL = row.BridgeURL
				cfg.Channels.Weixin.AllowedIDs = row.AllowedIDs
			} else {
				ensureChannelAccountMap(&cfg.Channels.Weixin.Accounts)
				account := cfg.Channels.Weixin.Accounts[row.AccountID]
				account.Enabled = row.Enabled
				account.Name = row.Name
				account.Mode = row.Mode
				account.Token = row.Token
				account.BaseURL = row.BaseURL
				account.CDNBaseURL = row.CDNBaseURL
				account.Proxy = row.Proxy
				account.BridgeURL = row.BridgeURL
				account.AllowedIDs = row.AllowedIDs
				cfg.Channels.Weixin.Accounts[row.AccountID] = account
			}
		case "imessage":
			if row.Legacy {
				cfg.Channels.IMessage.Enabled = row.Enabled
				cfg.Channels.IMessage.BridgeURL = row.BridgeURL
				cfg.Channels.IMessage.AllowedIDs = row.AllowedIDs
			} else {
				ensureChannelAccountMap(&cfg.Channels.IMessage.Accounts)
				account := cfg.Channels.IMessage.Accounts[row.AccountID]
				account.Enabled = row.Enabled
				account.Name = row.Name
				account.Mode = row.Mode
				account.BridgeURL = row.BridgeURL
				account.AllowedIDs = row.AllowedIDs
				cfg.Channels.IMessage.Accounts[row.AccountID] = account
			}
		case "feishu":
			if row.Legacy {
				cfg.Channels.Feishu.Enabled = row.Enabled
				cfg.Channels.Feishu.AppID = row.AppID
				cfg.Channels.Feishu.AppSecret = row.AppSecret
				cfg.Channels.Feishu.EncryptKey = row.EncryptKey
				cfg.Channels.Feishu.VerificationToken = row.VerificationToken
				cfg.Channels.Feishu.WebhookPort = row.WebhookPort
				cfg.Channels.Feishu.AllowedIDs = row.AllowedIDs
			} else {
				ensureChannelAccountMap(&cfg.Channels.Feishu.Accounts)
				account := cfg.Channels.Feishu.Accounts[row.AccountID]
				account.Enabled = row.Enabled
				account.Name = row.Name
				account.Mode = row.Mode
				account.AppID = row.AppID
				account.AppSecret = row.AppSecret
				account.EncryptKey = row.EncryptKey
				account.VerificationToken = row.VerificationToken
				account.WebhookPort = row.WebhookPort
				account.AllowedIDs = row.AllowedIDs
				cfg.Channels.Feishu.Accounts[row.AccountID] = account
			}
		case "qq":
			if row.Legacy {
				cfg.Channels.QQ.Enabled = row.Enabled
				cfg.Channels.QQ.AppID = row.AppID
				cfg.Channels.QQ.AppSecret = row.AppSecret
				cfg.Channels.QQ.AllowedIDs = row.AllowedIDs
			} else {
				ensureChannelAccountMap(&cfg.Channels.QQ.Accounts)
				account := cfg.Channels.QQ.Accounts[row.AccountID]
				account.Enabled = row.Enabled
				account.Name = row.Name
				account.Mode = row.Mode
				account.AppID = row.AppID
				account.AppSecret = row.AppSecret
				account.AllowedIDs = row.AllowedIDs
				cfg.Channels.QQ.Accounts[row.AccountID] = account
			}
		case "wework":
			if row.Legacy {
				cfg.Channels.WeWork.Enabled = row.Enabled
				cfg.Channels.WeWork.Mode = row.Mode
				cfg.Channels.WeWork.CorpID = row.CorpID
				cfg.Channels.WeWork.AgentID = row.AgentID
				cfg.Channels.WeWork.Secret = row.Secret
				cfg.Channels.WeWork.BotID = row.BotID
				cfg.Channels.WeWork.BotSecret = row.BotSecret
				cfg.Channels.WeWork.WebSocketURL = row.WebSocketURL
				cfg.Channels.WeWork.Token = row.Token
				cfg.Channels.WeWork.EncodingAESKey = row.EncodingAESKey
				cfg.Channels.WeWork.WebhookPort = row.WebhookPort
				cfg.Channels.WeWork.AllowedIDs = row.AllowedIDs
			} else {
				ensureChannelAccountMap(&cfg.Channels.WeWork.Accounts)
				account := cfg.Channels.WeWork.Accounts[row.AccountID]
				account.Enabled = row.Enabled
				account.Name = row.Name
				account.Mode = row.Mode
				account.CorpID = row.CorpID
				account.AgentID = row.AgentID
				account.AppSecret = row.Secret
				account.BotID = row.BotID
				account.BotSecret = row.BotSecret
				account.WebSocketURL = row.WebSocketURL
				account.Token = row.Token
				account.EncodingAESKey = row.EncodingAESKey
				account.WebhookPort = row.WebhookPort
				account.AllowedIDs = row.AllowedIDs
				cfg.Channels.WeWork.Accounts[row.AccountID] = account
			}
		case "dingtalk":
			if row.Legacy {
				cfg.Channels.DingTalk.Enabled = row.Enabled
				cfg.Channels.DingTalk.ClientID = row.ClientID
				cfg.Channels.DingTalk.ClientSecret = row.ClientSecret
				cfg.Channels.DingTalk.AllowedIDs = row.AllowedIDs
			} else {
				ensureChannelAccountMap(&cfg.Channels.DingTalk.Accounts)
				account := cfg.Channels.DingTalk.Accounts[row.AccountID]
				account.Enabled = row.Enabled
				account.Name = row.Name
				account.Mode = row.Mode
				account.ClientID = row.ClientID
				account.ClientSecret = row.ClientSecret
				account.AllowedIDs = row.AllowedIDs
				cfg.Channels.DingTalk.Accounts[row.AccountID] = account
			}
		case "infoflow":
			if row.Legacy {
				cfg.Channels.Infoflow.Enabled = row.Enabled
				cfg.Channels.Infoflow.WebhookURL = row.WebhookURL
				cfg.Channels.Infoflow.Token = row.Token
				cfg.Channels.Infoflow.AESKey = row.AESKey
				cfg.Channels.Infoflow.WebhookPort = row.WebhookPort
				cfg.Channels.Infoflow.AllowedIDs = row.AllowedIDs
			} else {
				ensureChannelAccountMap(&cfg.Channels.Infoflow.Accounts)
				account := cfg.Channels.Infoflow.Accounts[row.AccountID]
				account.Enabled = row.Enabled
				account.Name = row.Name
				account.Mode = row.Mode
				account.WebhookURL = row.WebhookURL
				account.Token = row.Token
				account.AESKey = row.AESKey
				account.WebhookPort = row.WebhookPort
				account.AllowedIDs = row.AllowedIDs
				cfg.Channels.Infoflow.Accounts[row.AccountID] = account
			}
		case "gotify":
			if row.Legacy {
				cfg.Channels.Gotify.Enabled = row.Enabled
				cfg.Channels.Gotify.ServerURL = row.ServerURL
				cfg.Channels.Gotify.AppToken = row.AppToken
				cfg.Channels.Gotify.Priority = row.Priority
				cfg.Channels.Gotify.AllowedIDs = row.AllowedIDs
			} else {
				ensureChannelAccountMap(&cfg.Channels.Gotify.Accounts)
				account := cfg.Channels.Gotify.Accounts[row.AccountID]
				account.Enabled = row.Enabled
				account.Name = row.Name
				account.Mode = row.Mode
				account.ServerURL = row.ServerURL
				account.AppToken = row.AppToken
				account.Priority = row.Priority
				account.AllowedIDs = row.AllowedIDs
				cfg.Channels.Gotify.Accounts[row.AccountID] = account
			}
		default:
			return fmt.Errorf("unsupported channel type: %s", row.Channel)
		}
	}

	normalizeChannelEnabledFlags(cfg)
	return nil
}

func normalizeChannelEnabledFlags(cfg *config.Config) {
	if len(cfg.Channels.Telegram.Accounts) > 0 {
		cfg.Channels.Telegram.Enabled = anyEnabledAccount(cfg.Channels.Telegram.Accounts)
	}
	if len(cfg.Channels.WhatsApp.Accounts) > 0 {
		cfg.Channels.WhatsApp.Enabled = anyEnabledAccount(cfg.Channels.WhatsApp.Accounts)
	}
	if len(cfg.Channels.Weixin.Accounts) > 0 {
		cfg.Channels.Weixin.Enabled = anyEnabledAccount(cfg.Channels.Weixin.Accounts)
	}
	if len(cfg.Channels.IMessage.Accounts) > 0 {
		cfg.Channels.IMessage.Enabled = anyEnabledAccount(cfg.Channels.IMessage.Accounts)
	}
	if len(cfg.Channels.Feishu.Accounts) > 0 {
		cfg.Channels.Feishu.Enabled = anyEnabledAccount(cfg.Channels.Feishu.Accounts)
	}
	if len(cfg.Channels.QQ.Accounts) > 0 {
		cfg.Channels.QQ.Enabled = anyEnabledAccount(cfg.Channels.QQ.Accounts)
	}
	if len(cfg.Channels.WeWork.Accounts) > 0 {
		cfg.Channels.WeWork.Enabled = anyEnabledAccount(cfg.Channels.WeWork.Accounts)
	}
	if len(cfg.Channels.DingTalk.Accounts) > 0 {
		cfg.Channels.DingTalk.Enabled = anyEnabledAccount(cfg.Channels.DingTalk.Accounts)
	}
	if len(cfg.Channels.Infoflow.Accounts) > 0 {
		cfg.Channels.Infoflow.Enabled = anyEnabledAccount(cfg.Channels.Infoflow.Accounts)
	}
	if len(cfg.Channels.Gotify.Accounts) > 0 {
		cfg.Channels.Gotify.Enabled = anyEnabledAccount(cfg.Channels.Gotify.Accounts)
	}
}

func sanitizeControlBindings(cfg *config.Config, rows []controlBindingConfig) ([]config.BindingConfig, error) {
	knownAgents := make(map[string]struct{}, len(cfg.Agents.List))
	for _, agentCfg := range cfg.Agents.List {
		knownAgents[agentCfg.ID] = struct{}{}
	}

	bindings := make([]config.BindingConfig, 0, len(rows))
	seen := make(map[string]struct{}, len(rows))
	for _, row := range rows {
		channel := strings.TrimSpace(strings.ToLower(row.Channel))
		accountID := strings.TrimSpace(row.AccountID)
		agentID := strings.TrimSpace(row.AgentID)

		if channel == "" || agentID == "" {
			continue
		}
		if _, ok := knownAgents[agentID]; !ok {
			return nil, fmt.Errorf("binding references unknown agent %q", agentID)
		}

		key := channel + "::" + accountID
		if _, exists := seen[key]; exists {
			return nil, fmt.Errorf("duplicate binding for %s / %s", channel, defaultString(accountID, "default"))
		}
		seen[key] = struct{}{}

		bindings = append(bindings, config.BindingConfig{
			AgentID: agentID,
			Match: config.BindingMatch{
				Channel:   channel,
				AccountID: accountID,
			},
		})
	}

	sort.Slice(bindings, func(i, j int) bool {
		if bindings[i].Match.Channel == bindings[j].Match.Channel {
			return bindings[i].Match.AccountID < bindings[j].Match.AccountID
		}
		return bindings[i].Match.Channel < bindings[j].Match.Channel
	})

	return bindings, nil
}

func anyEnabledAccount(accounts map[string]config.ChannelAccountConfig) bool {
	for _, account := range accounts {
		if account.Enabled {
			return true
		}
	}
	return false
}

func ensureChannelAccountMap(target *map[string]config.ChannelAccountConfig) {
	if *target == nil {
		*target = make(map[string]config.ChannelAccountConfig)
	}
}

func sanitizeStringList(values []string) []string {
	cleaned := make([]string, 0, len(values))
	for _, value := range values {
		trimmed := strings.TrimSpace(value)
		if trimmed == "" {
			continue
		}
		cleaned = append(cleaned, trimmed)
	}
	return cleaned
}

func cloneStrings(values []string) []string {
	if len(values) == 0 {
		return []string{}
	}

	cloned := make([]string, len(values))
	copy(cloned, values)
	return cloned
}
