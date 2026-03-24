package config

import "testing"

func TestResolveReplyDeliveryPrefersAccountOverride(t *testing.T) {
	cfg := &Config{
		ReplyDelivery: ReplyDeliveryConfig{
			Mode:          ReplyDeliveryModeSingle,
			MinChunkChars: DefaultReplyDeliveryMinChunkChars,
			MaxChunkChars: DefaultReplyDeliveryMaxChunkChars,
			MinDelayMs:    DefaultReplyDeliveryMinDelayMs,
			MaxDelayMs:    DefaultReplyDeliveryMaxDelayMs,
			MaxPushCount:  DefaultReplyDeliveryMaxPushCount,
		},
		Channels: ChannelsConfig{
			Weixin: WeixinChannelConfig{
				ReplyDelivery: ReplyDeliveryConfig{
					Mode:         ReplyDeliveryModeMultiPush,
					MaxPushCount: 3,
				},
				Accounts: map[string]ChannelAccountConfig{
					"bot1": {
						ReplyDelivery: ReplyDeliveryConfig{
							Mode:          ReplyDeliveryModeHybrid,
							MinChunkChars: 12,
						},
					},
				},
			},
		},
	}

	resolved := cfg.ResolveReplyDelivery("weixin", "bot1")
	if resolved.Mode != ReplyDeliveryModeHybrid {
		t.Fatalf("mode = %q, want %q", resolved.Mode, ReplyDeliveryModeHybrid)
	}
	if resolved.MinChunkChars != 12 {
		t.Fatalf("min_chunk_chars = %d, want 12", resolved.MinChunkChars)
	}
	if resolved.MaxPushCount != 3 {
		t.Fatalf("max_push_count = %d, want 3", resolved.MaxPushCount)
	}
	if resolved.MaxChunkChars != DefaultReplyDeliveryMaxChunkChars || resolved.MinDelayMs != DefaultReplyDeliveryMinDelayMs || resolved.MaxDelayMs != DefaultReplyDeliveryMaxDelayMs {
		t.Fatalf("unexpected inherited defaults: %+v", resolved)
	}
}

func TestDefaultReplyDeliveryConfigIsLessFragmented(t *testing.T) {
	defaults := DefaultReplyDeliveryConfig()

	if defaults.MinChunkChars != 48 {
		t.Fatalf("min_chunk_chars = %d, want 48", defaults.MinChunkChars)
	}
	if defaults.MaxChunkChars != 160 {
		t.Fatalf("max_chunk_chars = %d, want 160", defaults.MaxChunkChars)
	}
	if defaults.MaxPushCount != 3 {
		t.Fatalf("max_push_count = %d, want 3", defaults.MaxPushCount)
	}
}

func TestValidateReplyDeliveryConfigRejectsInvalidRange(t *testing.T) {
	if err := validateReplyDeliveryConfig("reply_delivery", ReplyDeliveryConfig{
		Mode:          ReplyDeliveryModeMultiPush,
		MinChunkChars: 50,
		MaxChunkChars: 10,
	}); err == nil {
		t.Fatalf("expected invalid chunk range error")
	}
}
