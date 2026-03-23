package channels

import (
	"github.com/smallnest/goclaw/internal/core/bus"
	"github.com/smallnest/goclaw/internal/core/channels/shared"
)

type BaseChannel = shared.BaseChannel
type BaseChannelConfig = shared.BaseChannelConfig
type BaseChannelImpl = shared.BaseChannelImpl

func NewBaseChannelImpl(name, accountID string, config BaseChannelConfig, bus *bus.MessageBus) *BaseChannelImpl {
	return shared.NewBaseChannelImpl(name, accountID, config, bus)
}
