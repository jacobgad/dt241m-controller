package mqtt

import (
	"bytes"
	"log/slog"
	"testing"

	"github.com/eclipse/paho.golang/paho"
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

func TestSubscriptionsRequestNoRetainedReplay(t *testing.T) {
	t.Parallel()
	sub := subscription(Subscriptions)
	if len(sub.Subscriptions) != len(Subscriptions) {
		t.Fatalf("subscriptions %d, want %d", len(sub.Subscriptions), len(Subscriptions))
	}
	for _, s := range sub.Subscriptions {
		if s.RetainHandling != neverReplayRetained {
			t.Errorf("%s: retain handling %d, want %d", s.Topic, s.RetainHandling, neverReplayRetained)
		}
		if s.QoS != 1 {
			t.Errorf("%s: qos %d, want 1", s.Topic, s.QoS)
		}
	}
}

func TestRetainedDeliveriesNeverReachHandlers(t *testing.T) {
	t.Parallel()
	log := slog.New(slog.NewTextHandler(&bytes.Buffer{}, nil))
	pc := &pahoConnection{log: log}
	var delivered []string
	pc.OnMessage(func(topic string, payload []byte) {
		delivered = append(delivered, topic+"="+string(payload))
	})
	cfg, err := clientConfig(PahoOptions{
		Settings: config.MQTT{Host: "core-mosquitto", Port: 1883},
		ClientID: "dt241m-test",
		Will:     Will{Topic: ControllerAvailability, Payload: PayloadOffline},
		Log:      log,
	}, pc)
	if err != nil {
		t.Fatal(err)
	}
	receive := cfg.OnPublishReceived[0]

	handled, err := receive(paho.PublishReceived{Packet: &paho.Publish{
		Topic: "dt241m/device/fc19286cd6d8/channel/set", Payload: []byte("3"), Retain: true,
	}})
	if err != nil || !handled {
		t.Fatalf("retained delivery handled=%v err=%v", handled, err)
	}
	if len(delivered) != 0 {
		t.Fatalf("retained delivery reached handlers: %v", delivered)
	}

	if _, err := receive(paho.PublishReceived{Packet: &paho.Publish{
		Topic: "dt241m/device/fc19286cd6d8/channel/set", Payload: []byte("3"),
	}}); err != nil {
		t.Fatal(err)
	}
	if len(delivered) != 1 || delivered[0] != "dt241m/device/fc19286cd6d8/channel/set=3" {
		t.Fatalf("live delivery %v", delivered)
	}
}
