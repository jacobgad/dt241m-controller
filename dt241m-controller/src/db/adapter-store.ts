import { eq } from "drizzle-orm";
import type { Adapter } from "../registry.js";
import { adapterIdFromMac } from "../mac.js";
import type { Db } from "./database.js";
import { adapters, type AdapterRecord } from "./schema.js";

export interface AdapterStore {
  loadAll(): Adapter[];
  save(adapter: Adapter): void;
  setName(mac: string, name: string | null): void;
}

export function recordToAdapter(record: AdapterRecord): Adapter {
  return {
    mac: record.mac,
    id: adapterIdFromMac(record.mac),
    role: record.role,
    ip: record.lastKnownIp ?? "",
    name: record.name,
    reportedName: record.reportedName,
    productName: record.productName,
    model: record.model,
    firmware: record.firmware,
    channel: record.lastKnownChannel,
    online: false,
    firstSeenAt: record.firstSeenAt.getTime(),
    lastSeenAt: record.lastSeenAt?.getTime() ?? null,
    rawCapabilities: null
  };
}

type AdapterFields = Omit<AdapterRecord, "mac">;

function adapterFields(adapter: Adapter): AdapterFields {
  return {
    name: adapter.name,
    reportedName: adapter.reportedName,
    role: adapter.role,
    lastKnownIp: adapter.ip || null,
    lastKnownChannel: adapter.channel,
    productName: adapter.productName,
    model: adapter.model,
    firmware: adapter.firmware,
    firstSeenAt: new Date(adapter.firstSeenAt),
    lastSeenAt: adapter.lastSeenAt === null ? null : new Date(adapter.lastSeenAt)
  };
}

export function adapterToRecord(adapter: Adapter): AdapterRecord {
  return { mac: adapter.mac, ...adapterFields(adapter) };
}

export class SqliteAdapterStore implements AdapterStore {
  constructor(private readonly db: Db) {}

  loadAll(): Adapter[] {
    return this.db.select().from(adapters).orderBy(adapters.mac).all().map(recordToAdapter);
  }

  save(adapter: Adapter): void {
    const fields = adapterFields(adapter);
    this.db
      .insert(adapters)
      .values({ mac: adapter.mac, ...fields })
      .onConflictDoUpdate({ target: adapters.mac, set: fields })
      .run();
  }

  setName(mac: string, name: string | null): void {
    this.db.update(adapters).set({ name }).where(eq(adapters.mac, mac)).run();
  }
}
