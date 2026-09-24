// Package dt241m is the only code that speaks to DT241M hardware: a multipart JSON-RPC
// client for /cgi-bin/proav.cgi limited to get_device_info_proav and set_channel_id,
// plus transmitter/receiver classification of the returned device information.
package dt241m

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"
)

// Wire-level constants observed on firmware 1.13471.133.
const (
	RPCPath      = "/cgi-bin/proav.cgi"
	RPCFormField = "data"
	RPCID        = 1

	MethodGetDeviceInfo = "get_device_info_proav"
	MethodSetChannel    = "set_channel_id"

	MinChannel = 0
	MaxChannel = 255
)

// Code classifies a failed device request so callers can tell an ambiguous write from a rejected one.
type Code string

// Error codes.
const (
	CodeInput       Code = "INPUT"
	CodeHTTPStatus  Code = "HTTP_STATUS"
	CodeInvalidJSON Code = "INVALID_JSON"
	CodeEnvelope    Code = "ENVELOPE"
	CodeRPCError    Code = "RPC_ERROR"
	CodeResultShape Code = "RESULT_SHAPE"
	CodeTimeout     Code = "TIMEOUT"
	CodeCanceled    Code = "CANCELED"
	CodeTransport   Code = "TRANSPORT"
	CodeNotAccepted Code = "NOT_ACCEPTED"
)

// Error is returned for every failed device request.
type Error struct {
	Code Code
	Msg  string
	Err  error
}

func (e *Error) Error() string {
	if e.Err != nil {
		return string(e.Code) + ": " + e.Msg + ": " + e.Err.Error()
	}
	return string(e.Code) + ": " + e.Msg
}

func (e *Error) Unwrap() error { return e.Err }

func newError(code Code, msg string, cause error) *Error {
	return &Error{Code: code, Msg: msg, Err: cause}
}

// IsCode reports whether err is a device Error with the given code.
func IsCode(err error, code Code) bool {
	var e *Error
	return errors.As(err, &e) && e.Code == code
}

// ValidChannel reports whether channel is inside the advertised [0,255] range.
func ValidChannel(channel int) bool {
	return channel >= MinChannel && channel <= MaxChannel
}

// Role is a device's function in the matrix.
type Role string

// Roles.
const (
	RoleTransmitter Role = "transmitter"
	RoleReceiver    Role = "receiver"
	RoleUnknown     Role = "unknown"
)

// Capability mirrors one entry of the firmware's capability map.
type Capability struct {
	Enable *bool  `json:"enable,omitempty"`
	Range  string `json:"range,omitempty"`
}

// DeviceInfo is the result of get_device_info_proav. Raw retains every field the
// firmware sent so that future firmware keys survive a round trip through this type.
type DeviceInfo struct {
	DevName     *string
	ProductName *string
	Model       *string
	Version     *string
	LanMAC      string
	ChannelID   int
	Capability  map[string]Capability
	Raw         map[string]json.RawMessage
}

// UnmarshalJSON requires lan_mac_addr and an integer channel_id in range; everything else is optional.
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

// Classify derives the role from product_name and model. dev_name is deliberately
// ignored: the captured receiver reports ER02_… while its product string says ER01.
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
