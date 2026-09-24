import { fixtureResult, type FixtureName } from "./fixtures.js";

export type RecordedRequest = {
  url: string;
  ip: string;
  method: string;
  contentType: string | null;
  rawBody: string;
  dataField: string | null;
  rpc: { jsonrpc?: unknown; method?: unknown; params?: unknown; id?: unknown } | null;
};

export type SimulatedDeviceOptions = {
  mac: string;
  fixture: FixtureName;
  reportedChannel?: number;
  frontPanelChannel?: number;
  overrides?: Record<string, unknown>;
  ackWithoutApply?: boolean;
  writeHangsAfterApply?: boolean;
  readHangs?: boolean;
  rejectWrites?: boolean;
  responder?: (rpc: RecordedRequest["rpc"], device: SimulatedDevice) => string | Response | null;
};

export const DEVICE_RESPONSE_HEADERS = { "content-type": "text/html" } as const;

function deviceResponse(body: string): Response {
  return new Response(body, { status: 200, headers: DEVICE_RESPONSE_HEADERS });
}

export class SimulatedDevice {
  mac: string;
  template: Record<string, unknown>;
  reportedChannel: number;
  videoChannel: number;
  frontPanelChannel: number;
  ackWithoutApply: boolean;
  writeHangsAfterApply: boolean;
  readHangs: boolean;
  rejectWrites: boolean;
  responder: SimulatedDeviceOptions["responder"];
  writeCount = 0;

  constructor(options: SimulatedDeviceOptions) {
    this.mac = options.mac;
    this.template = { ...fixtureResult(options.fixture), ...options.overrides };
    const initial = options.reportedChannel ?? (this.template["channel_id"] as number);
    this.reportedChannel = initial;
    this.videoChannel = initial;
    this.frontPanelChannel = options.frontPanelChannel ?? initial;
    this.ackWithoutApply = options.ackWithoutApply ?? false;
    this.writeHangsAfterApply = options.writeHangsAfterApply ?? false;
    this.readHangs = options.readHangs ?? false;
    this.rejectWrites = options.rejectWrites ?? false;
    this.responder = options.responder;
  }

  infoPayload(ip: string): string {
    return JSON.stringify({
      jsonrpc: "2.0",
      id: 1,
      result: { ...this.template, lan_mac_addr: this.mac.toUpperCase(), lan_ip_addr: ip, channel_id: this.reportedChannel }
    });
  }

  applyChannel(channel: number): void {
    this.writeCount += 1;
    if (this.ackWithoutApply) return;
    this.reportedChannel = channel;
    this.videoChannel = channel;
  }
}

export class SimulatedNetwork {
  private readonly devices = new Map<string, SimulatedDevice>();
  readonly requests: RecordedRequest[] = [];
  unreachableMode: "refuse" | "hang" = "refuse";
  readonly fetch: typeof fetch;

  constructor() {
    this.fetch = (input, init) => this.handle(input, init);
  }

  place(ip: string, device: SimulatedDevice): SimulatedDevice {
    this.devices.set(ip, device);
    return device;
  }

  remove(ip: string): void {
    this.devices.delete(ip);
  }

  move(fromIp: string, toIp: string): void {
    const device = this.devices.get(fromIp);
    if (!device) throw new Error(`no device at ${fromIp}`);
    this.devices.delete(fromIp);
    this.devices.set(toIp, device);
  }

  deviceAt(ip: string): SimulatedDevice | undefined {
    return this.devices.get(ip);
  }

  requestsTo(ip: string, method?: string): RecordedRequest[] {
    return this.requests.filter((r) => r.ip === ip && (method === undefined || r.rpc?.method === method));
  }

  writesTo(ip: string): RecordedRequest[] {
    return this.requestsTo(ip, "set_channel_id");
  }

  private async handle(input: string | URL | Request, init?: RequestInit): Promise<Response> {
    const url = typeof input === "string" ? input : input instanceof URL ? input.toString() : input.url;
    const parsed = new URL(url);
    const ip = parsed.hostname;
    const signal = init?.signal ?? null;

    const recorded = await this.record(url, ip, init);
    this.requests.push(recorded);

    const device = this.devices.get(ip);
    if (!device) {
      if (this.unreachableMode === "hang") return this.hangUntilAbort(signal);
      throw new TypeError("fetch failed");
    }
    if (parsed.pathname !== "/cgi-bin/proav.cgi" || init?.method !== "POST") {
      return new Response("Not Found", { status: 404 });
    }

    if (device.responder) {
      const custom = device.responder(recorded.rpc, device);
      if (custom !== null && custom !== undefined) {
        return typeof custom === "string" ? deviceResponse(custom) : custom;
      }
    }

    const method = recorded.rpc?.method;
    if (method === "get_device_info_proav") {
      if (device.readHangs) return this.hangUntilAbort(signal);
      return deviceResponse(device.infoPayload(ip));
    }
    if (method === "set_channel_id") {
      const params = recorded.rpc?.params as { channel_id?: unknown } | undefined;
      if (device.rejectWrites) {
        return deviceResponse(JSON.stringify({ jsonrpc: "2.0", id: 1, result: { result: false } }));
      }
      if (typeof params?.channel_id === "number") device.applyChannel(params.channel_id);
      if (device.writeHangsAfterApply) return this.hangUntilAbort(signal);
      return deviceResponse(JSON.stringify({ jsonrpc: "2.0", id: 1, result: { result: true } }));
    }
    return deviceResponse(JSON.stringify({ jsonrpc: "2.0", id: 1, error: { code: -32601, message: "Method not found" } }));
  }

  private hangUntilAbort(signal: AbortSignal | null): Promise<Response> {
    return new Promise((_, reject) => {
      if (!signal) return;
      if (signal.aborted) reject(signal.reason ?? new DOMException("aborted", "AbortError"));
      signal.addEventListener("abort", () => reject(signal.reason ?? new DOMException("aborted", "AbortError")), {
        once: true
      });
    });
  }

  private async record(url: string, ip: string, init?: RequestInit): Promise<RecordedRequest> {
    const body = init?.body;
    let rawBody = "";
    let contentType: string | null = null;
    let dataField: string | null = null;
    if (body instanceof FormData) {
      const serialized = new Response(body);
      contentType = serialized.headers.get("content-type");
      rawBody = await serialized.text();
      const value = body.get("data");
      dataField = typeof value === "string" ? value : null;
    } else if (typeof body === "string") {
      rawBody = body;
      contentType = headerValue(init?.headers, "content-type");
    }
    let rpc: RecordedRequest["rpc"] = null;
    if (dataField !== null) {
      try {
        rpc = JSON.parse(dataField);
      } catch {
        rpc = null;
      }
    }
    return { url, ip, method: init?.method ?? "GET", contentType, rawBody, dataField, rpc };
  }
}

function headerValue(headers: HeadersInit | undefined, name: string): string | null {
  if (!headers) return null;
  return new Headers(headers).get(name);
}
