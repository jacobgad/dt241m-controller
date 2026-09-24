package mqtt

import (
	"encoding/json"

	"github.com/jacobgad/dt241m-controller/internal/dt241m"
	"github.com/jacobgad/dt241m-controller/internal/registry"
)

// Origin identifies this add-on in discovery payloads.
type Origin struct {
	Version    string
	SupportURL string
}

// Message is one retained discovery config.
type Message struct {
	Topic   string
	Payload map[string]any
}

// JSON renders the payload.
func (m Message) JSON() string {
	data, _ := json.Marshal(m.Payload)
	return string(data)
}

const (
	manufacturer   = "PWAY"
	controllerName = "DT241M Controller"

	iconController = "mdi:video-switch"
	iconRescan     = "mdi:radar"
	iconName       = "mdi:rename-box"
	iconIP         = "mdi:ip-network"
	iconRole       = "mdi:swap-horizontal"
	iconSource     = "mdi:video-input-hdmi"
)

func roleIcon(role dt241m.Role) string {
	switch role {
	case dt241m.RoleReceiver:
		return "mdi:monitor"
	case dt241m.RoleTransmitter:
		return "mdi:broadcast"
	default:
		return "mdi:help-network"
	}
}

// entity describes one Home Assistant entity of an adapter. The common envelope
// (ids, device, origin, availability) is added by build; fields supplies the rest.
type entity struct {
	component string
	object    string
	name      string
	category  string
	icon      func(registry.Adapter) string
	// controllerOnly entities stay available while the device is offline (the Name text).
	controllerOnly bool
	fields         func(t DeviceTopics, sourceOptions []string) map[string]any
}

func staticIcon(icon string) func(registry.Adapter) string {
	return func(registry.Adapter) string { return icon }
}

func channelNumber(category string) entity {
	return entity{
		component: "number", object: "channel", name: "Channel", category: category,
		icon: func(a registry.Adapter) string { return roleIcon(a.Role) },
		fields: func(t DeviceTopics, _ []string) map[string]any {
			return map[string]any{
				"state_topic":   t.ChannelState,
				"command_topic": t.ChannelSet,
				"min":           dt241m.MinChannel,
				"max":           dt241m.MaxChannel,
				"step":          1,
				"mode":          "box",
				"optimistic":    false,
				"retain":        false,
				"qos":           1,
			}
		},
	}
}

var (
	receiverChannel    = channelNumber("")
	transmitterChannel = channelNumber("config")

	channelSensor = entity{
		component: "sensor", object: "channel", name: "Channel",
		icon: func(a registry.Adapter) string { return roleIcon(a.Role) },
		fields: func(t DeviceTopics, _ []string) map[string]any {
			return map[string]any{"state_topic": t.ChannelState}
		},
	}

	sourceSelect = entity{
		component: "select", object: "source", name: "Source", icon: staticIcon(iconSource),
		fields: func(t DeviceTopics, options []string) map[string]any {
			if options == nil {
				options = []string{}
			}
			return map[string]any{
				"state_topic":   t.SourceState,
				"command_topic": t.SourceSet,
				"options":       options,
				"optimistic":    false,
				"retain":        false,
				"qos":           1,
			}
		},
	}

	nameText = entity{
		component: "text", object: "name", name: "Name", category: "config", icon: staticIcon(iconName),
		controllerOnly: true,
		fields: func(t DeviceTopics, _ []string) map[string]any {
			return map[string]any{
				"state_topic":   t.NameState,
				"command_topic": t.NameSet,
				"min":           0,
				"max":           registry.MaxNameLength,
				"mode":          "text",
				"retain":        false,
				"qos":           1,
			}
		},
	}

	ipSensor = entity{
		component: "sensor", object: "ip_address", name: "IP address", category: "diagnostic", icon: staticIcon(iconIP),
		fields: func(t DeviceTopics, _ []string) map[string]any {
			return map[string]any{"state_topic": t.IPState}
		},
	}

	roleSensor = entity{
		component: "sensor", object: "role", name: "Role", category: "diagnostic", icon: staticIcon(iconRole),
		fields: func(t DeviceTopics, _ []string) map[string]any {
			return map[string]any{"state_topic": t.RoleState}
		},
	}
)

// entitiesFor lists the entities an adapter exposes in its current role.
func entitiesFor(role dt241m.Role) []entity {
	common := []entity{nameText, ipSensor, roleSensor}
	switch role {
	case dt241m.RoleReceiver:
		return append(common, receiverChannel, sourceSelect)
	case dt241m.RoleTransmitter:
		return append(common, transmitterChannel)
	default:
		return append(common, channelSensor)
	}
}

// StaleTopics are config topics an adapter does not use in its current role, cleared so a
// unique_id never lingers under another platform after a role flip or the 2.0 → 2.1 move
// of the transmitter channel from sensor to number.
func StaleTopics(a registry.Adapter) []string {
	all := []entity{receiverChannel, channelSensor, sourceSelect}
	var stale []string
	for _, candidate := range all {
		if !containsEntity(entitiesFor(a.Role), candidate) {
			stale = append(stale, HADiscoveryTopic(candidate.component, DeviceNodeID(a.MAC), candidate.object))
		}
	}
	return stale
}

func containsEntity(list []entity, e entity) bool {
	for _, item := range list {
		if item.component == e.component && item.object == e.object {
			return true
		}
	}
	return false
}

func build(a registry.Adapter, e entity, sourceOptions []string, o Origin) Message {
	topics := ForDevice(a.MAC)
	payload := e.fields(topics, sourceOptions)
	payload["name"] = e.name
	payload["unique_id"] = a.ID + "_" + e.object
	payload["object_id"] = a.ID + "_" + e.object
	payload["icon"] = e.icon(a)
	payload["device"] = adapterDevice(a)
	payload["origin"] = origin(o)
	if e.category != "" {
		payload["entity_category"] = e.category
	}
	if e.controllerOnly {
		payload["availability"] = []map[string]any{controllerAvailability()}
	} else {
		payload["availability"] = []map[string]any{
			controllerAvailability(),
			{"topic": topics.Availability, "payload_available": PayloadOnline, "payload_not_available": PayloadOffline},
		}
		payload["availability_mode"] = "all"
	}
	return Message{Topic: HADiscoveryTopic(e.component, DeviceNodeID(a.MAC), e.object), Payload: payload}
}

// AdapterMessages lists every entity published for an adapter; sourceOptions feeds receivers' Source select.
func AdapterMessages(a registry.Adapter, sourceOptions []string, o Origin) []Message {
	specs := entitiesFor(a.Role)
	out := make([]Message, 0, len(specs))
	for _, e := range specs {
		out = append(out, build(a, e, sourceOptions, o))
	}
	return out
}

// ReceiverSource is the Source select on a receiver, rebuilt on its own whenever the
// transmitter catalogue changes.
func ReceiverSource(a registry.Adapter, options []string, o Origin) Message {
	return build(a, sourceSelect, options, o)
}

func origin(o Origin) map[string]any {
	return map[string]any{"name": controllerName, "sw_version": o.Version, "support_url": o.SupportURL}
}

func controllerAvailability() map[string]any {
	return map[string]any{"topic": ControllerAvailability, "payload_available": PayloadOnline, "payload_not_available": PayloadOffline}
}

func controllerDevice(o Origin) map[string]any {
	return map[string]any{
		"identifiers":  []string{ControllerIdentifier},
		"name":         controllerName,
		"manufacturer": "dt241m-controller add-on",
		"model":        "DT241M MQTT bridge",
		"sw_version":   o.Version,
	}
}

func adapterDevice(a registry.Adapter) map[string]any {
	device := map[string]any{
		"identifiers":  []string{DeviceIdentifier(a.MAC)},
		"connections":  [][]string{{"mac", a.MAC}},
		"name":         a.DisplayName(),
		"manufacturer": manufacturer,
		"via_device":   ControllerIdentifier,
	}
	switch {
	case a.ProductName != "":
		device["model"] = a.ProductName
	case a.Model != "":
		device["model"] = a.Model
	default:
		device["model"] = "DT241M"
	}
	if a.Model != "" {
		device["model_id"] = a.Model
	}
	if a.Firmware != "" {
		device["sw_version"] = a.Firmware
	}
	return device
}

// ControllerMessages lists the controller device's entities.
func ControllerMessages(o Origin) []Message {
	return []Message{
		{
			Topic: HADiscoveryTopic("button", ControllerNodeID, "rescan"),
			Payload: map[string]any{
				"name":          "Rescan network",
				"unique_id":     ControllerNodeID + "_rescan",
				"object_id":     ControllerNodeID + "_rescan",
				"command_topic": ControllerRescanPress,
				"payload_press": PayloadPress,
				"retain":        false,
				"qos":           1,
				"icon":          iconRescan,
				"availability":  []map[string]any{controllerAvailability()},
				"device":        controllerDevice(o),
				"origin":        origin(o),
			},
		},
		countSensor(o, "known_devices", "Known devices", ControllerKnownState),
		countSensor(o, "online_devices", "Online devices", ControllerOnlineState),
	}
}

func countSensor(o Origin, objectID, name, stateTopic string) Message {
	return Message{
		Topic: HADiscoveryTopic("sensor", ControllerNodeID, objectID),
		Payload: map[string]any{
			"name":         name,
			"unique_id":    ControllerNodeID + "_" + objectID,
			"object_id":    ControllerNodeID + "_" + objectID,
			"state_topic":  stateTopic,
			"state_class":  "measurement",
			"icon":         iconController,
			"availability": []map[string]any{controllerAvailability()},
			"device":       controllerDevice(o),
			"origin":       origin(o),
		},
	}
}
