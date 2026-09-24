package mqtt

import (
	"regexp"

	"github.com/jacobgad/dt241m-controller/internal/mac"
)

const (
	Prefix            = "dt241m"
	HADiscoveryPrefix = "homeassistant"
	HAStatusTopic     = HADiscoveryPrefix + "/status"

	PayloadOnline  = "online"
	PayloadOffline = "offline"
	PayloadPress   = "PRESS"

	ControllerNodeID     = "dt241m_controller"
	ControllerIdentifier = "dt241m:controller"

	ControllerAvailability = Prefix + "/controller/availability"
	ControllerRescanPress  = Prefix + "/controller/rescan/press"
	ControllerKnownState   = Prefix + "/controller/known_devices/state"
	ControllerOnlineState  = Prefix + "/controller/online_devices/state"

	DeviceChannelSetWildcard = Prefix + "/device/+/channel/set"
	DeviceNameSetWildcard    = Prefix + "/device/+/name/set"
)

type DeviceTopics struct {
	Availability string
	ChannelState string
	ChannelSet   string
	NameState    string
	NameSet      string
	IPState      string
	RoleState    string
}

func ForDevice(macAddr string) DeviceTopics {
	base := Prefix + "/device/" + mac.Compact(macAddr)
	return DeviceTopics{
		Availability: base + "/availability",
		ChannelState: base + "/channel/state",
		ChannelSet:   base + "/channel/set",
		NameState:    base + "/name/state",
		NameSet:      base + "/name/set",
		IPState:      base + "/ip/state",
		RoleState:    base + "/role/state",
	}
}

func DeviceNodeID(macAddr string) string     { return "dt241m_" + mac.Compact(macAddr) }
func DeviceIdentifier(macAddr string) string { return "dt241m:" + mac.Compact(macAddr) }

func HADiscoveryTopic(component, nodeID, objectID string) string {
	return HADiscoveryPrefix + "/" + component + "/" + nodeID + "/" + objectID + "/config"
}

var deviceCommandPattern = regexp.MustCompile(`^` + Prefix + `/device/([0-9a-f]{12})/(channel|name)/set$`)

type DeviceCommand struct {
	MAC     string
	Command string
}

func ParseDeviceCommand(topic string) (DeviceCommand, bool) {
	m := deviceCommandPattern.FindStringSubmatch(topic)
	if m == nil {
		return DeviceCommand{}, false
	}
	return DeviceCommand{MAC: mac.FromCompact(m[1]), Command: m[2]}, true
}
