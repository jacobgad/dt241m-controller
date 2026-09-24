package dt241m

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"net/netip"
	"time"
)

type Client interface {
	GetDeviceInfo(ctx context.Context, ip string) (*DeviceInfo, error)
	SetChannel(ctx context.Context, ip string, channel int) error
}

type Options struct {
	Transport http.RoundTripper
	Timeout   time.Duration
	Password  string
}

type HTTPClient struct {
	http     *http.Client
	timeout  time.Duration
	password string
}

func NewHTTPClient(opts Options) *HTTPClient {
	transport := opts.Transport
	if transport == nil {
		transport = &http.Transport{
			MaxIdleConns:        16,
			MaxIdleConnsPerHost: 2,
			IdleConnTimeout:     30 * time.Second,
			DisableCompression:  true,
		}
	}
	timeout := opts.Timeout
	if timeout <= 0 {
		timeout = 2 * time.Second
	}
	return &HTTPClient{
		http: &http.Client{
			Transport: transport,
			CheckRedirect: func(*http.Request, []*http.Request) error {
				return errors.New("redirects are not allowed")
			},
		},
		timeout:  timeout,
		password: opts.Password,
	}
}

type rpcRequest struct {
	JSONRPC string         `json:"jsonrpc"`
	Method  string         `json:"method"`
	Params  map[string]any `json:"params"`
	ID      int            `json:"id"`
}

type rpcEnvelope struct {
	JSONRPC string           `json:"jsonrpc"`
	ID      json.Number      `json:"id"`
	Result  json.RawMessage  `json:"result"`
	Error   *json.RawMessage `json:"error"`
}

func EncodeRequest(method string, params map[string]any) []byte {
	body, _ := json.Marshal(rpcRequest{JSONRPC: "2.0", Method: method, Params: params, ID: RPCID})
	return body
}

func (c *HTTPClient) GetDeviceInfo(ctx context.Context, ip string) (*DeviceInfo, error) {
	result, err := c.rpc(ctx, ip, EncodeRequest(MethodGetDeviceInfo, map[string]any{}))
	if err != nil {
		return nil, err
	}
	var info DeviceInfo
	if err := json.Unmarshal(result, &info); err != nil {
		return nil, newError(CodeResultShape, "device info is missing required fields")
	}
	return &info, nil
}

func (c *HTTPClient) SetChannel(ctx context.Context, ip string, channel int) error {
	if !ValidChannel(channel) {
		return newError(CodeInput, "channel must be an integer between 0 and 255")
	}
	result, err := c.rpc(ctx, ip, EncodeRequest(MethodSetChannel, map[string]any{"pswd": c.password, "channel_id": channel}))
	if err != nil {
		return err
	}
	var ack struct {
		Result *bool `json:"result"`
	}
	if err := json.Unmarshal(result, &ack); err != nil || ack.Result == nil || !*ack.Result {
		return newError(CodeNotAccepted, "device did not explicitly acknowledge success")
	}
	return nil
}

func (c *HTTPClient) rpc(ctx context.Context, ip string, body []byte) (json.RawMessage, error) {
	addr, err := netip.ParseAddr(ip)
	if err != nil || !addr.Is4() {
		return nil, newError(CodeInput, "expected an IPv4 address")
	}

	var form bytes.Buffer
	writer := multipart.NewWriter(&form)
	if err := writer.WriteField(RPCFormField, string(body)); err != nil {
		return nil, newError(CodeInput, "could not encode request")
	}
	if err := writer.Close(); err != nil {
		return nil, newError(CodeInput, "could not encode request")
	}

	ctx, cancel := context.WithTimeout(ctx, c.timeout)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, "http://"+ip+RPCPath, &form)
	if err != nil {
		return nil, newError(CodeInput, "could not build request")
	}
	req.Header.Set("Content-Type", writer.FormDataContentType())

	resp, err := c.http.Do(req)
	if err != nil {
		if errors.Is(err, context.DeadlineExceeded) || errors.Is(ctx.Err(), context.DeadlineExceeded) {
			return nil, newError(CodeTimeout, "request timed out; a write may still have applied")
		}
		return nil, newError(CodeTransport, "could not complete the device HTTP request")
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		return nil, newError(CodeHTTPStatus, fmt.Sprintf("device returned HTTP %d", resp.StatusCode))
	}
	text, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		if errors.Is(ctx.Err(), context.DeadlineExceeded) {
			return nil, newError(CodeTimeout, "request timed out; a write may still have applied")
		}
		return nil, newError(CodeTransport, "could not read the device response")
	}

	var envelope rpcEnvelope
	if err := json.Unmarshal(text, &envelope); err != nil {
		return nil, newError(CodeInvalidJSON, "device response was not valid JSON")
	}
	if envelope.JSONRPC != "2.0" || envelope.ID.String() != fmt.Sprint(RPCID) {
		return nil, newError(CodeEnvelope, "unexpected JSON-RPC envelope or response ID")
	}
	if envelope.Error != nil {
		return nil, newError(CodeRPCError, "device returned a JSON-RPC error")
	}
	trimmed := bytes.TrimSpace(envelope.Result)
	if len(trimmed) == 0 || trimmed[0] != '{' {
		return nil, newError(CodeResultShape, "expected a JSON-RPC result object")
	}
	return envelope.Result, nil
}
