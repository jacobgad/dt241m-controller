package mqtt

import "testing"

func TestChannelPayloadParsing(t *testing.T) {
	t.Parallel()
	for input, want := range map[string]int{"2": 2, " 0 ": 0, "255": 255, "2.0": 2} {
		if got, ok := ParseChannelPayload(input); !ok || got != want {
			t.Fatalf("%q -> %d %v", input, got, ok)
		}
	}
	for _, bad := range []string{"2.5", "-1", "256", "two", ""} {
		if _, ok := ParseChannelPayload(bad); ok {
			t.Fatalf("accepted %q", bad)
		}
	}
}

func TestTopicParsing(t *testing.T) {
	t.Parallel()
	if DeviceIdentifier("fc:19:28:6c:d6:d8") != "dt241m:fc19286cd6d8" {
		t.Fatal("ha identifier wrong")
	}
	for topic, want := range map[string]DeviceCommand{
		"dt241m/device/fc19286cd6d8/name/set":    {MAC: "fc:19:28:6c:d6:d8", Command: "name"},
		"dt241m/device/fc19286cd6d8/channel/set": {MAC: "fc:19:28:6c:d6:d8", Command: "channel"},
		"dt241m/device/fc19286cd6d8/source/set":  {MAC: "fc:19:28:6c:d6:d8", Command: "source"},
	} {
		if cmd, ok := ParseDeviceCommand(topic); !ok || cmd != want {
			t.Fatalf("%s -> %+v %v", topic, cmd, ok)
		}
	}
	for _, bad := range []string{"dt241m/device/nope/channel/set", "dt241m/device/fc19286cd6d8/channel/state", "other/topic"} {
		if _, ok := ParseDeviceCommand(bad); ok {
			t.Fatalf("accepted %q", bad)
		}
	}
}
