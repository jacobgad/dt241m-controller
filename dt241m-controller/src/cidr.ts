export type Cidr = { network: number; prefix: number; text: string };

const CIDR_PATTERN = /^(\d{1,3})\.(\d{1,3})\.(\d{1,3})\.(\d{1,3})\/(\d{1,2})$/;

export const MIN_SCAN_PREFIX = 16;

const PRIVATE_BLOCKS: Cidr[] = [parseCidrStrict("10.0.0.0/8"), parseCidrStrict("172.16.0.0/12"), parseCidrStrict("192.168.0.0/16")];

export function ipv4ToNumber(ip: string): number | null {
  const parts = ip.split(".");
  if (parts.length !== 4) return null;
  let value = 0;
  for (const part of parts) {
    if (!/^\d{1,3}$/.test(part)) return null;
    const octet = Number(part);
    if (octet > 255) return null;
    value = value * 256 + octet;
  }
  return value;
}

export function numberToIpv4(value: number): string {
  return [(value >>> 24) & 255, (value >>> 16) & 255, (value >>> 8) & 255, value & 255].join(".");
}

export function parseCidr(text: string): Cidr | null {
  const match = CIDR_PATTERN.exec(text.trim());
  if (!match) return null;
  const prefix = Number(match[5]);
  if (prefix > 32) return null;
  const address = ipv4ToNumber(match.slice(1, 5).join("."));
  if (address === null) return null;
  const mask = prefix === 0 ? 0 : (0xffffffff << (32 - prefix)) >>> 0;
  return { network: (address & mask) >>> 0, prefix, text: `${numberToIpv4((address & mask) >>> 0)}/${prefix}` };
}

function parseCidrStrict(text: string): Cidr {
  const parsed = parseCidr(text);
  if (!parsed) throw new Error(`invalid CIDR ${text}`);
  return parsed;
}

export function cidrContains(outer: Cidr, inner: Cidr): boolean {
  if (inner.prefix < outer.prefix) return false;
  const mask = outer.prefix === 0 ? 0 : (0xffffffff << (32 - outer.prefix)) >>> 0;
  return ((inner.network & mask) >>> 0) === outer.network;
}

export function isPrivateCidr(cidr: Cidr): boolean {
  return PRIVATE_BLOCKS.some((block) => cidrContains(block, cidr));
}

export function expandCidrHosts(cidr: Cidr): string[] {
  const size = 2 ** (32 - cidr.prefix);
  if (cidr.prefix >= 31) {
    return Array.from({ length: size }, (_, i) => numberToIpv4(cidr.network + i));
  }
  const hosts: string[] = [];
  for (let i = 1; i < size - 1; i += 1) hosts.push(numberToIpv4(cidr.network + i));
  return hosts;
}

export function expandScanRanges(cidrs: Cidr[]): string[] {
  const seen = new Set<string>();
  const result: string[] = [];
  for (const cidr of cidrs) {
    for (const ip of expandCidrHosts(cidr)) {
      if (!seen.has(ip)) {
        seen.add(ip);
        result.push(ip);
      }
    }
  }
  return result;
}

export function ipInRanges(ip: string, cidrs: Cidr[]): boolean {
  const value = ipv4ToNumber(ip);
  if (value === null) return false;
  return cidrs.some((cidr) => {
    const mask = cidr.prefix === 0 ? 0 : (0xffffffff << (32 - cidr.prefix)) >>> 0;
    return ((value & mask) >>> 0) === cidr.network;
  });
}
