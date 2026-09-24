import { describe, expect, it } from "vitest";
import { probeAddresses } from "../discovery.js";
import { createDt241mClient } from "../dt241m/client.js";
import { deviceTopics } from "../mqtt/topics.js";
import { RX_FIXTURE_MAC, TX_FIXTURE_MAC } from "./helpers/fixtures.js";
import { createHarness } from "./helpers/harness.js";
import { SimulatedDevice, SimulatedNetwork } from "./helpers/simulated-network.js";

const RX_A_MAC = "fc:19:28:6c:d6:d8";
const RX_B_MAC = "fc:19:28:6c:aa:01";

const ips = Array.from({ length: 14 }, (_, i) => `192.168.1.${i + 1}`);

describe("probeAddresses", () => {
  it("discovers one responsive device among unresponsive addresses", async () => {
    const net = new SimulatedNetwork();
    net.place("192.168.1.5", new SimulatedDevice({ mac: RX_FIXTURE_MAC, fixture: "rx-info-initial-channel-2" }));
    const client = createDt241mClient({ fetchImpl: net.fetch });
    const hits = await probeAddresses(client, ips, { concurrency: 4, timeoutMs: 100 });
    expect(hits).toHaveLength(1);
    expect(hits[0]!.ip).toBe("192.168.1.5");
    expect(net.requests).toHaveLength(ips.length);
  });

  it("discovers multiple devices", async () => {
    const net = new SimulatedNetwork();
    net.place("192.168.1.5", new SimulatedDevice({ mac: RX_FIXTURE_MAC, fixture: "rx-info-initial-channel-2" }));
    net.place("192.168.1.9", new SimulatedDevice({ mac: TX_FIXTURE_MAC, fixture: "tx-info-initial-channel-3" }));
    const client = createDt241mClient({ fetchImpl: net.fetch });
    const hits = await probeAddresses(client, ips, { concurrency: 4, timeoutMs: 100 });
    expect(hits.map((h) => h.ip).sort()).toEqual(["192.168.1.5", "192.168.1.9"]);
  });

  it("continues past hanging addresses without aborting the scan", async () => {
    const net = new SimulatedNetwork();
    net.unreachableMode = "hang";
    net.place("192.168.1.14", new SimulatedDevice({ mac: RX_FIXTURE_MAC, fixture: "rx-info-initial-channel-2" }));
    const client = createDt241mClient({ fetchImpl: net.fetch });
    const hits = await probeAddresses(client, ips, { concurrency: 8, timeoutMs: 30 });
    expect(hits.map((h) => h.ip)).toEqual(["192.168.1.14"]);
  });

  it("respects the concurrency limit", async () => {
    const net = new SimulatedNetwork();
    net.unreachableMode = "hang";
    const client = createDt241mClient({ fetchImpl: net.fetch });
    let peak = 0;
    await probeAddresses(client, ips, {
      concurrency: 3,
      timeoutMs: 20,
      onProbeStart: (_ip, active) => {
        peak = Math.max(peak, active);
      }
    });
    expect(peak).toBe(3);
  });
});

describe("Controller discovery", () => {
  it("registers devices, classifies them, and publishes MQTT discovery", async () => {
    const h = createHarness();
    h.network.place("192.168.1.5", new SimulatedDevice({ mac: RX_FIXTURE_MAC, fixture: "rx-info-initial-channel-2" }));
    h.network.place("192.168.1.9", new SimulatedDevice({ mac: TX_FIXTURE_MAC, fixture: "tx-info-initial-channel-3" }));
    await h.controller.runDiscovery("test");

    const rx = h.controller.registry.get(RX_FIXTURE_MAC)!;
    const tx = h.controller.registry.get(TX_FIXTURE_MAC)!;
    expect(rx.role).toBe("receiver");
    expect(rx.ip).toBe("192.168.1.5");
    expect(tx.role).toBe("transmitter");
    expect(tx.ip).toBe("192.168.1.9");

    const configs = h.mqtt.discoveryConfigs();
    expect(configs.has("homeassistant/number/dt241m_fc19286cd6d8/channel/config")).toBe(true);
    expect(configs.has("homeassistant/sensor/dt241m_fc19286cd291/channel/config")).toBe(true);
    expect(h.mqtt.lastOn(deviceTopics(RX_FIXTURE_MAC).channelState)?.payload).toBe("2");
    expect(h.mqtt.lastOn(deviceTopics(TX_FIXTURE_MAC).channelState)?.payload).toBe("3");
    expect(h.mqtt.lastOn("dt241m/controller/known_devices/state")?.payload).toBe("2");
    expect(h.mqtt.lastOn("dt241m/controller/online_devices/state")?.payload).toBe("2");
  });

  it("updates the existing device when the same MAC appears at a new IP", async () => {
    const h = createHarness();
    h.network.place("192.168.1.5", new SimulatedDevice({ mac: RX_FIXTURE_MAC, fixture: "rx-info-initial-channel-2" }));
    await h.controller.runDiscovery("first");
    h.network.move("192.168.1.5", "192.168.1.11");
    await h.controller.runDiscovery("second");

    expect(h.controller.registry.all()).toHaveLength(1);
    expect(h.controller.registry.get(RX_FIXTURE_MAC)?.ip).toBe("192.168.1.11");
    expect(h.mqtt.lastOn(deviceTopics(RX_FIXTURE_MAC).ipState)?.payload).toBe("192.168.1.11");
    expect(h.mqtt.lastOn("dt241m/controller/known_devices/state")?.payload).toBe("1");
  });

  it("does not mutate the old identity when its IP later answers with a different MAC", async () => {
    const h = createHarness();
    h.network.place("192.168.1.5", new SimulatedDevice({ mac: RX_A_MAC, fixture: "rx-info-initial-channel-2" }));
    await h.controller.runDiscovery("first");

    h.network.remove("192.168.1.5");
    h.network.place("192.168.1.5", new SimulatedDevice({ mac: RX_B_MAC, fixture: "rx-info-initial-channel-2", overrides: { dev_name: "ER02_B" } }));
    await h.controller.runDiscovery("second");

    const a = h.controller.registry.get(RX_A_MAC)!;
    const b = h.controller.registry.get(RX_B_MAC)!;
    expect(a.id).toBe("dt241m_fc19286cd6d8");
    expect(a.reportedName).toBe("ER02_286CD6D8");
    expect(a.online).toBe(false);
    expect(b.id).toBe("dt241m_fc19286caa01");
    expect(b.ip).toBe("192.168.1.5");
    expect(h.mqtt.lastOn(deviceTopics(RX_A_MAC).availability)?.payload).toBe("offline");
    expect(h.mqtt.lastOn(deviceTopics(RX_B_MAC).availability)?.payload).toBe("online");
  });

  it("repeated discovery does not duplicate the Home Assistant device identity", async () => {
    const h = createHarness();
    h.network.place("192.168.1.5", new SimulatedDevice({ mac: RX_FIXTURE_MAC, fixture: "rx-info-initial-channel-2" }));
    await h.controller.runDiscovery("one");
    await h.controller.runDiscovery("two");
    await h.controller.runDiscovery("three");

    const configs = [...h.mqtt.discoveryConfigs().entries()].filter(([topic]) => topic.includes("dt241m_fc19286cd6d8"));
    const uniqueIds = new Set(configs.map(([, payload]) => payload["unique_id"]));
    expect(configs.map(([topic]) => topic).sort()).toEqual([
      "homeassistant/number/dt241m_fc19286cd6d8/channel/config",
      "homeassistant/sensor/dt241m_fc19286cd6d8/ip_address/config",
      "homeassistant/sensor/dt241m_fc19286cd6d8/role/config",
      "homeassistant/text/dt241m_fc19286cd6d8/name/config"
    ]);
    expect(uniqueIds.size).toBe(4);
    for (const [, payload] of configs) {
      expect((payload["device"] as { identifiers: string[] }).identifiers).toEqual(["dt241m:fc19286cd6d8"]);
    }
  });

  it("does not run overlapping discovery scans", async () => {
    const h = createHarness();
    h.network.unreachableMode = "hang";
    const first = h.controller.runDiscovery("a");
    const second = h.controller.runDiscovery("b");
    expect(h.controller.isDiscoveryRunning()).toBe(true);
    await Promise.all([first, second]);
    expect(h.controller.discoveryRunCount()).toBe(1);
    expect(h.controller.isDiscoveryRunning()).toBe(false);
  });

  it("only probes addresses inside the configured ranges", async () => {
    const h = createHarness();
    await h.controller.runDiscovery("bounded");
    const probed = new Set(h.network.requests.map((r) => r.ip));
    expect(probed.size).toBe(14);
    for (const ip of probed) expect(ip.startsWith("192.168.1.")).toBe(true);
    expect(probed.has("192.168.1.0")).toBe(false);
    expect(probed.has("192.168.1.15")).toBe(false);
  });
});

describe("Controller polling", () => {
  it("publishes a channel changed through physical controls", async () => {
    const h = createHarness();
    const device = h.network.place("192.168.1.5", new SimulatedDevice({ mac: RX_FIXTURE_MAC, fixture: "rx-info-initial-channel-2" }));
    await h.controller.runDiscovery("start");
    h.mqtt.clear();

    device.reportedChannel = 6;
    device.frontPanelChannel = 6;
    await h.controller.pollKnownDevices();

    expect(h.controller.registry.get(RX_FIXTURE_MAC)?.channel).toBe(6);
    expect(h.mqtt.lastOn(deviceTopics(RX_FIXTURE_MAC).channelState)?.payload).toBe("6");
    expect(h.network.writesTo("192.168.1.5")).toHaveLength(0);
  });

  it("never reapplies channel settings while polling", async () => {
    const h = createHarness();
    const device = h.network.place("192.168.1.5", new SimulatedDevice({ mac: RX_FIXTURE_MAC, fixture: "rx-info-initial-channel-2" }));
    await h.controller.runDiscovery("start");
    await h.controller.requestChannelChange(RX_FIXTURE_MAC, 4);
    device.reportedChannel = 1;
    await h.controller.pollKnownDevices();
    await h.controller.pollKnownDevices();
    expect(h.network.writesTo("192.168.1.5")).toHaveLength(1);
    expect(h.controller.registry.get(RX_FIXTURE_MAC)?.channel).toBe(1);
  });

  it("marks a vanished device unavailable, rediscovers it at a new IP and keeps one HA device", async () => {
    const h = createHarness();
    h.network.place("192.168.1.5", new SimulatedDevice({ mac: RX_FIXTURE_MAC, fixture: "rx-info-initial-channel-2" }));
    await h.controller.runDiscovery("start");
    const runsBefore = h.controller.discoveryRunCount();

    h.network.move("192.168.1.5", "192.168.1.12");
    h.mqtt.clear();
    await h.controller.pollKnownDevices();

    const availability = h.mqtt.messagesOn(deviceTopics(RX_FIXTURE_MAC).availability).map((m) => m.payload);
    expect(availability).toEqual(["offline", "online"]);
    expect(h.controller.discoveryRunCount()).toBe(runsBefore + 1);
    expect(h.controller.registry.get(RX_FIXTURE_MAC)?.ip).toBe("192.168.1.12");
    expect(h.controller.registry.all()).toHaveLength(1);
    expect(h.mqtt.lastOn(deviceTopics(RX_FIXTURE_MAC).ipState)?.payload).toBe("192.168.1.12");
  });

  it("leaves a device offline when it cannot be found anywhere", async () => {
    const h = createHarness();
    h.network.place("192.168.1.5", new SimulatedDevice({ mac: RX_FIXTURE_MAC, fixture: "rx-info-initial-channel-2" }));
    await h.controller.runDiscovery("start");
    h.network.remove("192.168.1.5");
    await h.controller.pollKnownDevices();
    expect(h.controller.registry.get(RX_FIXTURE_MAC)?.online).toBe(false);
    expect(h.mqtt.lastOn(deviceTopics(RX_FIXTURE_MAC).availability)?.payload).toBe("offline");
    expect(h.mqtt.lastOn("dt241m/controller/online_devices/state")?.payload).toBe("0");
    expect(h.mqtt.lastOn("dt241m/controller/known_devices/state")?.payload).toBe("1");
  });
});
