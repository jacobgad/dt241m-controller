import { MAX_NAME_LENGTH, displayName, type Adapter } from "../registry.js";
import {
  CONTROLLER_DEVICE_IDENTIFIER,
  CONTROLLER_NODE_ID,
  PAYLOAD_OFFLINE,
  PAYLOAD_ONLINE,
  PAYLOAD_PRESS,
  controllerTopics,
  deviceIdentifier,
  deviceNodeId,
  deviceTopics,
  haDiscoveryTopic
} from "./topics.js";

export type DiscoveryMessage = { topic: string; payload: Record<string, unknown> };

export type DiscoveryContext = {
  version: string;
  supportUrl: string;
};

const MANUFACTURER = "PWAY";
const CONTROLLER_NAME = "DT241M Controller";

export const ICONS = {
  receiver: "mdi:monitor",
  transmitter: "mdi:broadcast",
  unknown: "mdi:help-network",
  controller: "mdi:video-switch",
  rescan: "mdi:radar",
  name: "mdi:rename-box",
  ip: "mdi:ip-network",
  role: "mdi:swap-horizontal"
} as const;

function origin(ctx: DiscoveryContext) {
  return { name: CONTROLLER_NAME, sw_version: ctx.version, support_url: ctx.supportUrl };
}

function controllerAvailability() {
  return {
    topic: controllerTopics.availability,
    payload_available: PAYLOAD_ONLINE,
    payload_not_available: PAYLOAD_OFFLINE
  };
}

function controllerDevice(ctx: DiscoveryContext) {
  return {
    identifiers: [CONTROLLER_DEVICE_IDENTIFIER],
    name: CONTROLLER_NAME,
    manufacturer: "dt241m-controller add-on",
    model: "DT241M MQTT bridge",
    sw_version: ctx.version
  };
}

function adapterDevice(adapter: Adapter) {
  return {
    identifiers: [deviceIdentifier(adapter.mac)],
    connections: [["mac", adapter.mac]],
    name: displayName(adapter),
    manufacturer: MANUFACTURER,
    model: adapter.productName ?? adapter.model ?? "DT241M",
    model_id: adapter.model ?? undefined,
    sw_version: adapter.firmware ?? undefined,
    via_device: CONTROLLER_DEVICE_IDENTIFIER
  };
}

function adapterAvailability(adapter: Adapter) {
  const topics = deviceTopics(adapter.mac);
  return {
    availability: [
      controllerAvailability(),
      { topic: topics.availability, payload_available: PAYLOAD_ONLINE, payload_not_available: PAYLOAD_OFFLINE }
    ],
    availability_mode: "all"
  };
}

export function receiverChannelDiscovery(adapter: Adapter, ctx: DiscoveryContext): DiscoveryMessage {
  const topics = deviceTopics(adapter.mac);
  return {
    topic: haDiscoveryTopic("number", deviceNodeId(adapter.mac), "channel"),
    payload: {
      name: "Channel",
      unique_id: `${adapter.id}_channel`,
      object_id: `${adapter.id}_channel`,
      state_topic: topics.channelState,
      command_topic: topics.channelSet,
      min: 0,
      max: 255,
      step: 1,
      mode: "box",
      optimistic: false,
      retain: false,
      qos: 1,
      icon: ICONS.receiver,
      ...adapterAvailability(adapter),
      device: adapterDevice(adapter),
      origin: origin(ctx)
    }
  };
}

export function readOnlyChannelDiscovery(adapter: Adapter, ctx: DiscoveryContext): DiscoveryMessage {
  const topics = deviceTopics(adapter.mac);
  return {
    topic: haDiscoveryTopic("sensor", deviceNodeId(adapter.mac), "channel"),
    payload: {
      name: "Channel",
      unique_id: `${adapter.id}_channel`,
      object_id: `${adapter.id}_channel`,
      state_topic: topics.channelState,
      icon: ICONS[adapter.role],
      ...adapterAvailability(adapter),
      device: adapterDevice(adapter),
      origin: origin(ctx)
    }
  };
}

export function ipAddressDiscovery(adapter: Adapter, ctx: DiscoveryContext): DiscoveryMessage {
  const topics = deviceTopics(adapter.mac);
  return {
    topic: haDiscoveryTopic("sensor", deviceNodeId(adapter.mac), "ip_address"),
    payload: {
      name: "IP address",
      unique_id: `${adapter.id}_ip_address`,
      object_id: `${adapter.id}_ip_address`,
      state_topic: topics.ipState,
      entity_category: "diagnostic",
      icon: ICONS.ip,
      ...adapterAvailability(adapter),
      device: adapterDevice(adapter),
      origin: origin(ctx)
    }
  };
}

export function nameDiscovery(adapter: Adapter, ctx: DiscoveryContext): DiscoveryMessage {
  const topics = deviceTopics(adapter.mac);
  return {
    topic: haDiscoveryTopic("text", deviceNodeId(adapter.mac), "name"),
    payload: {
      name: "Name",
      unique_id: `${adapter.id}_name`,
      object_id: `${adapter.id}_name`,
      state_topic: topics.nameState,
      command_topic: topics.nameSet,
      min: 0,
      max: MAX_NAME_LENGTH,
      mode: "text",
      retain: false,
      qos: 1,
      entity_category: "config",
      icon: ICONS.name,
      availability: [controllerAvailability()],
      device: adapterDevice(adapter),
      origin: origin(ctx)
    }
  };
}

export function roleDiscovery(adapter: Adapter, ctx: DiscoveryContext): DiscoveryMessage {
  const topics = deviceTopics(adapter.mac);
  return {
    topic: haDiscoveryTopic("sensor", deviceNodeId(adapter.mac), "role"),
    payload: {
      name: "Role",
      unique_id: `${adapter.id}_role`,
      object_id: `${adapter.id}_role`,
      state_topic: topics.roleState,
      entity_category: "diagnostic",
      icon: ICONS.role,
      ...adapterAvailability(adapter),
      device: adapterDevice(adapter),
      origin: origin(ctx)
    }
  };
}

export function adapterDiscoveryMessages(adapter: Adapter, ctx: DiscoveryContext): DiscoveryMessage[] {
  const channel = adapter.role === "receiver" ? receiverChannelDiscovery(adapter, ctx) : readOnlyChannelDiscovery(adapter, ctx);
  return [channel, nameDiscovery(adapter, ctx), ipAddressDiscovery(adapter, ctx), roleDiscovery(adapter, ctx)];
}

export function staleAdapterDiscoveryTopics(adapter: Adapter): string[] {
  const other = adapter.role === "receiver" ? "sensor" : "number";
  return [haDiscoveryTopic(other, deviceNodeId(adapter.mac), "channel")];
}

export function rescanButtonDiscovery(ctx: DiscoveryContext): DiscoveryMessage {
  return {
    topic: haDiscoveryTopic("button", CONTROLLER_NODE_ID, "rescan"),
    payload: {
      name: "Rescan network",
      unique_id: `${CONTROLLER_NODE_ID}_rescan`,
      object_id: `${CONTROLLER_NODE_ID}_rescan`,
      command_topic: controllerTopics.rescanPress,
      payload_press: PAYLOAD_PRESS,
      retain: false,
      qos: 1,
      icon: ICONS.rescan,
      availability: [controllerAvailability()],
      device: controllerDevice(ctx),
      origin: origin(ctx)
    }
  };
}

export function knownDevicesDiscovery(ctx: DiscoveryContext): DiscoveryMessage {
  return {
    topic: haDiscoveryTopic("sensor", CONTROLLER_NODE_ID, "known_devices"),
    payload: {
      name: "Known devices",
      unique_id: `${CONTROLLER_NODE_ID}_known_devices`,
      object_id: `${CONTROLLER_NODE_ID}_known_devices`,
      state_topic: controllerTopics.knownDevicesState,
      state_class: "measurement",
      icon: ICONS.controller,
      availability: [controllerAvailability()],
      device: controllerDevice(ctx),
      origin: origin(ctx)
    }
  };
}

export function onlineDevicesDiscovery(ctx: DiscoveryContext): DiscoveryMessage {
  return {
    topic: haDiscoveryTopic("sensor", CONTROLLER_NODE_ID, "online_devices"),
    payload: {
      name: "Online devices",
      unique_id: `${CONTROLLER_NODE_ID}_online_devices`,
      object_id: `${CONTROLLER_NODE_ID}_online_devices`,
      state_topic: controllerTopics.onlineDevicesState,
      state_class: "measurement",
      icon: ICONS.controller,
      availability: [controllerAvailability()],
      device: controllerDevice(ctx),
      origin: origin(ctx)
    }
  };
}

export function controllerDiscoveryMessages(ctx: DiscoveryContext): DiscoveryMessage[] {
  return [rescanButtonDiscovery(ctx), knownDevicesDiscovery(ctx), onlineDevicesDiscovery(ctx)];
}
