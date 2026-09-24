package controller_test

import (
	"encoding/json"
	"flag"
	"os"
	"path/filepath"
	"sort"
	"testing"

	"github.com/jacobgad/dt241m-controller/internal/testutil"
)

var updateGolden = flag.Bool("update", false, "rewrite golden discovery payloads")

// TestDiscoveryPayloadsMatchGolden pins every Home Assistant discovery config byte-for-byte
// (after key sorting) so refactors of the payload builders cannot drift silently.
func TestDiscoveryPayloadsMatchGolden(t *testing.T) {
	t.Parallel()
	h := newHarness(t, harnessOptions{})
	txNamed(txCamera, "192.168.1.2", "Stage Camera", 1, h.net)
	txNamed(txLectern, "192.168.1.3", "Lectern PC", 2, h.net)
	rx(testutil.RxFixtureMAC, rxIP, h.net)
	rx("cc:cc:cc:cc:cc:cc", "192.168.1.6", h.net, func(o *testutil.DeviceOptions) {
		o.Overrides = map[string]any{"product_name": "Mystery", "model": "unknown"}
	})
	if err := h.ctrl.Start(h.ctx); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { h.ctrl.Stop(h.ctx) })

	configs := h.mqtt.DiscoveryConfigs()
	topics := make([]string, 0, len(configs))
	for topic := range configs {
		topics = append(topics, topic)
	}
	sort.Strings(topics)
	snapshot := make([]map[string]any, 0, len(topics))
	for _, topic := range topics {
		snapshot = append(snapshot, map[string]any{"topic": topic, "payload": configs[topic]})
	}
	got, err := json.MarshalIndent(snapshot, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	got = append(got, '\n')

	path := filepath.Join("testdata", "discovery.golden.json")
	if *updateGolden {
		if err := os.WriteFile(path, got, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	want, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("missing golden file; run with -update: %v", err)
	}
	if string(got) != string(want) {
		t.Fatalf("discovery payloads differ from %s (run `go test ./internal/controller -run Golden -update` after an intentional change)", path)
	}
}
