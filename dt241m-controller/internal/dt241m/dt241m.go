package dt241m

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"
)

const (
	RPCPath      = "/cgi-bin/proav.cgi"
	RPCFormField = "data"
	RPCID        = 1

	MethodGetDeviceInfo = "get_device_info_proav"
	MethodSetChannel    = "set_channel_id"

	MinChannel = 0
	MaxChannel = 255
)

type Code string

const (
	CodeInput       Code = "INPUT"
	CodeHTTPStatus  Code = "HTTP_STATUS"
	CodeInvalidJSON Code = "INVALID_JSON"
	CodeEnvelope    Code = "ENVELOPE"
	CodeRPCError    Code = "RPC_ERROR"
	CodeResultShape Code = "RESULT_SHAPE"
	CodeTimeout     Code = "TIMEOUT"
	CodeTransport   Code = "TRANSPORT"
	CodeNotAccepted Code = "NOT_ACCEPTED"
)

type Error struct {
	Code Code
	Msg  string
}

func (e *Error) Error() string { return string(e.Code) + ": " + e.Msg }

func newError(code Code, msg string) *Error { return &Error{Code: code, Msg: msg} }

func IsCode(err error, code Code) bool {
	var e *Error
	return errors.As(err, &e) && e.Code == code
}

func ValidChannel(channel int) bool {
	return channel >= MinChannel && channel <= MaxChannel
}

type Role string

const (
	RoleTransmitter Role = "transmitter"
	RoleReceiver    Role = "receiver"
	RoleUnknown     Role = "unknown"
)

type Capability struct {
	Enable *bool  `json:"enable,omitempty"`
	Range  string `json:"range,omitempty"`
}

// DeviceInfo is the parsed result of get_device_info_proav. Raw keeps every field the firmware sent.
type DeviceInfo struct {
	DevName     *string
	ProductName *string
	Model       *string
	Version     *string
	LanMAC      string
	LanIP       *string
	ChannelID   int
	Capability  map[string]Capability
	Raw         map[string]json.RawMessage
}

func (d *DeviceInfo) UnmarshalJSON(data []byte) error {
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(data, &raw); err != nil {
		return err
	}
	var mac string
	if err := optionalString(raw, "lan_mac_addr", &mac); err != nil || mac == "" {
		return fmt.Errorf("lan_mac_addr missing")
	}
	channelRaw, ok := raw["channel_id"]
	if !ok {
		return fmt.Errorf("channel_id missing")
	}
	var channel json.Number
	if len(channelRaw) == 0 || channelRaw[0] == '"' || json.Unmarshal(channelRaw, &channel) != nil {
		return fmt.Errorf("channel_id is not a number")
	}
	channelInt, err := channel.Int64()
	if err != nil || !ValidChannel(int(channelInt)) {
		return fmt.Errorf("channel_id out of range")
	}
	d.Raw = raw
	d.LanMAC = mac
	d.ChannelID = int(channelInt)
	d.DevName = stringPtr(raw, "dev_name")
	d.ProductName = stringPtr(raw, "product_name")
	d.Model = stringPtr(raw, "model")
	d.Version = stringPtr(raw, "version")
	d.LanIP = stringPtr(raw, "lan_ip_addr")
	if capRaw, ok := raw["capability"]; ok {
		_ = json.Unmarshal(capRaw, &d.Capability)
	}
	return nil
}

func optionalString(raw map[string]json.RawMessage, key string, out *string) error {
	value, ok := raw[key]
	if !ok || string(value) == "null" {
		return nil
	}
	return json.Unmarshal(value, out)
}

func stringPtr(raw map[string]json.RawMessage, key string) *string {
	var s string
	if err := optionalString(raw, key, &s); err != nil {
		return nil
	}
	if value, ok := raw[key]; !ok || string(value) == "null" {
		return nil
	}
	return &s
}

// Classify decides the role from product_name and model only; conflicting or missing evidence yields RoleUnknown.
func Classify(info *DeviceInfo) Role {
	var haystack []string
	for _, s := range []*string{info.ProductName, info.Model} {
		if s != nil {
			haystack = append(haystack, strings.ToLower(*s))
		}
	}
	looksTx, looksRx := false, false
	for _, s := range haystack {
		looksTx = looksTx || strings.Contains(s, "proavtx")
		looksRx = looksRx || strings.Contains(s, "proavrx")
	}
	switch {
	case looksTx && !looksRx:
		return RoleTransmitter
	case looksRx && !looksTx:
		return RoleReceiver
	default:
		return RoleUnknown
	}
}
