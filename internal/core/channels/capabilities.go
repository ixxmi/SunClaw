package channels

import "github.com/smallnest/goclaw/internal/core/channels/shared"

type CapabilityType = shared.CapabilityType

const (
	CapabilityReactions      CapabilityType = shared.CapabilityReactions
	CapabilityInlineButtons  CapabilityType = shared.CapabilityInlineButtons
	CapabilityThreads        CapabilityType = shared.CapabilityThreads
	CapabilityPolls          CapabilityType = shared.CapabilityPolls
	CapabilityStreaming      CapabilityType = shared.CapabilityStreaming
	CapabilityMedia          CapabilityType = shared.CapabilityMedia
	CapabilityNativeCommands CapabilityType = shared.CapabilityNativeCommands
)

type CapabilityScope = shared.CapabilityScope

const (
	CapabilityScopeOff       CapabilityScope = shared.CapabilityScopeOff
	CapabilityScopeDM        CapabilityScope = shared.CapabilityScopeDM
	CapabilityScopeGroup     CapabilityScope = shared.CapabilityScopeGroup
	CapabilityScopeAll       CapabilityScope = shared.CapabilityScopeAll
	CapabilityScopeAllowlist CapabilityScope = shared.CapabilityScopeAllowlist
)

type ChannelCapabilities = shared.ChannelCapabilities
type ChannelCapabilityConfig = shared.ChannelCapabilityConfig
type ChatContext = shared.ChatContext

func DefaultCapabilities() ChannelCapabilities {
	return shared.DefaultCapabilities()
}

func ParseCapabilityScope(s string) CapabilityScope {
	return shared.ParseCapabilityScope(s)
}

func IsCapabilityEnabled(capabilities ChannelCapabilities, capability CapabilityType, scope CapabilityScope, isWhitelisted bool) bool {
	return shared.IsCapabilityEnabled(capabilities, capability, scope, isWhitelisted)
}

func MergeCapabilities(base ChannelCapabilities, overrides []ChannelCapabilities) ChannelCapabilities {
	return shared.MergeCapabilities(base, overrides)
}

func GetDefaultCapabilitiesForChannel(channelType string) ChannelCapabilities {
	return shared.GetDefaultCapabilitiesForChannel(channelType)
}

func ToCapabilities(cfg ChannelCapabilityConfig) ChannelCapabilities {
	return shared.ToCapabilities(cfg)
}

func NewChatContext(metadata map[string]interface{}) ChatContext {
	return shared.NewChatContext(metadata)
}

func CheckCapability(capabilities ChannelCapabilities, capability CapabilityType, ctx ChatContext) bool {
	return shared.CheckCapability(capabilities, capability, ctx)
}
