package testutil

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
)

const (
	TxFixtureMAC = "fc:19:28:6c:d2:91"
	RxFixtureMAC = "fc:19:28:6c:d6:d8"
)

func fixturesDir() string {
	_, file, _, _ := runtime.Caller(0)
	return filepath.Join(filepath.Dir(file), "..", "..", "fixtures")
}

func FixtureText(name string) string {
	data, err := os.ReadFile(filepath.Join(fixturesDir(), name+".json"))
	if err != nil {
		panic(err)
	}
	return string(data)
}

func FixtureResult(name string) map[string]any {
	var envelope struct {
		Result map[string]any `json:"result"`
	}
	if err := json.Unmarshal([]byte(FixtureText(name)), &envelope); err != nil {
		panic(err)
	}
	return envelope.Result
}

type RPC struct {
	JSONRPC string         `json:"jsonrpc"`
	Method  string         `json:"method"`
	Params  map[string]any `json:"params"`
	ID      any            `json:"id"`
	IDRaw   json.RawMessage
}

type Request struct {
	IP          string
	Path        string
	Method      string
	ContentType string
	RawBody     string
	DataField   string
	RPC         *RPC
}

func (r Request) ChannelParam() int {
	if r.RPC == nil {
		return -1
	}
	v, ok := r.RPC.Params["channel_id"].(float64)
	if !ok {
		return -1
	}
	return int(v)
}

type Responder func(rpc *RPC, device *Device) *http.Response

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

type UnreachableMode int

const (
	Refuse UnreachableMode = iota
	Hang
)

// Network is an http.RoundTripper that plays the part of a LAN full of DT241M units.
type Network struct {
	mu          sync.Mutex
	devices     map[string]*Device
	requests    []Request
	inFlight    int
	maxInFlight int
	Unreachable UnreachableMode
}

func NewNetwork() *Network {
	return &Network{devices: make(map[string]*Device)}
}

func (n *Network) Place(ip string, d *Device) *Device {
	n.mu.Lock()
	defer n.mu.Unlock()
	n.devices[ip] = d
	return d
}

func (n *Network) Remove(ip string) {
	n.mu.Lock()
	defer n.mu.Unlock()
	delete(n.devices, ip)
}

func (n *Network) Move(from, to string) {
	n.mu.Lock()
	defer n.mu.Unlock()
	d, ok := n.devices[from]
	if !ok {
		panic("no device at " + from)
	}
	delete(n.devices, from)
	n.devices[to] = d
}

func (n *Network) Requests() []Request {
	n.mu.Lock()
	defer n.mu.Unlock()
	return append([]Request(nil), n.requests...)
}

func (n *Network) ResetRequests() {
	n.mu.Lock()
	defer n.mu.Unlock()
	n.requests = nil
}

func (n *Network) RequestsTo(ip, method string) []Request {
	var out []Request
	for _, r := range n.Requests() {
		if r.IP == ip && (method == "" || (r.RPC != nil && r.RPC.Method == method)) {
			out = append(out, r)
		}
	}
	return out
}

func (n *Network) WritesTo(ip string) []Request { return n.RequestsTo(ip, "set_channel_id") }

func (n *Network) AllWrites() []Request {
	var out []Request
	for _, r := range n.Requests() {
		if r.RPC != nil && r.RPC.Method == "set_channel_id" {
			out = append(out, r)
		}
	}
	return out
}

func DeviceResponse(body string) *http.Response {
	return &http.Response{
		StatusCode: http.StatusOK,
		Header:     http.Header{"Content-Type": []string{"text/html"}},
		Body:       io.NopCloser(strings.NewReader(body)),
	}
}

func StatusResponse(status int, body string) *http.Response {
	return &http.Response{StatusCode: status, Header: http.Header{}, Body: io.NopCloser(strings.NewReader(body))}
}

// MaxInFlight is the highest number of simultaneous requests seen.
func (n *Network) MaxInFlight() int {
	n.mu.Lock()
	defer n.mu.Unlock()
	return n.maxInFlight
}

func (n *Network) RoundTrip(req *http.Request) (*http.Response, error) {
	n.mu.Lock()
	n.inFlight++
	n.maxInFlight = max(n.maxInFlight, n.inFlight)
	n.mu.Unlock()
	defer func() {
		n.mu.Lock()
		n.inFlight--
		n.mu.Unlock()
	}()
	recorded, err := record(req)
	if err != nil {
		return nil, err
	}
	n.mu.Lock()
	n.requests = append(n.requests, recorded)
	device := n.devices[recorded.IP]
	mode := n.Unreachable
	n.mu.Unlock()

	if device == nil {
		if mode == Hang {
			return hang(req)
		}
		return nil, errors.New("dial tcp: connection refused")
	}
	if recorded.Path != "/cgi-bin/proav.cgi" || recorded.Method != http.MethodPost {
		return StatusResponse(http.StatusNotFound, "Not Found"), nil
	}
	if device.Responder != nil {
		if custom := device.Responder(recorded.RPC, device); custom != nil {
			return custom, nil
		}
	}
	if recorded.RPC == nil {
		return DeviceResponse(`{"jsonrpc":"2.0","id":1,"error":{"code":-32700,"message":"Parse error"}}`), nil
	}
	switch recorded.RPC.Method {
	case "get_device_info_proav":
		if device.ReadHangs {
			return hang(req)
		}
		return DeviceResponse(device.InfoPayload(recorded.IP)), nil
	case "set_channel_id":
		if device.RejectWrites {
			return DeviceResponse(`{"jsonrpc":"2.0","id":1,"result":{"result":false}}`), nil
		}
		if channel := recorded.ChannelParam(); channel >= 0 {
			device.ApplyChannel(channel)
		}
		if device.WriteHangs {
			return hang(req)
		}
		return DeviceResponse(`{"jsonrpc":"2.0","id":1,"result":{"result":true}}`), nil
	default:
		return DeviceResponse(`{"jsonrpc":"2.0","id":1,"error":{"code":-32601,"message":"Method not found"}}`), nil
	}
}

func hang(req *http.Request) (*http.Response, error) {
	<-req.Context().Done()
	return nil, req.Context().Err()
}

func record(req *http.Request) (Request, error) {
	recorded := Request{
		IP:          req.URL.Hostname(),
		Path:        req.URL.Path,
		Method:      req.Method,
		ContentType: req.Header.Get("Content-Type"),
	}
	if req.Body == nil {
		return recorded, nil
	}
	raw, err := io.ReadAll(req.Body)
	if err != nil {
		return recorded, err
	}
	recorded.RawBody = string(raw)
	req.Body = io.NopCloser(bytes.NewReader(raw))
	reader, err := req.MultipartReader()
	if err != nil {
		return recorded, nil
	}
	for {
		part, err := reader.NextPart()
		if err != nil {
			break
		}
		value, _ := io.ReadAll(part)
		if part.FormName() == "data" {
			recorded.DataField = string(value)
			var rpc RPC
			if json.Unmarshal(value, &rpc) == nil {
				var idOnly struct {
					ID json.RawMessage `json:"id"`
				}
				_ = json.Unmarshal(value, &idOnly)
				rpc.IDRaw = idOnly.ID
				recorded.RPC = &rpc
			}
		}
	}
	return recorded, nil
}

func (n *Network) String() string {
	return fmt.Sprintf("simnet(%d devices, %d requests)", len(n.devices), len(n.requests))
}
