const MAC_HEX = /^[0-9a-f]{12}$/;

export function normalizeMac(raw: string): string | null {
  const compact = raw.trim().toLowerCase().replace(/[^0-9a-f]/g, "");
  if (!MAC_HEX.test(compact)) return null;
  return compact.match(/.{2}/g)!.join(":");
}

export function compactMac(mac: string): string {
  return mac.replace(/:/g, "");
}

export function adapterIdFromMac(mac: string): string {
  return `dt241m_${compactMac(mac)}`;
}
