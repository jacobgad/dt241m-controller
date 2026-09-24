import type { DeviceInfo } from "./schemas.js";

export type AdapterRole = "transmitter" | "receiver" | "unknown";

const TX_MARKER = "proavtx";
const RX_MARKER = "proavrx";

export function classifyRole(info: Pick<DeviceInfo, "product_name" | "model">): AdapterRole {
  const haystack = [info.product_name, info.model]
    .filter((value): value is string => typeof value === "string")
    .map((value) => value.toLowerCase());

  const looksTx = haystack.some((value) => value.includes(TX_MARKER));
  const looksRx = haystack.some((value) => value.includes(RX_MARKER));

  if (looksTx && !looksRx) return "transmitter";
  if (looksRx && !looksTx) return "receiver";
  return "unknown";
}
