import { adapterToRecord, recordToAdapter, type AdapterStore } from "../../db/adapter-store.js";
import type { AdapterRecord } from "../../db/schema.js";
import type { Adapter } from "../../registry.js";

export class MemoryAdapterStore implements AdapterStore {
  private readonly records = new Map<string, AdapterRecord>();

  loadAll(): Adapter[] {
    return [...this.records.values()].sort((a, b) => a.mac.localeCompare(b.mac)).map(recordToAdapter);
  }

  save(adapter: Adapter): void {
    this.records.set(adapter.mac, adapterToRecord(adapter));
  }

  setName(mac: string, name: string | null): void {
    const existing = this.records.get(mac);
    if (existing) existing.name = name;
  }
}
