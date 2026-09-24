package mqtt

import (
	"encoding/json"

	"github.com/jacobgad/dt241m-controller/internal/dt241m"
	"github.com/jacobgad/dt241m-controller/internal/registry"
)

type Origin struct {
	Version    string
	SupportURL string
}

type Message struct {
	Topic   string
	Payload map[string]any
}

func (m Message) JSON() string {
	data, _ := json.Marshal(m.Payload)
	return string(data)
}

const (
	manufacturer   = "PWAY"
	controllerName = "DT241M Controller"
)

var Icons = map[string]string{
	"receiver":    "mdi:monitor",
	"transmitter": "mdi:broadcast",
	"unknown":     "mdi:help-network",
	"controller":  "mdi:video-switch",
	"rescan":      "mdi:radar",
	"name":        "mdi:rename-box",
	"ip":          "mdi:ip-network",
	"role":        "mdi:swap-horizontal",
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
	case a.ProductName != nil:
		device["model"] = *a.ProductName
	case a.Model != nil:
		device["model"] = *a.Model
	default:
		device["model"] = "DT241M"
	}
	if a.Model != nil {
		device["model_id"] = *a.Model
	}
	if a.Firmware != nil {
		device["sw_version"] = *a.Firmware
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
			"icon":          Icons["receiver"],
			"device":        adapterDevice(a),
			"origin":        origin(o),
		}),
	}
}

func ReadOnlyChannel(a registry.Adapter, o Origin) Message {
	t := ForDevice(a.MAC)
	return Message{
		Topic: HADiscoveryTopic("sensor", DeviceNodeID(a.MAC), "channel"),
		Payload: withAdapterAvailability(a, map[string]any{
			"name":        "Channel",
			"unique_id":   a.ID + "_channel",
			"object_id":   a.ID + "_channel",
			"state_topic": t.ChannelState,
			"icon":        Icons[string(a.Role)],
			"device":      adapterDevice(a),
			"origin":      origin(o),
		}),
	}
}

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
			"icon":            Icons["name"],
			"availability":    []map[string]any{controllerAvailability()},
			"device":          adapterDevice(a),
			"origin":          origin(o),
		},
	}
}

func IPAddress(a registry.Adapter, o Origin) Message {
	return Message{
		Topic: HADiscoveryTopic("sensor", DeviceNodeID(a.MAC), "ip_address"),
		Payload: withAdapterAvailability(a, map[string]any{
			"name":            "IP address",
			"unique_id":       a.ID + "_ip_address",
			"object_id":       a.ID + "_ip_address",
			"state_topic":     ForDevice(a.MAC).IPState,
			"entity_category": "diagnostic",
			"icon":            Icons["ip"],
			"device":          adapterDevice(a),
			"origin":          origin(o),
		}),
	}
}

func Role(a registry.Adapter, o Origin) Message {
	return Message{
		Topic: HADiscoveryTopic("sensor", DeviceNodeID(a.MAC), "role"),
		Payload: withAdapterAvailability(a, map[string]any{
			"name":            "Role",
			"unique_id":       a.ID + "_role",
			"object_id":       a.ID + "_role",
			"state_topic":     ForDevice(a.MAC).RoleState,
			"entity_category": "diagnostic",
			"icon":            Icons["role"],
			"device":          adapterDevice(a),
			"origin":          origin(o),
		}),
	}
}

func AdapterMessages(a registry.Adapter, o Origin) []Message {
	channel := ReadOnlyChannel(a, o)
	if a.Role == dt241m.RoleReceiver {
		channel = ReceiverChannel(a, o)
	}
	return []Message{channel, Name(a, o), IPAddress(a, o), Role(a, o)}
}

// StaleChannelTopic is the config topic of the *other* channel component, cleared when a role flips.
func StaleChannelTopic(a registry.Adapter) string {
	other := "number"
	if a.Role == dt241m.RoleReceiver {
		other = "sensor"
	}
	return HADiscoveryTopic(other, DeviceNodeID(a.MAC), "channel")
}

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
			"icon":          Icons["rescan"],
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
			"icon":         Icons["controller"],
			"availability": []map[string]any{controllerAvailability()},
			"device":       controllerDevice(o),
			"origin":       origin(o),
		},
	}
}

func ControllerMessages(o Origin) []Message {
	return []Message{
		RescanButton(o),
		countSensor(o, "known_devices", "Known devices", ControllerKnownState),
		countSensor(o, "online_devices", "Online devices", ControllerOnlineState),
	}
}
