package mqtt

import (
	"bytes"
	"log/slog"
	"testing"

	"github.com/jacobgad/dt241m-controller/internal/config"
)

func TestClientConfigSetsRetainedLastWillAndCredentials(t *testing.T) {
	t.Parallel()
	var logs bytes.Buffer
	opts := PahoOptions{
		Settings: config.MQTT{Host: "core-mosquitto", Port: 1883, Username: "addons", Password: "s3cret"},
		ClientID: "dt241m-test",
		Will:     Will{Topic: ControllerAvailability, Payload: PayloadOffline},
		Log:      slog.New(slog.NewTextHandler(&logs, nil)),
	}
	cfg, err := clientConfig(opts, &pahoConnection{log: opts.Log})
	if err != nil {
		t.Fatal(err)
	}
	if got := cfg.ServerUrls[0].String(); got != "mqtt://core-mosquitto:1883" {
		t.Fatalf("url %s", got)
	}
	if cfg.WillMessage == nil || cfg.WillMessage.Topic != ControllerAvailability || string(cfg.WillMessage.Payload) != PayloadOffline || !cfg.WillMessage.Retain || cfg.WillMessage.QoS != 1 {
		t.Fatalf("will %+v", cfg.WillMessage)
	}
	if cfg.ConnectUsername != "addons" || string(cfg.ConnectPassword) != "s3cret" {
		t.Fatal("credentials not applied")
	}
	if cfg.ClientID != "dt241m-test" || !cfg.CleanStartOnInitialConnection {
		t.Fatalf("client config %+v", cfg.ClientConfig)
	}
	if bytes.Contains(logs.Bytes(), []byte("s3cret")) {
		t.Fatal("password leaked into logs")
	}

	tlsOpts := opts
	tlsOpts.Settings.TLS = true
	tlsOpts.Settings.Port = 8883
	tlsCfg, _ := clientConfig(tlsOpts, &pahoConnection{log: opts.Log})
	if tlsCfg.ServerUrls[0].Scheme != "tls" || tlsCfg.TlsCfg == nil {
		t.Fatal("tls not configured")
	}
}
