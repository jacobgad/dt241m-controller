package config_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"strings"
	"testing"
	"time"

	"github.com/jacobgad/dt241m-controller/internal/cidr"
	"github.com/jacobgad/dt241m-controller/internal/config"
)

func TestOptionsDefaultsAndMultipleRanges(t *testing.T) {
	opts, err := config.ParseOptions([]byte(`{"scan_ranges":["192.168.40.0/24","10.10.5.0/25"]}`))
	if err != nil {
		t.Fatal(err)
	}
	if opts.PollInterval != 15*time.Second || opts.ProbeTimeout != 2*time.Second || opts.DiscoveryConcurrency != 8 || opts.LogLevel != "info" {
		t.Fatalf("defaults %+v", opts)
	}
	if got := strings.Join(cidr.Texts(opts.ScanRanges), ","); got != "192.168.40.0/24,10.10.5.0/25" {
		t.Fatalf("ranges %s", got)
	}
}

func TestOptionsRejections(t *testing.T) {
	cases := map[string]string{
		`{"scan_ranges":[]}`:                                           "at least one",
		`{"scan_ranges":["8.8.8.0/24"]}`:                               "private",
		`{"scan_ranges":["10.0.0.0/8"]}`:                               "too large",
		`{"scan_ranges":["not-a-cidr"]}`:                               "valid IPv4 CIDR",
		`{"scan_ranges":["192.168.1.0/24"],"poll_interval_seconds":1}`: "poll_interval_seconds",
		`{"scan_ranges":["192.168.1.0/24"],"discovery_concurrency":0}`: "discovery_concurrency",
		`{"scan_ranges":["192.168.1.0/24"],"log_level":"loud"}`:        "log_level",
	}
	for input, want := range cases {
		_, err := config.ParseOptions([]byte(input))
		if err == nil || !strings.Contains(err.Error(), want) {
			t.Fatalf("%s: got %v, want %q", input, err, want)
		}
	}
}

func TestCIDRExpansion(t *testing.T) {
	p, _ := cidr.ParseScanRange("192.168.40.17/24")
	if p.String() != "192.168.40.0/24" {
		t.Fatalf("not normalised: %s", p)
	}
	hosts := cidr.Hosts(p)
	if len(hosts) != 254 || hosts[0] != "192.168.40.1" || hosts[253] != "192.168.40.254" {
		t.Fatalf("hosts %d %s..%s", len(hosts), hosts[0], hosts[len(hosts)-1])
	}
	p31, _ := cidr.ParseScanRange("10.0.0.4/31")
	p32, _ := cidr.ParseScanRange("10.0.0.9/32")
	if h := cidr.Hosts(p31); len(h) != 2 || h[0] != "10.0.0.4" || h[1] != "10.0.0.5" {
		t.Fatalf("/31 %v", h)
	}
	if h := cidr.Hosts(p32); len(h) != 1 || h[0] != "10.0.0.9" {
		t.Fatalf("/32 %v", h)
	}
	a, _ := cidr.ParseScanRange("10.0.0.0/30")
	b, _ := cidr.ParseScanRange("10.0.0.0/29")
	if all := cidr.ExpandAll([]netip.Prefix{a, b}); len(all) != 6 || all[0] != "10.0.0.1" || all[5] != "10.0.0.6" {
		t.Fatalf("dedupe %v", all)
	}
}

func TestMQTTFromEnv(t *testing.T) {
	env := map[string]string{"MQTT_HOST": "core-mosquitto", "MQTT_PORT": "1883", "MQTT_USERNAME": "addons", "MQTT_PASSWORD": "secret", "MQTT_SSL": "false"}
	m, err := config.MQTTFromEnv(func(k string) string { return env[k] })
	if err != nil || m != (config.MQTT{Host: "core-mosquitto", Port: 1883, Username: "addons", Password: "secret"}) {
		t.Fatalf("%+v %v", m, err)
	}
	if _, err := config.MQTTFromEnv(func(string) string { return "" }); err == nil {
		t.Fatal("host should be required")
	}
	if m, _ := config.MQTTFromEnv(func(k string) string { return map[string]string{"MQTT_HOST": "b"}[k] }); m.Port != 1883 {
		t.Fatal("default port")
	}
}

func TestMQTTFromSupervisor(t *testing.T) {
	var gotAuth string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		w.Write([]byte(`{"result":"ok","data":{"host":"core-mosquitto","port":1883,"username":"addons","password":"pw","ssl":false,"protocol":"3.1.1","addon":"core_mosquitto"}}`))
	}))
	defer srv.Close()
	client := &http.Client{Transport: rewriteHost(srv.URL)}
	m, err := config.MQTTFromSupervisor(context.Background(), "tok", client)
	if err != nil || m.Host != "core-mosquitto" || m.Password != "pw" || gotAuth != "Bearer tok" {
		t.Fatalf("%+v %v auth=%q", m, err, gotAuth)
	}

	down := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(400)
		w.Write([]byte(`{"result":"error"}`))
	}))
	defer down.Close()
	if _, err := config.MQTTFromSupervisor(context.Background(), "tok", &http.Client{Transport: rewriteHost(down.URL)}); err == nil {
		t.Fatal("expected error when service unavailable")
	}
}

type rewriteHost string

func (r rewriteHost) RoundTrip(req *http.Request) (*http.Response, error) {
	target := strings.TrimPrefix(string(r), "http://")
	req.URL.Host = target
	return http.DefaultTransport.RoundTrip(req)
}
