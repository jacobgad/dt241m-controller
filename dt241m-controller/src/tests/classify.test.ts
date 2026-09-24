import { describe, expect, it } from "vitest";
import { classifyRole } from "../dt241m/classify.js";
import { deviceInfoSchema } from "../dt241m/schemas.js";
import { adapterIdFromMac, compactMac, normalizeMac } from "../mac.js";
import { fixtureResult } from "./helpers/fixtures.js";

describe("classifyRole", () => {
  it("classifies the real TX fixture as transmitter", () => {
    expect(classifyRole(deviceInfoSchema.parse(fixtureResult("tx-info-initial-channel-3")))).toBe("transmitter");
  });

  it("classifies the real RX fixture as receiver", () => {
    expect(classifyRole(deviceInfoSchema.parse(fixtureResult("rx-info-initial-channel-2")))).toBe("receiver");
  });

  it("returns unknown when product and model are missing", () => {
    expect(classifyRole({})).toBe("unknown");
    expect(classifyRole({ product_name: null, model: null })).toBe("unknown");
  });

  it("returns unknown for conflicting TX/RX information", () => {
    expect(classifyRole({ product_name: "ProAVTx ET01", model: "am_8270_proavrx-eth_er01-pway-dt241" })).toBe("unknown");
  });

  it("returns unknown for unrelated products", () => {
    expect(classifyRole({ product_name: "SomethingElse", model: "generic" })).toBe("unknown");
  });

  it("does not classify from dev_name alone", () => {
    expect(classifyRole({ product_name: null, model: null, ...{ dev_name: "ER02_286CD6D8" } })).toBe("unknown");
  });

  it("classifies from model when product_name is missing", () => {
    expect(classifyRole({ model: "am_8270_proavrx-eth_er01-pway-dt241" })).toBe("receiver");
  });
});

describe("MAC normalization", () => {
  it("normalizes the fixture MAC formats", () => {
    expect(normalizeMac("FC:19:28:6C:D6:D8")).toBe("fc:19:28:6c:d6:d8");
    expect(normalizeMac("fc-19-28-6c-d6-d8")).toBe("fc:19:28:6c:d6:d8");
    expect(normalizeMac("FC19286CD6D8")).toBe("fc:19:28:6c:d6:d8");
  });

  it("rejects invalid MACs", () => {
    expect(normalizeMac("")).toBeNull();
    expect(normalizeMac("not-a-mac")).toBeNull();
    expect(normalizeMac("fc:19:28:6c:d6")).toBeNull();
  });

  it("derives compact and stable ids", () => {
    expect(compactMac("fc:19:28:6c:d6:d8")).toBe("fc19286cd6d8");
    expect(adapterIdFromMac("fc:19:28:6c:d6:d8")).toBe("dt241m_fc19286cd6d8");
  });
});
