package gateway

import (
	"testing"

	"github.com/smallnest/goclaw/config"
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
