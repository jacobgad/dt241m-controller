import {
  RPC_FORM_FIELD,
  RPC_PATH,
  buildGetDeviceInfoRequest,
  buildSetChannelRequest,
  channelSchema,
  deviceInfoSchema,
  rpcEnvelopeSchema,
  setChannelResultSchema,
  type DeviceInfo
} from "./schemas.js";

export type Dt241mErrorCode =
  | "INPUT"
  | "HTTP_STATUS"
  | "INVALID_JSON"
  | "ENVELOPE"
  | "RPC_ERROR"
  | "RESULT_SHAPE"
  | "TIMEOUT"
  | "TRANSPORT"
  | "NOT_ACCEPTED";

export class Dt241mError extends Error {
  readonly code: Dt241mErrorCode;

  constructor(code: Dt241mErrorCode, message: string) {
    super(message);
    this.name = "Dt241mError";
    this.code = code;
  }
}

export function isDt241mError(error: unknown, code?: Dt241mErrorCode): error is Dt241mError {
  return error instanceof Dt241mError && (code === undefined || error.code === code);
}

export type SetChannelResult = { accepted: true };

export interface Dt241mClient {
  getDeviceInfo(ip: string, options?: RequestOptions): Promise<DeviceInfo>;
  setChannel(ip: string, channel: number, options?: RequestOptions): Promise<SetChannelResult>;
}

export type RequestOptions = { timeoutMs?: number };

export type Dt241mClientOptions = {
  fetchImpl?: typeof fetch;
  timeoutMs?: number;
  password?: string;
};

const IPV4_OCTET = /^(0|[1-9][0-9]{0,2})$/;

export function isIpv4(host: string): boolean {
  const parts = host.split(".");
  return parts.length === 4 && parts.every((part) => IPV4_OCTET.test(part) && Number(part) <= 255);
}

export function createDt241mClient(options: Dt241mClientOptions = {}): Dt241mClient {
  const fetchImpl = options.fetchImpl ?? globalThis.fetch;
  const defaultTimeoutMs = options.timeoutMs ?? 2000;
  const password = options.password ?? "";

  async function rpc(ip: string, body: string, timeoutMs: number): Promise<unknown> {
    if (!isIpv4(ip)) throw new Dt241mError("INPUT", "Expected an IPv4 address");
    if (!Number.isFinite(timeoutMs) || timeoutMs <= 0) throw new Dt241mError("INPUT", "Invalid timeout");

    const form = new FormData();
    form.set(RPC_FORM_FIELD, body);
    const controller = new AbortController();
    const timer = setTimeout(() => controller.abort(), timeoutMs);

    try {
      const response = await fetchImpl(`http://${ip}${RPC_PATH}`, {
        method: "POST",
        body: form,
        signal: controller.signal,
        redirect: "error"
      });
      if (!response.ok) {
        throw new Dt241mError("HTTP_STATUS", `Device returned HTTP ${response.status}`);
      }
      const text = await response.text();
      let envelopeRaw: unknown;
      try {
        envelopeRaw = JSON.parse(text);
      } catch {
        throw new Dt241mError("INVALID_JSON", "Device response was not valid JSON");
      }
      const envelope = rpcEnvelopeSchema.safeParse(envelopeRaw);
      if (!envelope.success) {
        throw new Dt241mError("ENVELOPE", "Unexpected JSON-RPC envelope or response ID");
      }
      if (envelope.data.error !== undefined) {
        throw new Dt241mError("RPC_ERROR", "Device returned a JSON-RPC error");
      }
      if (envelope.data.result === null || typeof envelope.data.result !== "object" || Array.isArray(envelope.data.result)) {
        throw new Dt241mError("RESULT_SHAPE", "Expected a JSON-RPC result object");
      }
      return envelope.data.result;
    } catch (error) {
      if (error instanceof Dt241mError) throw error;
      if (controller.signal.aborted) {
        throw new Dt241mError("TIMEOUT", "Request timed out; a write may still have applied");
      }
      throw new Dt241mError("TRANSPORT", "Could not complete the device HTTP request");
    } finally {
      clearTimeout(timer);
    }
  }

  return {
    async getDeviceInfo(ip, requestOptions = {}) {
      const result = await rpc(ip, buildGetDeviceInfoRequest(), requestOptions.timeoutMs ?? defaultTimeoutMs);
      const parsed = deviceInfoSchema.safeParse(result);
      if (!parsed.success) {
        throw new Dt241mError("RESULT_SHAPE", "Device info is missing required fields");
      }
      return parsed.data;
    },

    async setChannel(ip, channel, requestOptions = {}) {
      const validChannel = channelSchema.safeParse(channel);
      if (!validChannel.success) {
        throw new Dt241mError("INPUT", "Channel must be an integer between 0 and 255");
      }
      const result = await rpc(
        ip,
        buildSetChannelRequest(validChannel.data, password),
        requestOptions.timeoutMs ?? defaultTimeoutMs
      );
      const parsed = setChannelResultSchema.safeParse(result);
      if (!parsed.success || parsed.data.result !== true) {
        throw new Dt241mError("NOT_ACCEPTED", "Device did not explicitly acknowledge success");
      }
      return { accepted: true };
    }
  };
}
