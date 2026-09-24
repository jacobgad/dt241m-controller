package config

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/netip"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/jacobgad/dt241m-controller/internal/cidr"
)

type Options struct {
	ScanRanges           []netip.Prefix
	PollInterval         time.Duration
	ProbeTimeout         time.Duration
	DiscoveryConcurrency int
	LogLevel             string
}

type MQTT struct {
	Host     string
	Port     int
	Username string
	Password string
	TLS      bool
}

type Config struct {
	Options      Options
	MQTT         MQTT
	DatabasePath string
}

type rawOptions struct {
	ScanRanges           []string `json:"scan_ranges"`
	PollIntervalSeconds  *int     `json:"poll_interval_seconds"`
	ProbeTimeoutMs       *int     `json:"probe_timeout_ms"`
	DiscoveryConcurrency *int     `json:"discovery_concurrency"`
	LogLevel             *string  `json:"log_level"`
}

func ParseOptions(data []byte) (Options, error) {
	var raw rawOptions
	if err := json.Unmarshal(data, &raw); err != nil {
		return Options{}, fmt.Errorf("options are not valid JSON: %w", err)
	}
	if len(raw.ScanRanges) == 0 {
		return Options{}, errors.New("scan_ranges: configure at least one scan range")
	}
	opts := Options{PollInterval: 15 * time.Second, ProbeTimeout: 2 * time.Second, DiscoveryConcurrency: 8, LogLevel: "info"}
	for _, text := range raw.ScanRanges {
		prefix, err := cidr.ParseScanRange(text)
		if err != nil {
			return Options{}, fmt.Errorf("scan_ranges: %w", err)
		}
		opts.ScanRanges = append(opts.ScanRanges, prefix)
	}
	if raw.PollIntervalSeconds != nil {
		if v := *raw.PollIntervalSeconds; v < 5 || v > 3600 {
			return Options{}, errors.New("poll_interval_seconds must be between 5 and 3600")
		}
		opts.PollInterval = time.Duration(*raw.PollIntervalSeconds) * time.Second
	}
	if raw.ProbeTimeoutMs != nil {
		if v := *raw.ProbeTimeoutMs; v < 200 || v > 30000 {
			return Options{}, errors.New("probe_timeout_ms must be between 200 and 30000")
		}
		opts.ProbeTimeout = time.Duration(*raw.ProbeTimeoutMs) * time.Millisecond
	}
	if raw.DiscoveryConcurrency != nil {
		if v := *raw.DiscoveryConcurrency; v < 1 || v > 64 {
			return Options{}, errors.New("discovery_concurrency must be between 1 and 64")
		}
		opts.DiscoveryConcurrency = *raw.DiscoveryConcurrency
	}
	if raw.LogLevel != nil {
		switch *raw.LogLevel {
		case "debug", "info", "warn", "error":
			opts.LogLevel = *raw.LogLevel
		default:
			return Options{}, errors.New("log_level must be one of debug, info, warn, error")
		}
	}
	return opts, nil
}

func MQTTFromEnv(getenv func(string) string) (MQTT, error) {
	host := getenv("MQTT_HOST")
	if host == "" {
		return MQTT{}, errors.New("MQTT_HOST is required")
	}
	m := MQTT{Host: host, Port: 1883, Username: getenv("MQTT_USERNAME"), Password: getenv("MQTT_PASSWORD")}
	if p := getenv("MQTT_PORT"); p != "" {
		port, err := strconv.Atoi(p)
		if err != nil || port < 1 || port > 65535 {
			return MQTT{}, fmt.Errorf("MQTT_PORT %q is not a valid port", p)
		}
		m.Port = port
	}
	switch strings.ToLower(getenv("MQTT_SSL")) {
	case "true", "1", "yes":
		m.TLS = true
	}
	return m, nil
}

const supervisorServicesURL = "http://supervisor/services/mqtt"

// MQTTFromSupervisor asks the Home Assistant Supervisor for the shared MQTT service.
func MQTTFromSupervisor(ctx context.Context, token string, client *http.Client) (MQTT, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, supervisorServicesURL, nil)
	if err != nil {
		return MQTT{}, err
	}
	req.Header.Set("Authorization", "Bearer "+token)
	resp, err := client.Do(req)
	if err != nil {
		return MQTT{}, fmt.Errorf("supervisor request failed: %w", err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<16))
	if resp.StatusCode != http.StatusOK {
		return MQTT{}, fmt.Errorf("supervisor returned HTTP %d for the MQTT service", resp.StatusCode)
	}
	var payload struct {
		Result string `json:"result"`
		Data   struct {
			Host     string `json:"host"`
			Port     int    `json:"port"`
			Username string `json:"username"`
			Password string `json:"password"`
			SSL      bool   `json:"ssl"`
		} `json:"data"`
	}
	if err := json.Unmarshal(body, &payload); err != nil || payload.Result != "ok" || payload.Data.Host == "" {
		return MQTT{}, errors.New("supervisor did not return a usable MQTT service; is the Mosquitto broker add-on running?")
	}
	return MQTT{Host: payload.Data.Host, Port: payload.Data.Port, Username: payload.Data.Username, Password: payload.Data.Password, TLS: payload.Data.SSL}, nil
}

func Load(ctx context.Context) (Config, error) {
	optionsPath := envOr("DT241M_OPTIONS_PATH", "/data/options.json")
	data, err := os.ReadFile(optionsPath)
	if err != nil {
		return Config{}, fmt.Errorf("read %s: %w", optionsPath, err)
	}
	opts, err := ParseOptions(data)
	if err != nil {
		return Config{}, err
	}
	var mqtt MQTT
	if os.Getenv("MQTT_HOST") != "" {
		mqtt, err = MQTTFromEnv(os.Getenv)
	} else if token := os.Getenv("SUPERVISOR_TOKEN"); token != "" {
		mqtt, err = MQTTFromSupervisor(ctx, token, &http.Client{Timeout: 10 * time.Second})
	} else {
		err = errors.New("no MQTT configuration: set MQTT_HOST or run under the Home Assistant Supervisor")
	}
	if err != nil {
		return Config{}, err
	}
	return Config{Options: opts, MQTT: mqtt, DatabasePath: envOr("DT241M_DATABASE_PATH", "/data/dt241m.sqlite")}, nil
}

func envOr(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}
