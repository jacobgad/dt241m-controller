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
)

const (
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

func withAdapterAvailability(a registry.Adapter, payload map[string]any) map[string]any {
	payload["availability"] = []map[string]any{
		controllerAvailability(),
		{"topic": ForDevice(a.MAC).Availability, "payload_available": PayloadOnline, "payload_not_available": PayloadOffline},
	}
	payload["availability_mode"] = "all"
	return payload
}

// ReceiverChannel is the writable Channel number entity for a receiver.
func ReceiverChannel(a registry.Adapter, o Origin) Message {
	t := ForDevice(a.MAC)
	return Message{
		Topic: HADiscoveryTopic("number", DeviceNodeID(a.MAC), "channel"),
		Payload: withAdapterAvailability(a, map[string]any{
			"name":          "Channel",
			"unique_id":     a.ID + "_channel",
			"object_id":     a.ID + "_channel",
			"state_topic":   t.ChannelState,
			"command_topic": t.ChannelSet,
			"min":           dt241m.MinChannel,
			"max":           dt241m.MaxChannel,
			"step":          1,
			"mode":          "box",
			"optimistic":    false,
			"retain":        false,
			"qos":           1,
			"icon":          roleIcon(dt241m.RoleReceiver),
			"device":        adapterDevice(a),
			"origin":        origin(o),
		}),
	}
}

// TransmitterChannel is the writable Channel number for a transmitter. It is a
// configuration entity: changing it re-routes every receiver watching that transmitter.
func TransmitterChannel(a registry.Adapter, o Origin) Message {
	t := ForDevice(a.MAC)
	return Message{
		Topic: HADiscoveryTopic("number", DeviceNodeID(a.MAC), "channel"),
		Payload: withAdapterAvailability(a, map[string]any{
			"name":            "Channel",
			"unique_id":       a.ID + "_channel",
			"object_id":       a.ID + "_channel",
			"state_topic":     t.ChannelState,
			"command_topic":   t.ChannelSet,
			"min":             dt241m.MinChannel,
			"max":             dt241m.MaxChannel,
			"step":            1,
			"mode":            "box",
			"optimistic":      false,
			"retain":          false,
			"qos":             1,
			"entity_category": "config",
			"icon":            roleIcon(dt241m.RoleTransmitter),
			"device":          adapterDevice(a),
			"origin":          origin(o),
		}),
	}
}

// ReceiverSource is the Source select on a receiver: pick a transmitter by name instead
// of remembering its channel. The state is "none" when no transmitter uses the channel.
func ReceiverSource(a registry.Adapter, options []string, o Origin) Message {
	t := ForDevice(a.MAC)
	if options == nil {
		options = []string{}
	}
	return Message{
		Topic: HADiscoveryTopic("select", DeviceNodeID(a.MAC), "source"),
		Payload: withAdapterAvailability(a, map[string]any{
			"name":          "Source",
			"unique_id":     a.ID + "_source",
			"object_id":     a.ID + "_source",
			"state_topic":   t.SourceState,
			"command_topic": t.SourceSet,
			"options":       options,
			"optimistic":    false,
			"retain":        false,
			"qos":           1,
			"icon":          iconSource,
			"device":        adapterDevice(a),
			"origin":        origin(o),
		}),
	}
}

// ReadOnlyChannel is the Channel sensor for devices whose role could not be determined.
func ReadOnlyChannel(a registry.Adapter, o Origin) Message {
	t := ForDevice(a.MAC)
	return Message{
		Topic: HADiscoveryTopic("sensor", DeviceNodeID(a.MAC), "channel"),
		Payload: withAdapterAvailability(a, map[string]any{
			"name":        "Channel",
			"unique_id":   a.ID + "_channel",
			"object_id":   a.ID + "_channel",
			"state_topic": t.ChannelState,
			"icon":        roleIcon(a.Role),
			"device":      adapterDevice(a),
			"origin":      origin(o),
		}),
	}
}

// Name is the writable Name text entity; available whenever the controller is, so offline devices can still be renamed.
func Name(a registry.Adapter, o Origin) Message {
	t := ForDevice(a.MAC)
	return Message{
		Topic: HADiscoveryTopic("text", DeviceNodeID(a.MAC), "name"),
		Payload: map[string]any{
			"name":            "Name",
			"unique_id":       a.ID + "_name",
			"object_id":       a.ID + "_name",
			"state_topic":     t.NameState,
			"command_topic":   t.NameSet,
			"min":             0,
			"max":             registry.MaxNameLength,
			"mode":            "text",
			"retain":          false,
			"qos":             1,
			"entity_category": "config",
			"icon":            iconName,
			"availability":    []map[string]any{controllerAvailability()},
			"device":          adapterDevice(a),
			"origin":          origin(o),
		},
	}
}

// IPAddress is the diagnostic IP sensor.
func IPAddress(a registry.Adapter, o Origin) Message {
	return Message{
		Topic: HADiscoveryTopic("sensor", DeviceNodeID(a.MAC), "ip_address"),
		Payload: withAdapterAvailability(a, map[string]any{
			"name":            "IP address",
			"unique_id":       a.ID + "_ip_address",
			"object_id":       a.ID + "_ip_address",
			"state_topic":     ForDevice(a.MAC).IPState,
			"entity_category": "diagnostic",
			"icon":            iconIP,
			"device":          adapterDevice(a),
			"origin":          origin(o),
		}),
	}
}

// Role is the diagnostic Role sensor.
func Role(a registry.Adapter, o Origin) Message {
	return Message{
		Topic: HADiscoveryTopic("sensor", DeviceNodeID(a.MAC), "role"),
		Payload: withAdapterAvailability(a, map[string]any{
			"name":            "Role",
			"unique_id":       a.ID + "_role",
			"object_id":       a.ID + "_role",
			"state_topic":     ForDevice(a.MAC).RoleState,
			"entity_category": "diagnostic",
			"icon":            iconRole,
			"device":          adapterDevice(a),
			"origin":          origin(o),
		}),
	}
}

// AdapterMessages lists every entity published for an adapter; sourceOptions is only used for receivers.
func AdapterMessages(a registry.Adapter, sourceOptions []string, o Origin) []Message {
	messages := []Message{Name(a, o), IPAddress(a, o), Role(a, o)}
	switch a.Role {
	case dt241m.RoleReceiver:
		messages = append(messages, ReceiverChannel(a, o), ReceiverSource(a, sourceOptions, o))
	case dt241m.RoleTransmitter:
		messages = append(messages, TransmitterChannel(a, o))
	default:
		messages = append(messages, ReadOnlyChannel(a, o))
	}
	return messages
}

// StaleTopics are config topics that must be cleared for an adapter in its current role,
// so that a unique_id never lingers under a platform it no longer uses (role flips, and
// the 2.0 → 2.1 move of the transmitter channel from sensor to number).
func StaleTopics(a registry.Adapter) []string {
	node := DeviceNodeID(a.MAC)
	switch a.Role {
	case dt241m.RoleReceiver:
		return []string{HADiscoveryTopic("sensor", node, "channel")}
	case dt241m.RoleTransmitter:
		return []string{HADiscoveryTopic("sensor", node, "channel"), HADiscoveryTopic("select", node, "source")}
	default:
		return []string{HADiscoveryTopic("number", node, "channel"), HADiscoveryTopic("select", node, "source")}
	}
}

// RescanButton is the controller's Rescan network button.
func RescanButton(o Origin) Message {
	return Message{
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

// ControllerMessages lists the controller device's entities.
func ControllerMessages(o Origin) []Message {
	return []Message{
		RescanButton(o),
		countSensor(o, "known_devices", "Known devices", ControllerKnownState),
		countSensor(o, "online_devices", "Online devices", ControllerOnlineState),
	}
}
