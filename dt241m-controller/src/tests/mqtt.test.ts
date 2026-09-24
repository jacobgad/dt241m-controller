import { EventEmitter } from "node:events";
import { describe, expect, it, vi } from "vitest";
import { HA_STATUS_TOPIC, controllerTopics, deviceTopics } from "../mqtt/topics.js";
import { RX_FIXTURE_MAC, TX_FIXTURE_MAC } from "./helpers/fixtures.js";
import { createHarness } from "./helpers/harness.js";
import { SimulatedDevice } from "./helpers/simulated-network.js";

const RX_IP = "192.168.1.5";
const TX_IP = "192.168.1.9";
const RX_ID = "dt241m_fc19286cd6d8";
const TX_ID = "dt241m_fc19286cd291";

async function discoveredHarness() {
  const h = createHarness();
  const rx = h.network.place(RX_IP, new SimulatedDevice({ mac: RX_FIXTURE_MAC, fixture: "rx-info-initial-channel-2" }));
  const tx = h.network.place(TX_IP, new SimulatedDevice({ mac: TX_FIXTURE_MAC, fixture: "tx-info-initial-channel-3" }));
  await h.controller.start();
  return { h, rx, tx };
}

const flush = () => new Promise((resolve) => setTimeout(resolve, 10));

describe("MQTT discovery payloads", () => {
  it("publishes a receiver Channel number entity with MAC-based ids and non-optimistic mode", async () => {
    const { h } = await discoveredHarness();
    const config = h.mqtt.discoveryConfigs().get(`homeassistant/number/${RX_ID}/channel/config`)!;
    expect(config).toMatchObject({
      name: "Channel",
      unique_id: `${RX_ID}_channel`,
      state_topic: `dt241m/device/fc19286cd6d8/channel/state`,
      command_topic: `dt241m/device/fc19286cd6d8/channel/set`,
      min: 0,
      max: 255,
      step: 1,
      mode: "box",
      optimistic: false,
      retain: false,
      availability_mode: "all"
    });
    expect(config["device"]).toMatchObject({
      identifiers: ["dt241m:fc19286cd6d8"],
      connections: [["mac", RX_FIXTURE_MAC]],
      name: "ER02_286CD6D8",
      manufacturer: "PWAY",
      model: "ProAVRx ER01",
      sw_version: "1.13471.133"
    });
    const availability = config["availability"] as Array<{ topic: string }>;
    expect(availability.map((a) => a.topic)).toEqual([controllerTopics.availability, deviceTopics(RX_FIXTURE_MAC).availability]);
  });

  it("publishes a transmitter Channel sensor without a command topic", async () => {
    const { h } = await discoveredHarness();
    const configs = h.mqtt.discoveryConfigs();
    const config = configs.get(`homeassistant/sensor/${TX_ID}/channel/config`)!;
    expect(config).toMatchObject({
      name: "Channel",
      unique_id: `${TX_ID}_channel`,
      state_topic: `dt241m/device/fc19286cd291/channel/state`
    });
    expect(config["command_topic"]).toBeUndefined();
    expect(configs.has(`homeassistant/number/${TX_ID}/channel/config`)).toBe(false);
    expect(config["device"]).toMatchObject({ identifiers: ["dt241m:fc19286cd291"], name: "ET01_286CD291", model: "ProAVTx ET01" });
  });

  it("publishes the controller rescan button and count sensors", async () => {
    const { h } = await discoveredHarness();
    const configs = h.mqtt.discoveryConfigs();
    const button = configs.get("homeassistant/button/dt241m_controller/rescan/config")!;
    expect(button).toMatchObject({
      name: "Rescan network",
      unique_id: "dt241m_controller_rescan",
      command_topic: controllerTopics.rescanPress,
      payload_press: "PRESS",
      retain: false
    });
    expect(button["device"]).toMatchObject({ identifiers: ["dt241m:controller"] });
    expect(configs.get("homeassistant/sensor/dt241m_controller/known_devices/config")).toMatchObject({
      state_topic: controllerTopics.knownDevicesState
    });
    expect(configs.get("homeassistant/sensor/dt241m_controller/online_devices/config")).toMatchObject({
      state_topic: controllerTopics.onlineDevicesState
    });
  });

  it("never includes IP addresses or names in topics and keeps unique ids stable across IP changes", async () => {
    const { h } = await discoveredHarness();
    const before = h.mqtt.discoveryConfigs();
    h.network.move(RX_IP, "192.168.1.12");
    await h.controller.pollKnownDevices();
    const after = h.mqtt.discoveryConfigs();

    for (const [topic, payload] of after) {
      expect(topic).not.toMatch(/192\.168/);
      expect(payload["unique_id"]).toBe(before.get(topic)?.["unique_id"]);
    }
    for (const message of h.mqtt.published) {
      expect(message.topic).not.toMatch(/\d+\.\d+\.\d+\.\d+/);
      expect(message.topic).not.toMatch(/ER02|ET01/);
    }
    expect(h.mqtt.lastOn(deviceTopics(RX_FIXTURE_MAC).ipState)?.payload).toBe("192.168.1.12");
  });
});

describe("MQTT runtime behaviour", () => {
  it("publishes controller and device availability", async () => {
    const { h } = await discoveredHarness();
    expect(h.mqtt.lastOn(controllerTopics.availability)).toMatchObject({ payload: "online", retain: true });
    expect(h.mqtt.lastOn(deviceTopics(RX_FIXTURE_MAC).availability)).toMatchObject({ payload: "online", retain: true });
    expect(h.mqtt.lastOn(deviceTopics(TX_FIXTURE_MAC).availability)).toMatchObject({ payload: "online", retain: true });
  });

  it("subscribes to command topics and the Home Assistant status topic", async () => {
    const { h } = await discoveredHarness();
    expect(h.mqtt.subscriptions).toEqual(
      expect.arrayContaining(["dt241m/device/+/channel/set", "dt241m/device/+/name/set", controllerTopics.rescanPress, HA_STATUS_TOPIC])
    );
  });

  it("an MQTT command changes the receiver channel and publishes the readback", async () => {
    const { h, rx } = await discoveredHarness();
    h.mqtt.deliver(deviceTopics(RX_FIXTURE_MAC).channelSet, "5");
    await flush();
    expect(rx.reportedChannel).toBe(5);
    expect(h.network.writesTo(RX_IP)).toHaveLength(1);
    expect(h.mqtt.lastOn(deviceTopics(RX_FIXTURE_MAC).channelState)).toMatchObject({ payload: "5", retain: true });
  });

  it("ignores invalid channel payloads and commands to transmitters", async () => {
    const { h, tx } = await discoveredHarness();
    h.mqtt.deliver(deviceTopics(RX_FIXTURE_MAC).channelSet, "abc");
    h.mqtt.deliver(deviceTopics(RX_FIXTURE_MAC).channelSet, "300");
    h.mqtt.deliver(deviceTopics(TX_FIXTURE_MAC).channelSet, "2");
    await flush();
    expect(h.network.requests.filter((r) => r.rpc?.method === "set_channel_id")).toHaveLength(0);
    expect(tx.reportedChannel).toBe(3);
  });

  it("never publishes to command topics and never retains commands", async () => {
    const { h } = await discoveredHarness();
    h.mqtt.deliver(deviceTopics(RX_FIXTURE_MAC).channelSet, "4");
    h.mqtt.deliver(controllerTopics.rescanPress, "PRESS");
    await flush();
    for (const message of h.mqtt.published) {
      expect(message.topic.endsWith("/set")).toBe(false);
      expect(message.topic).not.toBe(controllerTopics.rescanPress);
    }
  });

  it("the rescan button triggers a full discovery", async () => {
    const { h } = await discoveredHarness();
    const runs = h.controller.discoveryRunCount();
    h.mqtt.deliver(controllerTopics.rescanPress, "PRESS");
    await flush();
    expect(h.controller.discoveryRunCount()).toBe(runs + 1);
  });

  it("republishes discovery and state on MQTT reconnect without touching the hardware", async () => {
    const { h } = await discoveredHarness();
    h.mqtt.clear();
    h.network.requests.length = 0;
    h.mqtt.simulateDisconnect();
    h.mqtt.simulateConnect();
    await flush();

    const configs = h.mqtt.discoveryConfigs();
    expect(configs.has(`homeassistant/number/${RX_ID}/channel/config`)).toBe(true);
    expect(configs.has(`homeassistant/sensor/${TX_ID}/channel/config`)).toBe(true);
    expect(configs.has("homeassistant/button/dt241m_controller/rescan/config")).toBe(true);
    expect(h.mqtt.lastOn(controllerTopics.availability)?.payload).toBe("online");
    expect(h.mqtt.lastOn(deviceTopics(RX_FIXTURE_MAC).availability)?.payload).toBe("online");
    expect(h.mqtt.lastOn(deviceTopics(RX_FIXTURE_MAC).channelState)?.payload).toBe("2");
    expect(h.mqtt.lastOn(deviceTopics(TX_FIXTURE_MAC).channelState)?.payload).toBe("3");
    expect(h.mqtt.lastOn(deviceTopics(RX_FIXTURE_MAC).nameState)?.payload).toBe("ER02_286CD6D8");
    expect(h.network.requests).toHaveLength(0);
  });

  it("republishes discovery and state when Home Assistant announces it is online", async () => {
    const { h } = await discoveredHarness();
    h.mqtt.clear();
    h.network.requests.length = 0;
    h.mqtt.deliver(HA_STATUS_TOPIC, "online");
    await flush();
    expect(h.mqtt.discoveryConfigs().size).toBeGreaterThanOrEqual(9);
    expect(h.mqtt.lastOn(deviceTopics(RX_FIXTURE_MAC).channelState)?.payload).toBe("2");
    expect(h.network.requests).toHaveLength(0);

    h.mqtt.clear();
    h.mqtt.deliver(HA_STATUS_TOPIC, "offline");
    await flush();
    expect(h.mqtt.published).toHaveLength(0);
  });

  it("publishes controller offline and disconnects on shutdown", async () => {
    const { h } = await discoveredHarness();
    await h.controller.stop();
    expect(h.mqtt.lastOn(controllerTopics.availability)).toMatchObject({ payload: "offline", retain: true });
    expect(h.mqtt.ended).toBe(true);
    await expect(h.controller.requestChannelChange(RX_FIXTURE_MAC, 4)).rejects.toMatchObject({ reason: "shutting_down" });
  });
});

describe("MQTT connection setup", () => {
  it("configures a retained Last Will on the controller availability topic and passes credentials without logging them", async () => {
    const connect = vi.fn(() => {
      const emitter = new EventEmitter() as EventEmitter & { connected: boolean };
      emitter.connected = false;
      return emitter;
    });
    vi.doMock("mqtt", () => ({ default: { connect }, connect }));
    const { createMqttConnection } = await import("../mqtt/client.js");
    const lines: string[] = [];
    const { createLogger } = await import("../logger.js");

    createMqttConnection({
      settings: { host: "core-mosquitto", port: 1883, username: "addons", password: "s3cret", tls: false },
      will: { topic: controllerTopics.availability, payload: "offline" },
      clientId: "dt241m-test",
      log: createLogger("debug", (line) => lines.push(line))
    });

    expect(connect).toHaveBeenCalledTimes(1);
    const [url, options] = connect.mock.calls[0] as unknown as [string, Record<string, unknown>];
    expect(url).toBe("mqtt://core-mosquitto:1883");
    expect(options["will"]).toMatchObject({ topic: controllerTopics.availability, retain: true, qos: 1 });
    expect(String((options["will"] as { payload: Buffer }).payload)).toBe("offline");
    expect(options["username"]).toBe("addons");
    expect(options["password"]).toBe("s3cret");
    expect(lines.join("\n")).not.toContain("s3cret");
    expect(lines.join("\n")).not.toContain("addons");
    vi.doUnmock("mqtt");
  });
});
