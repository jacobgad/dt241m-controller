import { readFileSync } from "node:fs";
import { z } from "zod";
import { MIN_SCAN_PREFIX, isPrivateCidr, parseCidr, type Cidr } from "./cidr.js";

const scanRangeSchema = z
  .string()
  .transform((text, ctx): Cidr => {
    const parsed = parseCidr(text);
    if (!parsed) {
      ctx.addIssue({ code: "custom", message: `"${text}" is not a valid IPv4 CIDR such as 192.168.1.0/24` });
      return z.NEVER;
    }
    if (parsed.prefix < MIN_SCAN_PREFIX) {
      ctx.addIssue({ code: "custom", message: `"${text}" is too large; use a prefix of /${MIN_SCAN_PREFIX} or longer` });
      return z.NEVER;
    }
    if (!isPrivateCidr(parsed)) {
      ctx.addIssue({ code: "custom", message: `"${text}" is not a private (RFC 1918) range; refusing to scan it` });
      return z.NEVER;
    }
    return parsed;
  });

export const logLevelSchema = z.enum(["debug", "info", "warn", "error"]);

export const addonOptionsSchema = z.object({
  scan_ranges: z.array(scanRangeSchema).min(1, "Configure at least one scan range"),
  poll_interval_seconds: z.number().int().min(5).max(3600).default(15),
  probe_timeout_ms: z.number().int().min(200).max(30_000).default(2000),
  discovery_concurrency: z.number().int().min(1).max(64).default(8),
  log_level: logLevelSchema.default("info")
});

export type AddonOptions = z.infer<typeof addonOptionsSchema>;

const booleanish = z
  .union([z.boolean(), z.string()])
  .transform((value) => (typeof value === "boolean" ? value : ["true", "1", "yes"].includes(value.toLowerCase())));

export const mqttEnvSchema = z.object({
  MQTT_HOST: z.string().min(1, "MQTT_HOST is required"),
  MQTT_PORT: z.coerce.number().int().min(1).max(65535).default(1883),
  MQTT_USERNAME: z.string().optional(),
  MQTT_PASSWORD: z.string().optional(),
  MQTT_SSL: booleanish.default(false)
});

export type MqttSettings = {
  host: string;
  port: number;
  username: string | undefined;
  password: string | undefined;
  tls: boolean;
};

export type AppConfig = {
  options: AddonOptions;
  mqtt: MqttSettings;
  databasePath: string;
};

const DEFAULT_DATABASE_PATH = "/data/dt241m.sqlite";

export function parseAddonOptions(raw: unknown): AddonOptions {
  return addonOptionsSchema.parse(raw);
}

export function parseMqttSettings(env: NodeJS.ProcessEnv): MqttSettings {
  const parsed = mqttEnvSchema.parse(env);
  return {
    host: parsed.MQTT_HOST,
    port: parsed.MQTT_PORT,
    username: parsed.MQTT_USERNAME || undefined,
    password: parsed.MQTT_PASSWORD || undefined,
    tls: parsed.MQTT_SSL
  };
}

export function loadConfig(env: NodeJS.ProcessEnv = process.env): AppConfig {
  const optionsPath = env["DT241M_OPTIONS_PATH"] ?? "/data/options.json";
  const raw: unknown = JSON.parse(readFileSync(optionsPath, "utf8"));
  return {
    options: parseAddonOptions(raw),
    mqtt: parseMqttSettings(env),
    databasePath: env["DT241M_DATABASE_PATH"] ?? DEFAULT_DATABASE_PATH
  };
}
