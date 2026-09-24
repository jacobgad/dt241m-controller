package mqtt

import (
	"log/slog"
	"regexp"
	"strconv"
	"strings"

	"github.com/jacobgad/dt241m-controller/internal/dt241m"
)

// Actions are invoked synchronously from the broker's delivery goroutine, so each
// must return quickly; the controller reserves its queue slot inline and does the
// work in the background, which is what preserves command order.
type Actions struct {
	ChannelCommand      func(mac string, channel int)
	NameCommand         func(mac string, raw string)
	RescanRequested     func()
	HomeAssistantOnline func()
}

// Subscriptions are the topics the controller listens on.
var Subscriptions = []string{DeviceChannelSetWildcard, DeviceNameSetWildcard, ControllerRescanPress, HAStatusTopic}

var channelPayload = regexp.MustCompile(`^[+-]?\d+(\.0+)?$`)

// ParseChannelPayload accepts integers and integer-valued decimals such as "2.0", which Home Assistant may send.
func ParseChannelPayload(payload string) (int, bool) {
	trimmed := strings.TrimSpace(payload)
	if !channelPayload.MatchString(trimmed) {
		return 0, false
	}
	value, err := strconv.ParseFloat(trimmed, 64)
	if err != nil {
		return 0, false
	}
	channel := int(value)
	if float64(channel) != value || !dt241m.ValidChannel(channel) {
		return 0, false
	}
	return channel, true
}

// NewRouter maps inbound topics to Actions.
func NewRouter(actions Actions, log *slog.Logger) MessageHandler {
	return func(topic string, payload []byte) {
		if cmd, ok := ParseDeviceCommand(topic); ok {
			switch cmd.Command {
			case "channel":
				channel, ok := ParseChannelPayload(string(payload))
				if !ok {
					log.Warn("channel_command_invalid", "mac", cmd.MAC, "payload", string(payload))
					return
				}
				actions.ChannelCommand(cmd.MAC, channel)
			case "name":
				actions.NameCommand(cmd.MAC, string(payload))
			}
			return
		}
		switch topic {
		case ControllerRescanPress:
			actions.RescanRequested()
		case HAStatusTopic:
			if strings.TrimSpace(string(payload)) == PayloadOnline {
				log.Info("home_assistant_online")
				actions.HomeAssistantOnline()
			}
		default:
			log.Debug("mqtt_message_ignored", "topic", topic)
		}
	}
}
