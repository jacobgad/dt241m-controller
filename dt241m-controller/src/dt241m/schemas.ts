import { z } from "zod";

export const channelSchema = z.number().int().min(0).max(255);

export const RPC_ID = 1 as const;
export const RPC_PATH = "/cgi-bin/proav.cgi";
export const RPC_FORM_FIELD = "data";

export const rpcMethodSchema = z.enum(["get_device_info_proav", "set_channel_id"]);
export type RpcMethod = z.infer<typeof rpcMethodSchema>;

export const rpcEnvelopeSchema = z.looseObject({
  jsonrpc: z.literal("2.0"),
  id: z.literal(RPC_ID),
  result: z.unknown().optional(),
  error: z.unknown().optional()
});

export const capabilityEntrySchema = z.looseObject({
  enable: z.boolean().optional(),
  range: z.string().optional()
});

export const deviceInfoSchema = z.looseObject({
  dev_name: z.string().nullable().optional(),
  product_name: z.string().nullable().optional(),
  model: z.string().nullable().optional(),
  version: z.string().nullable().optional(),
  lan_mac_addr: z.string(),
  lan_ip_addr: z.string().nullable().optional(),
  channel_id: channelSchema,
  capability: z.record(z.string(), capabilityEntrySchema).optional()
});

export type DeviceInfo = z.infer<typeof deviceInfoSchema>;

export const setChannelResultSchema = z.looseObject({
  result: z.boolean()
});

export function buildRpcRequest(method: RpcMethod, params: Record<string, unknown>): string {
  return JSON.stringify({ jsonrpc: "2.0", method, params, id: RPC_ID });
}

export function buildGetDeviceInfoRequest(): string {
  return buildRpcRequest("get_device_info_proav", {});
}

export function buildSetChannelRequest(channel: number, password = ""): string {
  return buildRpcRequest("set_channel_id", { pswd: password, channel_id: channelSchema.parse(channel) });
}
