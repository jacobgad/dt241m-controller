import { describe, expect, it } from "vitest";
import { expandCidrHosts, expandScanRanges, ipInRanges, isPrivateCidr, parseCidr } from "../cidr.js";
import { parseAddonOptions, parseMqttSettings } from "../config.js";
import { parseChannelPayload } from "../mqtt/handlers.js";

describe("CIDR parsing", () => {
  it("parses and normalizes a /24", () => {
    const cidr = parseCidr("192.168.40.17/24")!;
    expect(cidr.text).toBe("192.168.40.0/24");
    const hosts = expandCidrHosts(cidr);
    expect(hosts).toHaveLength(254);
    expect(hosts[0]).toBe("192.168.40.1");
    expect(hosts[253]).toBe("192.168.40.254");
  });

  it("expands /31 and /32 without dropping addresses", () => {
    expect(expandCidrHosts(parseCidr("10.0.0.4/31")!)).toEqual(["10.0.0.4", "10.0.0.5"]);
    expect(expandCidrHosts(parseCidr("10.0.0.9/32")!)).toEqual(["10.0.0.9"]);
  });

  it("rejects malformed input", () => {
    expect(parseCidr("192.168.1.0")).toBeNull();
    expect(parseCidr("192.168.1.0/33")).toBeNull();
    expect(parseCidr("999.1.1.1/24")).toBeNull();
    expect(parseCidr("nope")).toBeNull();
  });

  it("de-duplicates overlapping ranges", () => {
    const hosts = expandScanRanges([parseCidr("10.0.0.0/30")!, parseCidr("10.0.0.0/29")!]);
    expect(hosts).toEqual(["10.0.0.1", "10.0.0.2", "10.0.0.3", "10.0.0.4", "10.0.0.5", "10.0.0.6"]);
  });

  it("identifies private ranges", () => {
    expect(isPrivateCidr(parseCidr("192.168.40.0/24")!)).toBe(true);
    expect(isPrivateCidr(parseCidr("10.20.0.0/16")!)).toBe(true);
    expect(isPrivateCidr(parseCidr("172.31.0.0/24")!)).toBe(true);
    expect(isPrivateCidr(parseCidr("172.32.0.0/24")!)).toBe(false);
    expect(isPrivateCidr(parseCidr("8.8.8.0/24")!)).toBe(false);
  });

  it("checks range membership", () => {
    const ranges = [parseCidr("192.168.1.0/24")!];
    expect(ipInRanges("192.168.1.50", ranges)).toBe(true);
    expect(ipInRanges("192.168.2.50", ranges)).toBe(false);
  });
});

describe("add-on options", () => {
  it("applies defaults", () => {
    const options = parseAddonOptions({ scan_ranges: ["192.168.40.0/24"] });
    expect(options.poll_interval_seconds).toBe(15);
    expect(options.probe_timeout_ms).toBe(2000);
    expect(options.discovery_concurrency).toBe(8);
    expect(options.log_level).toBe("info");
    expect(options.scan_ranges[0]?.text).toBe("192.168.40.0/24");
  });

  it("supports multiple ranges", () => {
    const options = parseAddonOptions({ scan_ranges: ["192.168.40.0/24", "10.10.5.0/25"] });
    expect(options.scan_ranges.map((c) => c.text)).toEqual(["192.168.40.0/24", "10.10.5.0/25"]);
  });

  it("requires at least one range", () => {
    expect(() => parseAddonOptions({ scan_ranges: [] })).toThrow();
  });

  it("rejects public ranges and oversized ranges", () => {
    expect(() => parseAddonOptions({ scan_ranges: ["8.8.8.0/24"] })).toThrow(/private/);
    expect(() => parseAddonOptions({ scan_ranges: ["10.0.0.0/8"] })).toThrow(/too large/);
    expect(() => parseAddonOptions({ scan_ranges: ["not-a-cidr"] })).toThrow(/valid IPv4 CIDR/);
  });

  it("bounds numeric options", () => {
    expect(() => parseAddonOptions({ scan_ranges: ["192.168.1.0/24"], poll_interval_seconds: 1 })).toThrow();
    expect(() => parseAddonOptions({ scan_ranges: ["192.168.1.0/24"], discovery_concurrency: 0 })).toThrow();
  });
});

describe("MQTT settings from Supervisor-provided environment", () => {
  it("reads host, port, credentials and tls", () => {
    const settings = parseMqttSettings({
      MQTT_HOST: "core-mosquitto",
      MQTT_PORT: "1883",
      MQTT_USERNAME: "addons",
      MQTT_PASSWORD: "secret",
      MQTT_SSL: "false"
    });
    expect(settings).toEqual({ host: "core-mosquitto", port: 1883, username: "addons", password: "secret", tls: false });
  });

  it("requires a host", () => {
    expect(() => parseMqttSettings({})).toThrow();
  });

  it("treats empty credentials as absent", () => {
    const settings = parseMqttSettings({ MQTT_HOST: "broker", MQTT_USERNAME: "", MQTT_PASSWORD: "" });
    expect(settings.username).toBeUndefined();
    expect(settings.password).toBeUndefined();
    expect(settings.port).toBe(1883);
  });
});

describe("channel command payload parsing", () => {
  it("accepts integers and integer-valued decimals", () => {
    expect(parseChannelPayload("2")).toBe(2);
    expect(parseChannelPayload(" 0 ")).toBe(0);
    expect(parseChannelPayload("255")).toBe(255);
    expect(parseChannelPayload("2.0")).toBe(2);
  });

  it("rejects invalid values", () => {
    expect(parseChannelPayload("2.5")).toBeNull();
    expect(parseChannelPayload("-1")).toBeNull();
    expect(parseChannelPayload("256")).toBeNull();
    expect(parseChannelPayload("two")).toBeNull();
    expect(parseChannelPayload("")).toBeNull();
  });
});
