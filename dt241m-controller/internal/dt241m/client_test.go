package dt241m_test

import (
	"context"
	"encoding/json"
	"net/http"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/jacobgad/dt241m-controller/internal/dt241m"
	"github.com/jacobgad/dt241m-controller/internal/testutil"
)

const (
	txIP = "10.0.0.11"
	rxIP = "10.0.0.12"
)

func network() *testutil.Network {
	net := testutil.NewNetwork()
	net.Place(txIP, testutil.NewDevice(testutil.DeviceOptions{MAC: testutil.TxFixtureMAC, Fixture: "tx-info-initial-channel-3"}))
	net.Place(rxIP, testutil.NewDevice(testutil.DeviceOptions{MAC: testutil.RxFixtureMAC, Fixture: "rx-info-initial-channel-2"}))
	return net
}

func client(net *testutil.Network, timeout time.Duration) *dt241m.HTTPClient {
	return dt241m.NewHTTPClient(dt241m.Options{Transport: net, Timeout: timeout})
}

func expectCode(t *testing.T, err error, code dt241m.Code) {
	t.Helper()
	if !dt241m.IsCode(err, code) {
		t.Fatalf("expected %s, got %v", code, err)
	}
}

func TestRequestShape(t *testing.T) {
	net := network()
	if _, err := client(net, time.Second).GetDeviceInfo(context.Background(), rxIP); err != nil {
		t.Fatal(err)
	}
	req := net.Requests()[0]
	if req.Method != http.MethodPost || req.Path != "/cgi-bin/proav.cgi" || req.IP != rxIP {
		t.Fatalf("unexpected request %+v", req)
	}
	if !strings.HasPrefix(req.ContentType, "multipart/form-data; boundary=") {
		t.Fatalf("content type %q", req.ContentType)
	}
	if !strings.Contains(req.RawBody, `Content-Disposition: form-data; name="data"`) || strings.Contains(req.RawBody, "filename=") {
		t.Fatalf("body %q", req.RawBody)
	}
	if strings.Count(req.RawBody, `name="`) != 1 {
		t.Fatalf("expected exactly one form field")
	}
	var got map[string]any
	if err := json.Unmarshal([]byte(req.DataField), &got); err != nil {
		t.Fatal(err)
	}
	if got["jsonrpc"] != "2.0" || got["method"] != "get_device_info_proav" || got["id"] != float64(1) {
		t.Fatalf("rpc %v", got)
	}
	if params, ok := got["params"].(map[string]any); !ok || len(params) != 0 {
		t.Fatalf("params %v", got["params"])
	}
	if string(req.RPC.IDRaw) != "1" {
		t.Fatalf("id must be numeric 1, got %s", req.RPC.IDRaw)
	}
}

func TestSetChannelRequest(t *testing.T) {
	net := network()
	if err := client(net, time.Second).SetChannel(context.Background(), rxIP, 2); err != nil {
		t.Fatal(err)
	}
	req := net.Requests()[0]
	if req.DataField != `{"jsonrpc":"2.0","method":"set_channel_id","params":{"channel_id":2,"pswd":""},"id":1}` {
		t.Fatalf("data %s", req.DataField)
	}
}

func TestRPCIDStaysOne(t *testing.T) {
	net := network()
	c := client(net, time.Second)
	ctx := context.Background()
	_, _ = c.GetDeviceInfo(ctx, rxIP)
	_ = c.SetChannel(ctx, rxIP, 5)
	_, _ = c.GetDeviceInfo(ctx, rxIP)
	for _, r := range net.Requests() {
		if string(r.RPC.IDRaw) != "1" {
			t.Fatalf("id %s", r.RPC.IDRaw)
		}
	}
}

func TestRejectsNonIPv4WithoutSending(t *testing.T) {
	net := network()
	c := client(net, time.Second)
	for _, host := range []string{"example.com", "http://10.0.0.1", "::1"} {
		_, err := c.GetDeviceInfo(context.Background(), host)
		expectCode(t, err, dt241m.CodeInput)
	}
	if len(net.Requests()) != 0 {
		t.Fatal("request was sent")
	}
}

func TestParsesCapturedFixtures(t *testing.T) {
	var tx dt241m.DeviceInfo
	if err := json.Unmarshal(mustResult("tx-info-initial-channel-3"), &tx); err != nil {
		t.Fatal(err)
	}
	if tx.LanMAC != "FC:19:28:6C:D2:91" || tx.ChannelID != 3 || *tx.ProductName != "ProAVTx ET01" || *tx.Version != "1.13471.133" {
		t.Fatalf("tx %+v", tx)
	}
	if string(tx.Raw["wifi_mac_addr"]) != "null" || string(tx.Raw["resolution"]) != `"1920x1080 @60Hz RGB"` {
		t.Fatalf("raw fields not preserved")
	}
	if cap := tx.Capability["set_channel_id"]; cap.Enable == nil || !*cap.Enable || cap.Range != "[0,255]" {
		t.Fatalf("capability %+v", cap)
	}

	var after dt241m.DeviceInfo
	_ = json.Unmarshal(mustResult("tx-info-after-set-channel-4"), &after)
	if after.ChannelID != 4 {
		t.Fatal("expected channel 4")
	}

	var rx dt241m.DeviceInfo
	if err := json.Unmarshal(mustResult("rx-info-initial-channel-2"), &rx); err != nil {
		t.Fatal(err)
	}
	if rx.LanMAC != "FC:19:28:6C:D6:D8" || rx.ChannelID != 2 || *rx.DevName != "ER02_286CD6D8" || string(rx.Raw["resolution"]) != `"1920x1080_60P"` {
		t.Fatalf("rx %+v", rx)
	}
}

func TestPreservesUnknownFields(t *testing.T) {
	result := testutil.FixtureResult("rx-info-initial-channel-2")
	result["future_field"] = "keep me"
	data, _ := json.Marshal(result)
	var info dt241m.DeviceInfo
	if err := json.Unmarshal(data, &info); err != nil {
		t.Fatal(err)
	}
	if string(info.Raw["future_field"]) != `"keep me"` || string(info.Raw["swsp_mode"]) != `"SW"` {
		t.Fatal("unknown fields dropped")
	}
}

func TestAcceptsCapturedAcknowledgements(t *testing.T) {
	for _, fixture := range []string{"tx-set-channel-4-success", "rx-set-channel-1-success"} {
		net := testutil.NewNetwork()
		body := testutil.FixtureText(fixture)
		net.Place(rxIP, testutil.NewDevice(testutil.DeviceOptions{
			MAC: testutil.RxFixtureMAC, Fixture: "rx-info-initial-channel-2",
			Responder: func(rpc *testutil.RPC, _ *testutil.Device) *http.Response {
				if rpc != nil && rpc.Method == "set_channel_id" {
					return testutil.DeviceResponse(body)
				}
				return nil
			},
		}))
		if err := client(net, time.Second).SetChannel(context.Background(), rxIP, 1); err != nil {
			t.Fatalf("%s: %v", fixture, err)
		}
	}
}

func TestReturnsFixtureUnchanged(t *testing.T) {
	net := testutil.NewNetwork()
	net.Place(rxIP, testutil.NewDevice(testutil.DeviceOptions{
		MAC: testutil.RxFixtureMAC, Fixture: "rx-info-initial-channel-2",
		Responder: func(*testutil.RPC, *testutil.Device) *http.Response {
			return testutil.DeviceResponse(testutil.FixtureText("rx-info-initial-channel-2"))
		},
	}))
	info, err := client(net, time.Second).GetDeviceInfo(context.Background(), rxIP)
	if err != nil {
		t.Fatal(err)
	}
	expected := testutil.FixtureResult("rx-info-initial-channel-2")
	raw, _ := json.Marshal(info.Raw)
	var got map[string]any
	_ = json.Unmarshal(raw, &got)
	if !reflect.DeepEqual(got, expected) {
		t.Fatalf("raw result differs from fixture")
	}
}

func respondingWith(resp *http.Response) *dt241m.HTTPClient {
	net := testutil.NewNetwork()
	net.Place(rxIP, testutil.NewDevice(testutil.DeviceOptions{
		MAC: testutil.RxFixtureMAC, Fixture: "rx-info-initial-channel-2",
		Responder: func(*testutil.RPC, *testutil.Device) *http.Response { return resp },
	}))
	return client(net, time.Second)
}

func TestSyntheticFailures(t *testing.T) {
	ctx := context.Background()
	cases := []struct {
		name string
		resp *http.Response
		call func(c *dt241m.HTTPClient) error
		code dt241m.Code
	}{
		{"malformed json", testutil.DeviceResponse("<html>oops</html>"), read, dt241m.CodeInvalidJSON},
		{"http status", testutil.StatusResponse(503, "busy"), read, dt241m.CodeHTTPStatus},
		{"rpc error", testutil.DeviceResponse(`{"jsonrpc":"2.0","id":1,"error":{"code":-32000,"message":"denied"}}`), write, dt241m.CodeRPCError},
		{"wrong id", testutil.DeviceResponse(`{"jsonrpc":"2.0","id":7,"result":{"result":true}}`), write, dt241m.CodeEnvelope},
		{"nested false", testutil.DeviceResponse(`{"jsonrpc":"2.0","id":1,"result":{"result":false}}`), write, dt241m.CodeNotAccepted},
		{"truthy outer result", testutil.DeviceResponse(`{"jsonrpc":"2.0","id":1,"result":{}}`), write, dt241m.CodeNotAccepted},
		{"result not object", testutil.DeviceResponse(`{"jsonrpc":"2.0","id":1,"result":true}`), write, dt241m.CodeResultShape},
		{"missing channel", testutil.DeviceResponse(`{"jsonrpc":"2.0","id":1,"result":{"lan_mac_addr":"FC:19:28:6C:D6:D8"}}`), read, dt241m.CodeResultShape},
		{"fractional channel", testutil.DeviceResponse(`{"jsonrpc":"2.0","id":1,"result":{"lan_mac_addr":"FC:19:28:6C:D6:D8","channel_id":2.5}}`), read, dt241m.CodeResultShape},
		{"string channel", testutil.DeviceResponse(`{"jsonrpc":"2.0","id":1,"result":{"lan_mac_addr":"FC:19:28:6C:D6:D8","channel_id":"2"}}`), read, dt241m.CodeResultShape},
	}
	_ = ctx
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			expectCode(t, tc.call(respondingWith(tc.resp)), tc.code)
		})
	}
}

func read(c *dt241m.HTTPClient) error {
	_, err := c.GetDeviceInfo(context.Background(), rxIP)
	return err
}

func write(c *dt241m.HTTPClient) error {
	return c.SetChannel(context.Background(), rxIP, 2)
}

func TestTimeoutIsDistinct(t *testing.T) {
	net := testutil.NewNetwork()
	net.Place(rxIP, testutil.NewDevice(testutil.DeviceOptions{MAC: testutil.RxFixtureMAC, Fixture: "rx-info-initial-channel-2", ReadHangs: true}))
	_, err := client(net, 20*time.Millisecond).GetDeviceInfo(context.Background(), rxIP)
	expectCode(t, err, dt241m.CodeTimeout)
}

func TestRefusedConnectionIsTransport(t *testing.T) {
	_, err := client(testutil.NewNetwork(), time.Second).GetDeviceInfo(context.Background(), "10.0.0.99")
	expectCode(t, err, dt241m.CodeTransport)
}

func TestChannelValidation(t *testing.T) {
	for _, ok := range []int{0, 1, 2, 255} {
		if !dt241m.ValidChannel(ok) {
			t.Fatalf("%d should be valid", ok)
		}
	}
	for _, bad := range []int{-1, 256, 1000} {
		if dt241m.ValidChannel(bad) {
			t.Fatalf("%d should be invalid", bad)
		}
	}
	net := network()
	c := client(net, time.Second)
	for _, bad := range []int{-1, 256} {
		expectCode(t, c.SetChannel(context.Background(), rxIP, bad), dt241m.CodeInput)
	}
	if len(net.Requests()) != 0 {
		t.Fatal("invalid channel reached the transport")
	}
	for _, edge := range []int{0, 255} {
		if err := c.SetChannel(context.Background(), rxIP, edge); err != nil {
			t.Fatal(err)
		}
	}
	writes := net.WritesTo(rxIP)
	if len(writes) != 2 || writes[0].ChannelParam() != 0 || writes[1].ChannelParam() != 255 {
		t.Fatalf("writes %+v", writes)
	}
}

func TestClassify(t *testing.T) {
	var tx, rx dt241m.DeviceInfo
	_ = json.Unmarshal(mustResult("tx-info-initial-channel-3"), &tx)
	_ = json.Unmarshal(mustResult("rx-info-initial-channel-2"), &rx)
	if dt241m.Classify(&tx) != dt241m.RoleTransmitter || dt241m.Classify(&rx) != dt241m.RoleReceiver {
		t.Fatal("fixture classification wrong")
	}
	str := func(s string) *string { return &s }
	cases := map[string]struct {
		info dt241m.DeviceInfo
		want dt241m.Role
	}{
		"missing":        {dt241m.DeviceInfo{}, dt241m.RoleUnknown},
		"conflicting":    {dt241m.DeviceInfo{ProductName: str("ProAVTx ET01"), Model: str("am_8270_proavrx-eth_er01-pway-dt241")}, dt241m.RoleUnknown},
		"unrelated":      {dt241m.DeviceInfo{ProductName: str("SomethingElse"), Model: str("generic")}, dt241m.RoleUnknown},
		"dev name alone": {dt241m.DeviceInfo{DevName: str("ER02_286CD6D8")}, dt241m.RoleUnknown},
		"model only":     {dt241m.DeviceInfo{Model: str("am_8270_proavrx-eth_er01-pway-dt241")}, dt241m.RoleReceiver},
	}
	for name, tc := range cases {
		if got := dt241m.Classify(&tc.info); got != tc.want {
			t.Fatalf("%s: got %s want %s", name, got, tc.want)
		}
	}
}

func mustResult(fixture string) []byte {
	data, _ := json.Marshal(testutil.FixtureResult(fixture))
	return data
}
