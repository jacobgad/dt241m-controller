import { parseAddonOptions, type AddonOptions } from "../../config.js";
import { Controller } from "../../controller.js";
import type { AdapterStore } from "../../db/adapter-store.js";
import { createDt241mClient } from "../../dt241m/client.js";
import { createLogger, silentLogger, type Logger } from "../../logger.js";
import { FakeMqtt } from "./fake-mqtt.js";
import { MemoryAdapterStore } from "./memory-store.js";
import { SimulatedNetwork } from "./simulated-network.js";

export const TEST_RANGE = "192.168.1.0/28";

export type HarnessOptions = {
  network?: SimulatedNetwork;
  mqtt?: FakeMqtt;
  store?: AdapterStore;
  options?: Partial<Record<keyof AddonOptions, unknown>>;
  captureLogs?: boolean;
};

export type Harness = {
  network: SimulatedNetwork;
  mqtt: FakeMqtt;
  store: AdapterStore;
  controller: Controller;
  logs: string[];
  log: Logger;
};

export function createHarness(overrides: HarnessOptions = {}): Harness {
  const network = overrides.network ?? new SimulatedNetwork();
  const mqtt = overrides.mqtt ?? new FakeMqtt();
  const store = overrides.store ?? new MemoryAdapterStore();
  const logs: string[] = [];
  const log = overrides.captureLogs ? createLogger("debug", (line) => logs.push(line)) : silentLogger;

  const options = parseAddonOptions({
    scan_ranges: [TEST_RANGE],
    poll_interval_seconds: 5,
    probe_timeout_ms: 200,
    discovery_concurrency: 4,
    log_level: "debug",
    ...overrides.options
  });

  const controller = new Controller({
    client: createDt241mClient({ fetchImpl: network.fetch, timeoutMs: options.probe_timeout_ms }),
    mqtt,
    store,
    options,
    log,
    discoveryContext: { version: "test", supportUrl: "https://example.invalid" },
    sleep: async () => {},
    readback: { attempts: 2, delayMs: 0 }
  });

  return { network, mqtt, store, controller, logs, log };
}
