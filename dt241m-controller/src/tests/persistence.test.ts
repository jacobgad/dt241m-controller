import { mkdtempSync, rmSync } from "node:fs";
import { tmpdir } from "node:os";
import path from "node:path";
import { afterEach, beforeEach, describe, expect, it } from "vitest";
import { SqliteAdapterStore } from "../db/adapter-store.js";
import { openDatabase, type OpenedDatabase } from "../db/database.js";
import { adapters } from "../db/schema.js";
import { deviceTopics } from "../mqtt/topics.js";
import { RX_FIXTURE_MAC } from "./helpers/fixtures.js";
import { createHarness, type Harness } from "./helpers/harness.js";
import { SimulatedDevice, SimulatedNetwork } from "./helpers/simulated-network.js";

const RX_IP = "192.168.1.5";
const RX_ID = "dt241m_fc19286cd6d8";
const RX_TOPICS = deviceTopics(RX_FIXTURE_MAC);

let tempDir: string;
let opened: OpenedDatabase[] = [];

beforeEach(() => {
  tempDir = mkdtempSync(path.join(tmpdir(), "dt241m-test-"));
  opened = [];
});

afterEach(() => {
  for (const database of opened) database.close();
  rmSync(tempDir, { recursive: true, force: true });
});

function openStore(file = "adapters.sqlite") {
  const database = openDatabase(path.join(tempDir, file));
  opened.push(database);
  return { database, store: new SqliteAdapterStore(database.db) };
}

async function bootWithDevice(store: SqliteAdapterStore, network = new SimulatedNetwork()): Promise<Harness> {
  const h = createHarness({ store, network });
  await h.controller.start();
  return h;
}

describe("SQLite adapter store", () => {
  it("runs migrations and starts empty", () => {
    const { store, database } = openStore();
    expect(store.loadAll()).toEqual([]);
    expect(database.db.select().from(adapters).all()).toEqual([]);
  });

  it("reopening an existing database file is idempotent", () => {
    const first = openStore("reopen.sqlite");
    first.database.close();
    const second = openStore("reopen.sqlite");
    expect(second.store.loadAll()).toEqual([]);
  });

  it("round-trips an adapter through save and loadAll", async () => {
    const { store } = openStore();
    const network = new SimulatedNetwork();
    network.place(RX_IP, new SimulatedDevice({ mac: RX_FIXTURE_MAC, fixture: "rx-info-initial-channel-2" }));
    const h = createHarness({ store, network });
    await h.controller.runDiscovery("seed");

    const [record] = store.loadAll();
    expect(record).toMatchObject({
      mac: RX_FIXTURE_MAC,
      id: RX_ID,
      role: "receiver",
      ip: RX_IP,
      name: null,
      reportedName: "ER02_286CD6D8",
      productName: "ProAVRx ER01",
      model: "am_8270_proavrx-eth_er01-pway-dt241",
      firmware: "1.13471.133",
      channel: 2,
      online: false
    });
    expect(typeof record!.firstSeenAt).toBe("number");
    expect(record!.lastSeenAt).toBe(record!.firstSeenAt);
  });
});

describe("restart behaviour", () => {
  it("keeps an offline adapter visible as unavailable after restart", async () => {
    const network = new SimulatedNetwork();
    network.place(RX_IP, new SimulatedDevice({ mac: RX_FIXTURE_MAC, fixture: "rx-info-initial-channel-2" }));
    const first = openStore();
    const before = await bootWithDevice(first.store, network);
    await before.controller.stop();
    first.database.close();

    network.remove(RX_IP);
    const second = openStore();
    const after = createHarness({ store: second.store, network });
    await after.controller.start();

    const adapter = after.controller.registry.get(RX_FIXTURE_MAC);
    expect(adapter).toBeDefined();
    expect(adapter!.online).toBe(false);
    expect(adapter!.ip).toBe(RX_IP);
    expect(adapter!.channel).toBe(2);

    const configs = after.mqtt.discoveryConfigs();
    expect(configs.has(`homeassistant/number/${RX_ID}/channel/config`)).toBe(true);
    expect(configs.has(`homeassistant/text/${RX_ID}/name/config`)).toBe(true);
    expect(after.mqtt.lastOn(RX_TOPICS.availability)?.payload).toBe("offline");
    expect(after.mqtt.lastOn("dt241m/controller/known_devices/state")?.payload).toBe("1");
    expect(after.mqtt.lastOn("dt241m/controller/online_devices/state")?.payload).toBe("0");

    expect(second.store.loadAll()).toHaveLength(1);
    expect(network.writesTo(RX_IP)).toHaveLength(0);
    await after.controller.stop();
  });

  it("brings a persisted adapter online by probing its last-known IP before the full scan", async () => {
    const network = new SimulatedNetwork();
    network.place(RX_IP, new SimulatedDevice({ mac: RX_FIXTURE_MAC, fixture: "rx-info-initial-channel-2" }));
    const first = openStore();
    const before = await bootWithDevice(first.store, network);
    await before.controller.stop();

    const after = createHarness({ store: first.store, network });
    network.requests.length = 0;
    await after.controller.start();

    expect(network.requests[0]?.ip).toBe(RX_IP);
    expect(after.controller.registry.get(RX_FIXTURE_MAC)?.online).toBe(true);
    const availability = after.mqtt.messagesOn(RX_TOPICS.availability).map((m) => m.payload);
    expect(availability[0]).toBe("offline");
    expect(availability[availability.length - 1]).toBe("online");
    expect(network.writesTo(RX_IP)).toHaveLength(0);
    await after.controller.stop();
  });

  it("does not reapply previous routes after restart", async () => {
    const network = new SimulatedNetwork();
    const device = network.place(RX_IP, new SimulatedDevice({ mac: RX_FIXTURE_MAC, fixture: "rx-info-initial-channel-2" }));
    const { store } = openStore();
    const before = await bootWithDevice(store, network);
    await before.controller.requestChannelChange(RX_FIXTURE_MAC, 7);
    await before.controller.stop();

    device.reportedChannel = 1;
    const after = createHarness({ store, network });
    await after.controller.start();

    expect(device.reportedChannel).toBe(1);
    expect(network.writesTo(RX_IP)).toHaveLength(1);
    expect(after.mqtt.lastOn(RX_TOPICS.channelState)?.payload).toBe("1");
    await after.controller.stop();
  });

  it("name survives restart", async () => {
    const network = new SimulatedNetwork();
    network.place(RX_IP, new SimulatedDevice({ mac: RX_FIXTURE_MAC, fixture: "rx-info-initial-channel-2" }));
    const { store } = openStore();
    const before = await bootWithDevice(store, network);
    await before.controller.rename(RX_FIXTURE_MAC, "Main Projector");
    await before.controller.stop();

    const after = createHarness({ store, network });
    await after.controller.start();
    const adapter = after.controller.registry.get(RX_FIXTURE_MAC)!;
    expect(adapter.name).toBe("Main Projector");
    expect(adapter.reportedName).toBe("ER02_286CD6D8");
    expect(after.mqtt.lastOn(RX_TOPICS.nameState)?.payload).toBe("Main Projector");
    const config = after.mqtt.discoveryConfigs().get(`homeassistant/number/${RX_ID}/channel/config`)!;
    expect((config["device"] as { name: string }).name).toBe("Main Projector");
    await after.controller.stop();
  });

  it("persists a DHCP move as one record with the same name and identity", async () => {
    const network = new SimulatedNetwork();
    network.place("192.168.1.2", new SimulatedDevice({ mac: RX_FIXTURE_MAC, fixture: "rx-info-initial-channel-2" }));
    const { store, database } = openStore();
    const h = await bootWithDevice(store, network);
    await h.controller.rename(RX_FIXTURE_MAC, "Main Projector");
    const configsBefore = h.mqtt.discoveryConfigs();

    network.move("192.168.1.2", "192.168.1.8");
    await h.controller.runDiscovery("dhcp");

    const rows = database.db.select().from(adapters).all();
    expect(rows).toHaveLength(1);
    expect(rows[0]).toMatchObject({ mac: RX_FIXTURE_MAC, lastKnownIp: "192.168.1.8", name: "Main Projector" });
    expect(h.mqtt.lastOn(RX_TOPICS.ipState)?.payload).toBe("192.168.1.8");

    const configsAfter = h.mqtt.discoveryConfigs();
    expect([...configsAfter.keys()].sort()).toEqual([...configsBefore.keys()].sort());
    const deviceConfigs = [...configsAfter.entries()].filter(([topic]) => topic.includes(RX_ID));
    expect(deviceConfigs.length).toBe(3);
    for (const [, payload] of deviceConfigs) {
      expect((payload["device"] as { identifiers: string[] }).identifiers).toEqual(["dt241m:fc19286cd6d8"]);
      expect((payload["device"] as { name: string }).name).toBe("Main Projector");
    }
    expect(configsAfter.get(`homeassistant/number/${RX_ID}/channel/config`)?.["unique_id"]).toBe(`${RX_ID}_channel`);
    await h.controller.stop();
  });
});

describe("names", () => {
  async function namedHarness() {
    const network = new SimulatedNetwork();
    const device = network.place(RX_IP, new SimulatedDevice({ mac: RX_FIXTURE_MAC, fixture: "rx-info-initial-channel-2" }));
    const h = createHarness({ network });
    await h.controller.runDiscovery("seed");
    return { h, device };
  }

  it("discovery preserves the stored name even when hardware metadata changes", async () => {
    const { h, device } = await namedHarness();
    await h.controller.rename(RX_FIXTURE_MAC, "Main Projector");

    device.template["dev_name"] = "ER02_RENAMED_ON_DEVICE";
    device.template["version"] = "1.13471.999";
    await h.controller.runDiscovery("again");

    const adapter = h.controller.registry.get(RX_FIXTURE_MAC)!;
    expect(adapter.name).toBe("Main Projector");
    expect(adapter.reportedName).toBe("ER02_RENAMED_ON_DEVICE");
    expect(adapter.firmware).toBe("1.13471.999");
    const [stored] = h.store.loadAll();
    expect(stored?.name).toBe("Main Projector");
    expect(stored?.reportedName).toBe("ER02_RENAMED_ON_DEVICE");
    expect(h.mqtt.lastOn(RX_TOPICS.nameState)?.payload).toBe("Main Projector");
  });

  it("rename does not change MAC identity, topics, HA device identifier, or unique IDs", async () => {
    const { h } = await namedHarness();
    await h.controller.rename(RX_FIXTURE_MAC, "Main Projector");
    const before = h.mqtt.discoveryConfigs();
    h.mqtt.clear();

    await h.controller.rename(RX_FIXTURE_MAC, "Auditorium Projector");

    const after = h.mqtt.discoveryConfigs();
    expect([...after.keys()].sort()).toEqual([...before.keys()].sort());
    for (const [topic, payload] of after) {
      const previous = before.get(topic)!;
      expect(payload["unique_id"]).toBe(previous["unique_id"]);
      expect(payload["state_topic"]).toBe(previous["state_topic"]);
      expect(payload["command_topic"]).toBe(previous["command_topic"]);
      expect((payload["device"] as { identifiers: string[] }).identifiers).toEqual(["dt241m:fc19286cd6d8"]);
      expect((payload["device"] as { name: string }).name).toBe("Auditorium Projector");
    }
    expect(h.controller.registry.get(RX_FIXTURE_MAC)?.mac).toBe(RX_FIXTURE_MAC);
    expect(h.controller.registry.get(RX_FIXTURE_MAC)?.id).toBe(RX_ID);
    const [stored] = h.store.loadAll();
    expect(stored?.mac).toBe(RX_FIXTURE_MAC);
    expect(stored?.name).toBe("Auditorium Projector");
    expect(h.mqtt.lastOn(RX_TOPICS.nameState)?.payload).toBe("Auditorium Projector");
    for (const topic of h.mqtt.published.map((m) => m.topic)) {
      expect(topic).not.toMatch(/Projector/);
    }
  });

  it("clears the name with an empty string and falls back to reportedName", async () => {
    const { h } = await namedHarness();
    await h.controller.rename(RX_FIXTURE_MAC, "Main Projector");
    const outcome = await h.controller.rename(RX_FIXTURE_MAC, "   ");
    expect(outcome).toEqual({ status: "renamed", mac: RX_FIXTURE_MAC, name: null });
    expect(h.controller.registry.get(RX_FIXTURE_MAC)?.name).toBeNull();
    expect(h.mqtt.lastOn(RX_TOPICS.nameState)?.payload).toBe("ER02_286CD6D8");
    const [stored] = h.store.loadAll();
    expect(stored?.name).toBeNull();
  });

  it("trims and validates names", async () => {
    const { h } = await namedHarness();
    await h.controller.rename(RX_FIXTURE_MAC, "  Lobby TV  ");
    expect(h.controller.registry.get(RX_FIXTURE_MAC)?.name).toBe("Lobby TV");

    const tooLong = await h.controller.rename(RX_FIXTURE_MAC, "x".repeat(65));
    expect(tooLong.status).toBe("rejected");
    const controlChars = await h.controller.rename(RX_FIXTURE_MAC, "bad\u0007name");
    expect(controlChars.status).toBe("rejected");
    expect(h.controller.registry.get(RX_FIXTURE_MAC)?.name).toBe("Lobby TV");
    expect(h.mqtt.lastOn(RX_TOPICS.nameState)?.payload).toBe("Lobby TV");

    const unknown = await h.controller.rename("00:11:22:33:44:55", "Ghost");
    expect(unknown.status).toBe("rejected");
  });

  it("renaming never writes to the DT241M hardware", async () => {
    const { h } = await namedHarness();
    h.network.requests.length = 0;
    await h.controller.rename(RX_FIXTURE_MAC, "Main Projector");
    expect(h.network.requests).toHaveLength(0);
  });

  it("accepts renames delivered over MQTT", async () => {
    const { h } = await namedHarness();
    h.mqtt.deliver(RX_TOPICS.nameSet, "Stage Monitor");
    await new Promise((resolve) => setTimeout(resolve, 5));
    expect(h.controller.registry.get(RX_FIXTURE_MAC)?.name).toBe("Stage Monitor");
    expect(h.mqtt.lastOn(RX_TOPICS.nameState)?.payload).toBe("Stage Monitor");
  });

  it("the Name entity stays usable while the device is offline", async () => {
    const { h } = await namedHarness();
    const config = h.mqtt.discoveryConfigs().get(`homeassistant/text/${RX_ID}/name/config`)!;
    const availability = config["availability"] as Array<{ topic: string }>;
    expect(availability.map((a) => a.topic)).toEqual(["dt241m/controller/availability"]);
    expect(config["command_topic"]).toBe(RX_TOPICS.nameSet);
    expect(config["max"]).toBe(64);
  });
});
