package mqtt

import (
	"context"
	"crypto/tls"
	"fmt"
	"log/slog"
	"net/url"
	"sync"
	"time"

	"github.com/eclipse/paho.golang/autopaho"
	"github.com/eclipse/paho.golang/paho"
	"github.com/jacobgad/dt241m-controller/internal/config"
)

type PahoOptions struct {
	Settings config.MQTT
	ClientID string
	Will     struct{ Topic, Payload string }
	Log      *slog.Logger
}

type pahoConnection struct {
	cm        *autopaho.ConnectionManager
	cancel    context.CancelFunc
	log       *slog.Logger
	mu        sync.RWMutex
	connected bool
	onMessage []MessageHandler
	onConnect []func()
}

// Connect starts a self-reconnecting session that lives until Close; ctx only bounds the initial setup.
func Connect(ctx context.Context, opts PahoOptions) (Connection, error) {
	scheme := "mqtt"
	var tlsCfg *tls.Config
	if opts.Settings.TLS {
		scheme = "tls"
		tlsCfg = &tls.Config{MinVersion: tls.VersionTLS12}
	}
	serverURL, err := url.Parse(fmt.Sprintf("%s://%s:%d", scheme, opts.Settings.Host, opts.Settings.Port))
	if err != nil {
		return nil, err
	}
	pc := &pahoConnection{log: opts.Log}

	cfg := autopaho.ClientConfig{
		ServerUrls:                    []*url.URL{serverURL},
		TlsCfg:                        tlsCfg,
		KeepAlive:                     30,
		CleanStartOnInitialConnection: true,
		SessionExpiryInterval:         0,
		ConnectRetryDelay:             5 * time.Second,
		ConnectTimeout:                10 * time.Second,
		OnConnectionUp: func(_ *autopaho.ConnectionManager, _ *paho.Connack) {
			opts.Log.Info("mqtt_connected", "host", opts.Settings.Host, "port", opts.Settings.Port)
			pc.setConnected(true)
			for _, h := range pc.connectHandlers() {
				go h()
			}
		},
		OnConnectionDown: func() bool {
			opts.Log.Warn("mqtt_disconnected")
			pc.setConnected(false)
			return true
		},
		OnConnectError: func(err error) {
			opts.Log.Error("mqtt_connect_error", "error", err.Error())
		},
		ClientConfig: paho.ClientConfig{
			ClientID: opts.ClientID,
			OnPublishReceived: []func(paho.PublishReceived) (bool, error){
				func(pr paho.PublishReceived) (bool, error) {
					for _, h := range pc.messageHandlers() {
						h(pr.Packet.Topic, pr.Packet.Payload)
					}
					return true, nil
				},
			},
			OnClientError: func(err error) { opts.Log.Warn("mqtt_client_error", "error", err.Error()) },
		},
	}
	if opts.Settings.Username != "" || opts.Settings.Password != "" {
		cfg.SetUsernamePassword(opts.Settings.Username, []byte(opts.Settings.Password))
	}
	cfg.SetWillMessage(opts.Will.Topic, []byte(opts.Will.Payload), 1, true)

	opts.Log.Info("mqtt_connecting", "host", opts.Settings.Host, "port", opts.Settings.Port, "tls", opts.Settings.TLS)
	sessionCtx, cancel := context.WithCancel(context.WithoutCancel(ctx))
	cm, err := autopaho.NewConnection(sessionCtx, cfg)
	if err != nil {
		cancel()
		return nil, err
	}
	pc.cm = cm
	pc.cancel = cancel
	return pc, nil
}

func (p *pahoConnection) setConnected(v bool) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.connected = v
}

func (p *pahoConnection) connectHandlers() []func() {
	p.mu.RLock()
	defer p.mu.RUnlock()
	return append([]func(){}, p.onConnect...)
}

func (p *pahoConnection) messageHandlers() []MessageHandler {
	p.mu.RLock()
	defer p.mu.RUnlock()
	return append([]MessageHandler{}, p.onMessage...)
}

func (p *pahoConnection) Publish(ctx context.Context, topic string, payload string, retain bool) error {
	if !p.Connected() {
		return fmt.Errorf("mqtt not connected")
	}
	_, err := p.cm.Publish(ctx, &paho.Publish{Topic: topic, QoS: 1, Retain: retain, Payload: []byte(payload)})
	return err
}

func (p *pahoConnection) Subscribe(ctx context.Context, topics []string) error {
	subs := make([]paho.SubscribeOptions, len(topics))
	for i, t := range topics {
		subs[i] = paho.SubscribeOptions{Topic: t, QoS: 1}
	}
	_, err := p.cm.Subscribe(ctx, &paho.Subscribe{Subscriptions: subs})
	return err
}

func (p *pahoConnection) OnMessage(handler MessageHandler) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.onMessage = append(p.onMessage, handler)
}

func (p *pahoConnection) OnConnect(handler func()) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.onConnect = append(p.onConnect, handler)
}

func (p *pahoConnection) Connected() bool {
	p.mu.RLock()
	defer p.mu.RUnlock()
	return p.connected
}

func (p *pahoConnection) Close(ctx context.Context) error {
	defer p.cancel()
	err := p.cm.Disconnect(ctx)
	select {
	case <-p.cm.Done():
	case <-ctx.Done():
	}
	return err
}
