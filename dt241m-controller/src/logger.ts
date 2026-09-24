export type LogLevel = "debug" | "info" | "warn" | "error";

const LEVEL_RANK: Record<LogLevel, number> = { debug: 10, info: 20, warn: 30, error: 40 };

export type LogFields = Record<string, unknown>;

export interface Logger {
  debug(event: string, fields?: LogFields): void;
  info(event: string, fields?: LogFields): void;
  warn(event: string, fields?: LogFields): void;
  error(event: string, fields?: LogFields): void;
}

export type LogSink = (line: string) => void;

function formatValue(value: unknown): string {
  if (value instanceof Error) return JSON.stringify(value.message);
  if (typeof value === "string") return /[\s="]/.test(value) ? JSON.stringify(value) : value;
  if (value === undefined) return "undefined";
  return JSON.stringify(value);
}

export function createLogger(level: LogLevel, sink: LogSink = (line) => process.stdout.write(`${line}\n`)): Logger {
  const threshold = LEVEL_RANK[level];

  function emit(entryLevel: LogLevel, event: string, fields: LogFields = {}): void {
    if (LEVEL_RANK[entryLevel] < threshold) return;
    const suffix = Object.entries(fields)
      .filter(([, value]) => value !== undefined)
      .map(([key, value]) => `${key}=${formatValue(value)}`)
      .join(" ");
    sink(`${new Date().toISOString()} ${entryLevel.toUpperCase().padEnd(5)} ${event}${suffix ? ` ${suffix}` : ""}`);
  }

  return {
    debug: (event, fields) => emit("debug", event, fields),
    info: (event, fields) => emit("info", event, fields),
    warn: (event, fields) => emit("warn", event, fields),
    error: (event, fields) => emit("error", event, fields)
  };
}

export const silentLogger: Logger = {
  debug: () => {},
  info: () => {},
  warn: () => {},
  error: () => {}
};
