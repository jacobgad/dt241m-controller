import { integer, sqliteTable, text } from "drizzle-orm/sqlite-core";

export const adapters = sqliteTable("adapters", {
  mac: text("mac").primaryKey(),
  name: text("name"),
  reportedName: text("reported_name"),
  role: text("role", { enum: ["transmitter", "receiver", "unknown"] }).notNull().default("unknown"),
  lastKnownIp: text("last_known_ip"),
  lastKnownChannel: integer("last_known_channel"),
  productName: text("product_name"),
  model: text("model"),
  firmware: text("firmware"),
  firstSeenAt: integer("first_seen_at", { mode: "timestamp_ms" }).notNull(),
  lastSeenAt: integer("last_seen_at", { mode: "timestamp_ms" })
});

export type AdapterRecord = typeof adapters.$inferSelect;
export type NewAdapterRecord = typeof adapters.$inferInsert;
