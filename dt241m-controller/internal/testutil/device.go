package testutil

import (
	"encoding/json"
	"net/http"
	"strings"
	"sync"
)

// Responder overrides a device's reply; returning nil falls back to the default behaviour.
type Responder func(rpc *RPC, device *Device) *http.Response

// Device is a simulated DT241M unit. Reported, Video and FrontPanel are kept separate so
// tests can model the observed quirk of a stale panel after a successful HTTP switch.
type Device struct {
	MAC             string
	Template        map[string]any
	Reported        int
	Video           int
	FrontPanel      int
	AckWithoutApply bool
	WriteHangs      bool
	ReadHangs       bool
	RejectWrites    bool
	Responder       Responder
	WriteCount      int
	mu              sync.Mutex
}

// DeviceOptions configures NewDevice.
type DeviceOptions struct {
	MAC             string
	Fixture         string
	Reported        *int
	FrontPanel      *int
	Overrides       map[string]any
	AckWithoutApply bool
	WriteHangs      bool
	ReadHangs       bool
	RejectWrites    bool
	Responder       Responder
}

// NewDevice builds a device from a captured fixture plus overrides.
func NewDevice(opts DeviceOptions) *Device {
	template := FixtureResult(opts.Fixture)
	for k, v := range opts.Overrides {
		template[k] = v
	}
	initial := int(template["channel_id"].(float64))
	if opts.Reported != nil {
		initial = *opts.Reported
	}
	d := &Device{
		MAC:             opts.MAC,
		Template:        template,
		Reported:        initial,
		Video:           initial,
		FrontPanel:      initial,
		AckWithoutApply: opts.AckWithoutApply,
		WriteHangs:      opts.WriteHangs,
		ReadHangs:       opts.ReadHangs,
		RejectWrites:    opts.RejectWrites,
		Responder:       opts.Responder,
	}
	if opts.FrontPanel != nil {
		d.FrontPanel = *opts.FrontPanel
	}
	return d
}

func (d *Device) SetTemplate(key string, value any) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.Template[key] = value
}

func (d *Device) SetReported(channel int) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.Reported = channel
}

func (d *Device) ReportedChannel() int {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.Reported
}

func (d *Device) Writes() int {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.WriteCount
}

func (d *Device) InfoPayload(ip string) string {
	d.mu.Lock()
	defer d.mu.Unlock()
	result := make(map[string]any, len(d.Template)+3)
	for k, v := range d.Template {
		result[k] = v
	}
	result["lan_mac_addr"] = strings.ToUpper(d.MAC)
	result["lan_ip_addr"] = ip
	result["channel_id"] = d.Reported
	body, _ := json.Marshal(map[string]any{"jsonrpc": "2.0", "id": 1, "result": result})
	return string(body)
}

func (d *Device) ApplyChannel(channel int) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.WriteCount++
	if d.AckWithoutApply {
		return
	}
	d.Reported = channel
	d.Video = channel
}
