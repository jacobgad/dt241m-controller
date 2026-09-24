import { mapWithConcurrency } from "./concurrency.js";
import type { Dt241mClient } from "./dt241m/client.js";
import type { DeviceInfo } from "./dt241m/schemas.js";

export type ProbeHit = { ip: string; info: DeviceInfo };

export type ProbeOptions = {
  concurrency: number;
  timeoutMs: number;
  onHit?: (hit: ProbeHit) => void | Promise<void>;
  onProbeStart?: (ip: string, activeCount: number) => void;
  shouldStop?: () => boolean;
};

export async function probeAddresses(client: Dt241mClient, ips: readonly string[], options: ProbeOptions): Promise<ProbeHit[]> {
  const hits: ProbeHit[] = [];
  let active = 0;

  await mapWithConcurrency(ips, options.concurrency, async (ip) => {
    if (options.shouldStop?.()) return;
    active += 1;
    options.onProbeStart?.(ip, active);
    try {
      const info = await client.getDeviceInfo(ip, { timeoutMs: options.timeoutMs });
      const hit = { ip, info };
      hits.push(hit);
      await options.onHit?.(hit);
    } catch {
      // why: an unreachable address is the normal case during a subnet sweep, not a scan failure
    } finally {
      active -= 1;
    }
  });

  return hits;
}
