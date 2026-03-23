package gateway

import (
	"testing"

	"github.com/smallnest/goclaw/internal/core/config"
)

func TestControlChannelFromAccountMapsWeWorkSecretAlias(t *testing.T) {
	row := controlChannelFromAccount("wework", "corp-a", config.ChannelAccountConfig{
		Enabled:   true,
		AppSecret: "secret-value",
	})

	if row.Secret != "secret-value" {
		t.Fatalf("secret = %q, want %q", row.Secret, "secret-value")
	}
	if row.AppSecret != "secret-value" {
		t.Fatalf("appSecret = %q, want %q", row.AppSecret, "secret-value")
	}
}

func TestApplyControlChannelConfigsAcceptsWeWorkAppSecretAlias(t *testing.T) {
	cfg := &config.Config{}

	err := applyControlChannelConfigs(cfg, []controlChannelConfig{
		{
			Channel:   "wework",
			AccountID: "corp-a",
			Enabled:   true,
			Mode:      "webhook",
			CorpID:    "corp-id",
			AgentID:   "agent-id",
			AppSecret: "secret-value",
		},
	})
	if err != nil {
		t.Fatalf("applyControlChannelConfigs returned error: %v", err)
	}

	account, ok := cfg.Channels.WeWork.Accounts["corp-a"]
	if !ok {
		t.Fatal("expected wework account to be created")
	}
	if account.AppSecret != "secret-value" {
		t.Fatalf("appSecret = %q, want %q", account.AppSecret, "secret-value")
	}
	if !cfg.Channels.WeWork.Enabled {
		t.Fatal("expected wework channel to be marked enabled")
	}
}

func TestApplyControlChannelConfigsRejectsMissingMultiAccountID(t *testing.T) {
	cfg := &config.Config{}

	err := applyControlChannelConfigs(cfg, []controlChannelConfig{
		{
			Channel: "telegram",
			Enabled: true,
			Token:   "token",
		},
	})
	if err == nil {
		t.Fatal("expected missing account ID error")
	}
}

func TestApplyControlChannelConfigsSupportsWeixinBridge(t *testing.T) {
	cfg := &config.Config{}

	err := applyControlChannelConfigs(cfg, []controlChannelConfig{
		{
			Channel:   "weixin",
			AccountID: "wx-1",
			Enabled:   true,
			BridgeURL: "https://weixin-bridge.example.com",
		},
	})
	if err != nil {
		t.Fatalf("applyControlChannelConfigs returned error: %v", err)
	}

	account, ok := cfg.Channels.Weixin.Accounts["wx-1"]
	if !ok {
		t.Fatal("expected weixin account to be created")
	}
	if account.BridgeURL != "https://weixin-bridge.example.com" {
		t.Fatalf("bridge_url = %q", account.BridgeURL)
	}
	if !cfg.Channels.Weixin.Enabled {
		t.Fatal("expected weixin channel to be marked enabled")
	}
}

func TestApplyControlChannelConfigsSupportsWeixinDirect(t *testing.T) {
	cfg := &config.Config{}

	err := applyControlChannelConfigs(cfg, []controlChannelConfig{
		{
			Channel:    "weixin",
			AccountID:  "wx-direct",
			Enabled:    true,
			Mode:       "direct",
			Token:      "bot-token",
			BaseURL:    "https://ilinkai.weixin.qq.com/",
			CDNBaseURL: "https://novac2c.cdn.weixin.qq.com/c2c",
			Proxy:      "http://127.0.0.1:7890",
		},
	})
	if err != nil {
		t.Fatalf("applyControlChannelConfigs returned error: %v", err)
	}

	account, ok := cfg.Channels.Weixin.Accounts["wx-direct"]
	if !ok {
		t.Fatal("expected weixin account to be created")
	}
	if account.Mode != "direct" {
		t.Fatalf("mode = %q, want direct", account.Mode)
	}
	if account.Token != "bot-token" {
		t.Fatalf("token = %q, want bot-token", account.Token)
	}
	if account.BaseURL != "https://ilinkai.weixin.qq.com/" {
		t.Fatalf("base_url = %q", account.BaseURL)
	}
	if account.CDNBaseURL != "https://novac2c.cdn.weixin.qq.com/c2c" {
		t.Fatalf("cdn_base_url = %q", account.CDNBaseURL)
	}
	if account.Proxy != "http://127.0.0.1:7890" {
		t.Fatalf("proxy = %q", account.Proxy)
	}
}
