import { classifyRole, type AdapterRole } from "./dt241m/classify.js";
import type { DeviceInfo } from "./dt241m/schemas.js";
import { adapterIdFromMac, normalizeMac } from "./mac.js";

export type Adapter = {
  mac: string;
  id: string;
  role: AdapterRole;
  ip: string;
  name: string | null;
  reportedName: string | null;
  productName: string | null;
  model: string | null;
  firmware: string | null;
  channel: number | null;
  online: boolean;
  firstSeenAt: number;
  lastSeenAt: number | null;
  rawCapabilities: unknown | null;
};

export const MAX_NAME_LENGTH = 64;

export type NameValidation = { ok: true; name: string | null } | { ok: false; reason: string };

export function validateName(raw: string): NameValidation {
  const trimmed = raw.trim();
  if (trimmed.length === 0) return { ok: true, name: null };
  if (trimmed.length > MAX_NAME_LENGTH) return { ok: false, reason: `name longer than ${MAX_NAME_LENGTH} characters` };
  if (/[\u0000-\u001f\u007f]/.test(trimmed)) return { ok: false, reason: "name contains control characters" };
  return { ok: true, name: trimmed };
}

export function displayName(adapter: Pick<Adapter, "name" | "reportedName" | "productName" | "id">): string {
  return adapter.name ?? adapter.reportedName ?? adapter.productName ?? adapter.id;
}

export type ObservationResult = {
  adapter: Adapter;
  created: boolean;
  cameOnline: boolean;
  ipChanged: boolean;
  previousIp: string | null;
  channelChanged: boolean;
  metadataChanged: boolean;
  roleChanged: boolean;
  displaced: Adapter | null;
};

export type RegistryCounts = { known: number; online: number };

export class Registry {
  private readonly adapters = new Map<string, Adapter>();

  hydrate(adapters: readonly Adapter[]): void {
    for (const adapter of adapters) {
      this.adapters.set(adapter.mac, { ...adapter, online: false });
    }
  }

  get(mac: string): Adapter | undefined {
    return this.adapters.get(mac);
  }

  setName(mac: string, name: string | null): Adapter | null {
    const adapter = this.adapters.get(mac);
    if (!adapter) return null;
    adapter.name = name;
    return adapter;
  }

  getById(id: string): Adapter | undefined {
    for (const adapter of this.adapters.values()) {
      if (adapter.id === id) return adapter;
    }
    return undefined;
  }

  all(): Adapter[] {
    return [...this.adapters.values()];
  }

  onlineAtIp(ip: string): Adapter | undefined {
    for (const adapter of this.adapters.values()) {
      if (adapter.ip === ip && adapter.online) return adapter;
    }
    return undefined;
  }

  counts(): RegistryCounts {
    let online = 0;
    for (const adapter of this.adapters.values()) if (adapter.online) online += 1;
    return { known: this.adapters.size, online };
  }

  recordObservation(ip: string, info: DeviceInfo, now: number): ObservationResult | null {
    const mac = normalizeMac(info.lan_mac_addr);
    if (!mac) return null;

    const role = classifyRole(info);
    const displaced = this.displaceOtherAdapterAt(ip, mac);
    const existing = this.adapters.get(mac);

    if (!existing) {
      const adapter: Adapter = {
        mac,
        id: adapterIdFromMac(mac),
        role,
        ip,
        name: null,
        reportedName: info.dev_name ?? null,
        productName: info.product_name ?? null,
        model: info.model ?? null,
        firmware: info.version ?? null,
        channel: info.channel_id,
        online: true,
        firstSeenAt: now,
        lastSeenAt: now,
        rawCapabilities: info.capability ?? null
      };
      this.adapters.set(mac, adapter);
      return {
        adapter,
        created: true,
        cameOnline: true,
        ipChanged: false,
        previousIp: null,
        channelChanged: true,
        metadataChanged: true,
        roleChanged: false,
        displaced
      };
    }

    const previousIp = existing.ip;
    const ipChanged = previousIp !== ip;
    const channelChanged = existing.channel !== info.channel_id;
    const cameOnline = !existing.online;
    const nextName = info.dev_name ?? null;
    const nextProduct = info.product_name ?? null;
    const nextModel = info.model ?? null;
    const nextFirmware = info.version ?? null;
    const roleChanged = existing.role !== role;
    const metadataChanged =
      existing.reportedName !== nextName ||
      existing.productName !== nextProduct ||
      existing.model !== nextModel ||
      existing.firmware !== nextFirmware ||
      existing.role !== role;

    existing.ip = ip;
    existing.role = role;
    existing.reportedName = nextName;
    existing.productName = nextProduct;
    existing.model = nextModel;
    existing.firmware = nextFirmware;
    existing.channel = info.channel_id;
    existing.online = true;
    existing.lastSeenAt = now;
    existing.rawCapabilities = info.capability ?? existing.rawCapabilities;

    return {
      adapter: existing,
      created: false,
      cameOnline,
      ipChanged,
      previousIp: ipChanged ? previousIp : null,
      channelChanged,
      metadataChanged,
      roleChanged,
      displaced
    };
  }

  markOffline(mac: string): Adapter | null {
    const adapter = this.adapters.get(mac);
    if (!adapter || !adapter.online) return null;
    adapter.online = false;
    return adapter;
  }

  private displaceOtherAdapterAt(ip: string, mac: string): Adapter | null {
    for (const adapter of this.adapters.values()) {
      if (adapter.mac !== mac && adapter.ip === ip && adapter.online) {
        adapter.online = false;
        return adapter;
      }
    }
    return null;
  }
}
