import { readFileSync } from "node:fs";
import { fileURLToPath } from "node:url";
import path from "node:path";
import { ZodError } from "zod";
import { loadConfig } from "./config.js";
import { Controller } from "./controller.js";
import { SqliteAdapterStore } from "./db/adapter-store.js";
import { openDatabase } from "./db/database.js";
import { createDt241mClient } from "./dt241m/client.js";
import { createLogger } from "./logger.js";
import { createMqttConnection } from "./mqtt/client.js";
import { PAYLOAD_OFFLINE, controllerTopics } from "./mqtt/topics.js";

const SUPPORT_URL = "https://github.com/jacobgad/dt241m-controller";

function readVersion(): string {
  try {
    const packagePath = path.resolve(path.dirname(fileURLToPath(import.meta.url)), "../package.json");
    const parsed = JSON.parse(readFileSync(packagePath, "utf8")) as { version?: string };
    return parsed.version ?? "0.0.0";
  } catch {
    return "0.0.0";
  }
}

function describeConfigError(error: unknown): string {
  if (error instanceof ZodError) {
    return error.issues.map((issue) => `${issue.path.join(".") || "(root)"}: ${issue.message}`).join("; ");
  }
  return error instanceof Error ? error.message : String(error);
}

async function main(): Promise<void> {
  const bootLog = createLogger("info");
  let config;
  try {
    config = loadConfig();
  } catch (error) {
    bootLog.error("config_invalid", { detail: describeConfigError(error) });
    process.exitCode = 1;
    return;
  }

  const log = createLogger(config.options.log_level);
  const version = readVersion();

  const database = openDatabase(config.databasePath);
  log.info("database_ready", { path: config.databasePath });

  const mqtt = createMqttConnection({
    settings: config.mqtt,
    will: { topic: controllerTopics.availability, payload: PAYLOAD_OFFLINE },
    clientId: `dt241m-controller-${process.pid}`,
    log
  });

  const controller = new Controller({
    client: createDt241mClient({ timeoutMs: config.options.probe_timeout_ms }),
    mqtt,
    store: new SqliteAdapterStore(database.db),
    options: config.options,
    log,
    discoveryContext: { version, supportUrl: SUPPORT_URL }
  });

  let shuttingDown = false;
  const shutdown = (signal: string) => {
    if (shuttingDown) return;
    shuttingDown = true;
    log.info("shutdown_requested", { signal });
    const forceExit = setTimeout(() => {
      log.error("shutdown_timeout");
      process.exit(1);
    }, 10_000);
    forceExit.unref();
    controller
      .stop()
      .then(() => {
        database.close();
        process.exit(0);
      })
      .catch((error: unknown) => {
        log.error("operation_failed", { operation: "shutdown", error });
        process.exit(1);
      });
  };
  process.on("SIGTERM", () => shutdown("SIGTERM"));
  process.on("SIGINT", () => shutdown("SIGINT"));

  await controller.start();
}

main().catch((error: unknown) => {
  createLogger("error").error("fatal", { error });
  process.exit(1);
});
