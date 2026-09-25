package testutil

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
)

// RPC is the decoded JSON-RPC request a device received.
type RPC struct {
	JSONRPC string         `json:"jsonrpc"`
	Method  string         `json:"method"`
	Params  map[string]any `json:"params"`
	ID      any            `json:"id"`
	IDRaw   json.RawMessage
}

// Request is one recorded HTTP request, including its multipart body.
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

// UnreachableMode is how the network treats addresses with no device.
type UnreachableMode int

// Behaviours for unreachable addresses.
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

// NewNetwork returns an empty LAN.
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

// DeviceResponse mimics the firmware: HTTP 200 with a text/html content type and a JSON body.
func DeviceResponse(body string) *http.Response {
	return &http.Response{
		StatusCode: http.StatusOK,
		Header:     http.Header{"Content-Type": []string{"text/html"}},
		Body:       io.NopCloser(strings.NewReader(body)),
	}
}

// StatusResponse builds an arbitrary HTTP response for failure cases.
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
