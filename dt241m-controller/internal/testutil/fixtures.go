// Package testutil provides the offline doubles the tests run against: a simulated LAN of
// DT241M devices behind an http.RoundTripper, a recording MQTT connection and an
// in-memory store. Fixtures are the captured hardware responses in ../../fixtures.
package testutil

import (
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
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
