import { describe, expect, it } from "vitest";
import { deviceTopics } from "../mqtt/topics.js";
import { RX_FIXTURE_MAC } from "./helpers/fixtures.js";
import { createHarness } from "./helpers/harness.js";
import { SimulatedDevice } from "./helpers/simulated-network.js";

const RX_IP = "192.168.1.5";

describe("hardware quirks (synthetic, modelled on user-observed behaviour)", () => {
  it("treats a stale front panel as irrelevant: reported channel 2 with panel showing 3 is success", async () => {
    const h = createHarness({ captureLogs: true });
    const device = h.network.place(
      RX_IP,
      new SimulatedDevice({ mac: RX_FIXTURE_MAC, fixture: "rx-info-initial-channel-2", reportedChannel: 3, frontPanelChannel: 3 })
    );
    await h.controller.runDiscovery("start");

    const outcome = await h.controller.requestChannelChange(RX_FIXTURE_MAC, 2);

    expect(device.reportedChannel).toBe(2);
    expect(device.videoChannel).toBe(2);
    expect(device.frontPanelChannel).toBe(3);
    expect(outcome.status).toBe("matched");
    expect(h.mqtt.lastOn(deviceTopics(RX_FIXTURE_MAC).channelState)?.payload).toBe("2");
    expect(h.logs.some((line) => line.includes("channel_readback_mismatch"))).toBe(false);
    expect(h.logs.some((line) => line.includes("front_panel") || line.includes("frontPanel"))).toBe(false);
  });

  it("reports a readback mismatch when the device acknowledges but does not apply, without retrying the write", async () => {
    const h = createHarness({ captureLogs: true });
    const device = h.network.place(
      RX_IP,
      new SimulatedDevice({ mac: RX_FIXTURE_MAC, fixture: "rx-info-initial-channel-2", ackWithoutApply: true })
    );
    await h.controller.runDiscovery("start");

    const outcome = await h.controller.requestChannelChange(RX_FIXTURE_MAC, 5);

    expect(outcome.status).toBe("mismatch");
    expect(outcome.status === "mismatch" && outcome.reported).toBe(2);
    expect(device.writeCount).toBe(1);
    expect(h.mqtt.lastOn(deviceTopics(RX_FIXTURE_MAC).channelState)?.payload).toBe("2");
    expect(h.logs.some((line) => line.includes("channel_readback_mismatch"))).toBe(true);
  });

  it("never publishes the requested value optimistically", async () => {
    const h = createHarness();
    h.network.place(RX_IP, new SimulatedDevice({ mac: RX_FIXTURE_MAC, fixture: "rx-info-initial-channel-2", ackWithoutApply: true }));
    await h.controller.runDiscovery("start");
    h.mqtt.clear();
    await h.controller.requestChannelChange(RX_FIXTURE_MAC, 9);
    const states = h.mqtt.messagesOn(deviceTopics(RX_FIXTURE_MAC).channelState).map((m) => m.payload);
    expect(states).not.toContain("9");
  });

  it("treats a write timeout as ambiguous, reads back, and does not resend", async () => {
    const h = createHarness({ captureLogs: true });
    const device = h.network.place(
      RX_IP,
      new SimulatedDevice({ mac: RX_FIXTURE_MAC, fixture: "rx-info-initial-channel-2", writeHangsAfterApply: true })
    );
    await h.controller.runDiscovery("start");

    const outcome = await h.controller.requestChannelChange(RX_FIXTURE_MAC, 4);

    expect(device.writeCount).toBe(1);
    expect(h.network.writesTo(RX_IP)).toHaveLength(1);
    expect(outcome.status).toBe("matched");
    expect(h.mqtt.lastOn(deviceTopics(RX_FIXTURE_MAC).channelState)?.payload).toBe("4");
    expect(h.logs.some((line) => line.includes("channel_change_ambiguous"))).toBe(true);
  });

  it("reports a rejected write and publishes the actual state", async () => {
    const h = createHarness();
    h.network.place(RX_IP, new SimulatedDevice({ mac: RX_FIXTURE_MAC, fixture: "rx-info-initial-channel-2", rejectWrites: true }));
    await h.controller.runDiscovery("start");
    const outcome = await h.controller.requestChannelChange(RX_FIXTURE_MAC, 4);
    expect(outcome.status).toBe("failed");
    expect(outcome.status === "failed" && outcome.reason).toBe("write_rejected");
    expect(outcome.reported).toBe(2);
    expect(h.mqtt.lastOn(deviceTopics(RX_FIXTURE_MAC).channelState)?.payload).toBe("2");
  });

  it("accepts channel 0 as a real value", async () => {
    const h = createHarness();
    const device = h.network.place(RX_IP, new SimulatedDevice({ mac: RX_FIXTURE_MAC, fixture: "rx-info-initial-channel-2" }));
    await h.controller.runDiscovery("start");
    const outcome = await h.controller.requestChannelChange(RX_FIXTURE_MAC, 0);
    expect(outcome.status).toBe("matched");
    expect(device.reportedChannel).toBe(0);
    expect(h.mqtt.lastOn(deviceTopics(RX_FIXTURE_MAC).channelState)?.payload).toBe("0");
  });
});
