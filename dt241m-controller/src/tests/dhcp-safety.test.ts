import { describe, expect, it } from "vitest";
import { deviceTopics } from "../mqtt/topics.js";
import { createHarness } from "./helpers/harness.js";
import { SimulatedDevice } from "./helpers/simulated-network.js";

const RECEIVER_A_MAC = "aa:aa:aa:aa:aa:aa";
const RECEIVER_B_MAC = "bb:bb:bb:bb:bb:bb";

describe("DHCP identity safety", () => {
  it("never writes to an IP that now belongs to another MAC; relocates the target first", async () => {
    const h = createHarness({ captureLogs: true });
    const receiverA = h.network.place(
      "192.168.1.5",
      new SimulatedDevice({ mac: RECEIVER_A_MAC, fixture: "rx-info-initial-channel-2", overrides: { dev_name: "RX_A" } })
    );
    await h.controller.runDiscovery("initial");
    expect(h.controller.registry.get(RECEIVER_A_MAC)?.ip).toBe("192.168.1.5");

    h.network.move("192.168.1.5", "192.168.1.10");
    const receiverB = h.network.place(
      "192.168.1.5",
      new SimulatedDevice({ mac: RECEIVER_B_MAC, fixture: "rx-info-initial-channel-2", overrides: { dev_name: "RX_B" }, reportedChannel: 9 })
    );
    h.network.requests.length = 0;

    const outcome = await h.controller.requestChannelChange(RECEIVER_A_MAC, 7);

    expect(h.network.requestsTo("192.168.1.5", "get_device_info_proav").length).toBeGreaterThan(0);
    expect(h.network.writesTo("192.168.1.5")).toHaveLength(0);
    expect(receiverB.reportedChannel).toBe(9);
    expect(receiverB.writeCount).toBe(0);

    expect(h.network.writesTo("192.168.1.10")).toHaveLength(1);
    expect(receiverA.reportedChannel).toBe(7);
    expect(outcome.status).toBe("matched");
    expect(outcome.ip).toBe("192.168.1.10");

    const firstWriteIndex = h.network.requests.findIndex((r) => r.rpc?.method === "set_channel_id");
    const verifyIndex = h.network.requests.findIndex((r) => r.ip === "192.168.1.10" && r.rpc?.method === "get_device_info_proav");
    expect(verifyIndex).toBeGreaterThanOrEqual(0);
    expect(verifyIndex).toBeLessThan(firstWriteIndex);

    const a = h.controller.registry.get(RECEIVER_A_MAC)!;
    const b = h.controller.registry.get(RECEIVER_B_MAC)!;
    expect(a.ip).toBe("192.168.1.10");
    expect(a.id).toBe("dt241m_aaaaaaaaaaaa");
    expect(b.ip).toBe("192.168.1.5");
    expect(b.id).toBe("dt241m_bbbbbbbbbbbb");
    expect(h.controller.registry.all()).toHaveLength(2);

    expect(h.mqtt.lastOn(deviceTopics(RECEIVER_A_MAC).channelState)?.payload).toBe("7");
    expect(h.mqtt.lastOn(deviceTopics(RECEIVER_B_MAC).channelState)?.payload).toBe("9");
    expect(h.logs.some((line) => line.includes("identity_mismatch"))).toBe(true);
  });

  it("does not write when the target cannot be located after rediscovery", async () => {
    const h = createHarness();
    h.network.place("192.168.1.5", new SimulatedDevice({ mac: RECEIVER_A_MAC, fixture: "rx-info-initial-channel-2" }));
    await h.controller.runDiscovery("initial");

    h.network.remove("192.168.1.5");
    const receiverB = h.network.place("192.168.1.5", new SimulatedDevice({ mac: RECEIVER_B_MAC, fixture: "rx-info-initial-channel-2" }));

    const outcome = await h.controller.requestChannelChange(RECEIVER_A_MAC, 7);
    expect(outcome.status).toBe("failed");
    expect(outcome.status === "failed" && outcome.reason).toBe("device_not_located");
    expect(receiverB.writeCount).toBe(0);
    expect(h.network.writesTo("192.168.1.5")).toHaveLength(0);
    expect(h.controller.registry.get(RECEIVER_A_MAC)?.online).toBe(false);
  });

  it("does not write when the stored IP stops responding and the device is gone", async () => {
    const h = createHarness();
    h.network.place("192.168.1.5", new SimulatedDevice({ mac: RECEIVER_A_MAC, fixture: "rx-info-initial-channel-2" }));
    await h.controller.runDiscovery("initial");
    h.network.remove("192.168.1.5");

    const outcome = await h.controller.requestChannelChange(RECEIVER_A_MAC, 3);
    expect(outcome.status).toBe("failed");
    expect(h.network.requests.filter((r) => r.rpc?.method === "set_channel_id")).toHaveLength(0);
    expect(h.mqtt.lastOn(deviceTopics(RECEIVER_A_MAC).availability)?.payload).toBe("offline");
  });

  it("follows the device to its new IP when the old IP is silent", async () => {
    const h = createHarness();
    const receiverA = h.network.place("192.168.1.5", new SimulatedDevice({ mac: RECEIVER_A_MAC, fixture: "rx-info-initial-channel-2" }));
    await h.controller.runDiscovery("initial");
    h.network.move("192.168.1.5", "192.168.1.13");

    const outcome = await h.controller.requestChannelChange(RECEIVER_A_MAC, 3);
    expect(outcome.status).toBe("matched");
    expect(receiverA.reportedChannel).toBe(3);
    expect(h.network.writesTo("192.168.1.13")).toHaveLength(1);
    expect(h.mqtt.lastOn(deviceTopics(RECEIVER_A_MAC).availability)?.payload).toBe("online");
  });

  it("refuses to write to transmitters and unknown devices", async () => {
    const h = createHarness();
    h.network.place("192.168.1.5", new SimulatedDevice({ mac: "fc:19:28:6c:d2:91", fixture: "tx-info-initial-channel-3" }));
    h.network.place(
      "192.168.1.6",
      new SimulatedDevice({ mac: "cc:cc:cc:cc:cc:cc", fixture: "rx-info-initial-channel-2", overrides: { product_name: "Mystery", model: "unknown" } })
    );
    await h.controller.runDiscovery("initial");
    expect(h.controller.registry.get("cc:cc:cc:cc:cc:cc")?.role).toBe("unknown");

    await expect(h.controller.requestChannelChange("fc:19:28:6c:d2:91", 2)).rejects.toMatchObject({ reason: "not_a_receiver" });
    await expect(h.controller.requestChannelChange("cc:cc:cc:cc:cc:cc", 2)).rejects.toMatchObject({ reason: "not_a_receiver" });
    await expect(h.controller.requestChannelChange("dd:dd:dd:dd:dd:dd", 2)).rejects.toMatchObject({ reason: "unknown_device" });
    expect(h.network.requests.filter((r) => r.rpc?.method === "set_channel_id")).toHaveLength(0);
  });
});
