import { readFileSync } from "node:fs";
import { fileURLToPath } from "node:url";
import path from "node:path";

const fixturesDir = path.resolve(path.dirname(fileURLToPath(import.meta.url)), "../../../fixtures");

export type FixtureName =
  | "tx-info-initial-channel-3"
  | "tx-info-after-set-channel-4"
  | "tx-set-channel-4-success"
  | "rx-info-initial-channel-2"
  | "rx-set-channel-1-success";

export function loadFixtureText(name: FixtureName): string {
  return readFileSync(path.join(fixturesDir, `${name}.json`), "utf8");
}

export function loadFixture<T = unknown>(name: FixtureName): T {
  return JSON.parse(loadFixtureText(name)) as T;
}

export type FixtureEnvelope = { jsonrpc: "2.0"; id: 1; result: Record<string, unknown> };

export function fixtureResult(name: FixtureName): Record<string, unknown> {
  return loadFixture<FixtureEnvelope>(name).result;
}

export const TX_FIXTURE_MAC = "fc:19:28:6c:d2:91";
export const RX_FIXTURE_MAC = "fc:19:28:6c:d6:d8";
