import { describe, expect, it } from "vitest";
import { deviceInfoSchema, type DeviceInfo } from "../dt241m/schemas.js";
import { Registry } from "../registry.js";
import { RX_FIXTURE_MAC, TX_FIXTURE_MAC, fixtureResult } from "./helpers/fixtures.js";

function info(fixture: "tx-info-initial-channel-3" | "rx-info-initial-channel-2", overrides: Record<string, unknown> = {}): DeviceInfo {
  return deviceInfoSchema.parse({ ...fixtureResult(fixture), ...overrides });
}

describe("Registry", () => {
  it("creates an adapter keyed by normalized MAC with a stable id", () => {
    const registry = new Registry();
    const result = registry.recordObservation("192.168.1.20", info("rx-info-initial-channel-2"), 1000)!;
    expect(result.created).toBe(true);
    expect(result.adapter.mac).toBe(RX_FIXTURE_MAC);
    expect(result.adapter.id).toBe("dt241m_fc19286cd6d8");
    expect(result.adapter.role).toBe("receiver");
    expect(result.adapter.channel).toBe(2);
    expect(result.adapter.online).toBe(true);
    expect(result.adapter.lastSeenAt).toBe(1000);
    expect(result.adapter.reportedName).toBe("ER02_286CD6D8");
    expect(result.adapter.firmware).toBe("1.13471.133");
  });

  it("updates the same adapter when the MAC is seen at a new IP", () => {
    const registry = new Registry();
    registry.recordObservation("192.168.1.20", info("rx-info-initial-channel-2"), 1000);
    const result = registry.recordObservation("192.168.1.50", info("rx-info-initial-channel-2"), 2000)!;
    expect(result.created).toBe(false);
    expect(result.ipChanged).toBe(true);
    expect(result.previousIp).toBe("192.168.1.20");
    expect(registry.all()).toHaveLength(1);
    expect(registry.get(RX_FIXTURE_MAC)?.ip).toBe("192.168.1.50");
  });

  it("does not mutate the old adapter identity when its IP returns a different MAC", () => {
    const registry = new Registry();
    registry.recordObservation("192.168.1.20", info("rx-info-initial-channel-2"), 1000);
    const result = registry.recordObservation("192.168.1.20", info("tx-info-initial-channel-3"), 2000)!;

    expect(result.created).toBe(true);
    expect(result.adapter.mac).toBe(TX_FIXTURE_MAC);
    expect(result.displaced?.mac).toBe(RX_FIXTURE_MAC);

    const original = registry.get(RX_FIXTURE_MAC)!;
    expect(original.mac).toBe(RX_FIXTURE_MAC);
    expect(original.id).toBe("dt241m_fc19286cd6d8");
    expect(original.role).toBe("receiver");
    expect(original.online).toBe(false);
    expect(registry.all()).toHaveLength(2);
  });

  it("reports channel changes and online transitions", () => {
    const registry = new Registry();
    registry.recordObservation("192.168.1.20", info("rx-info-initial-channel-2"), 1000);
    registry.markOffline(RX_FIXTURE_MAC);
    expect(registry.counts()).toEqual({ known: 1, online: 0 });

    const result = registry.recordObservation("192.168.1.20", info("rx-info-initial-channel-2", { channel_id: 7 }), 3000)!;
    expect(result.cameOnline).toBe(true);
    expect(result.channelChanged).toBe(true);
    expect(result.adapter.channel).toBe(7);
    expect(registry.counts()).toEqual({ known: 1, online: 1 });
  });

  it("markOffline is idempotent", () => {
    const registry = new Registry();
    registry.recordObservation("192.168.1.20", info("rx-info-initial-channel-2"), 1000);
    expect(registry.markOffline(RX_FIXTURE_MAC)).not.toBeNull();
    expect(registry.markOffline(RX_FIXTURE_MAC)).toBeNull();
    expect(registry.markOffline("00:00:00:00:00:00")).toBeNull();
  });

  it("rejects observations with an invalid MAC", () => {
    const registry = new Registry();
    expect(registry.recordObservation("192.168.1.20", info("rx-info-initial-channel-2", { lan_mac_addr: "garbage" }), 1)).toBeNull();
    expect(registry.all()).toHaveLength(0);
  });

  it("flags role changes", () => {
    const registry = new Registry();
    registry.recordObservation("192.168.1.20", info("rx-info-initial-channel-2", { product_name: null, model: null }), 1);
    expect(registry.get(RX_FIXTURE_MAC)?.role).toBe("unknown");
    const result = registry.recordObservation("192.168.1.20", info("rx-info-initial-channel-2"), 2)!;
    expect(result.roleChanged).toBe(true);
    expect(result.adapter.role).toBe("receiver");
  });
});
