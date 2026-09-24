import { compactMac } from "../mac.js";

export const TOPIC_PREFIX = "dt241m";
export const HA_DISCOVERY_PREFIX = "homeassistant";
export const HA_STATUS_TOPIC = `${HA_DISCOVERY_PREFIX}/status`;

export const PAYLOAD_ONLINE = "online";
export const PAYLOAD_OFFLINE = "offline";
export const PAYLOAD_PRESS = "PRESS";

export const CONTROLLER_NODE_ID = "dt241m_controller";
export const CONTROLLER_DEVICE_IDENTIFIER = "dt241m:controller";

export const controllerTopics = {
  availability: `${TOPIC_PREFIX}/controller/availability`,
  rescanPress: `${TOPIC_PREFIX}/controller/rescan/press`,
  knownDevicesState: `${TOPIC_PREFIX}/controller/known_devices/state`,
  onlineDevicesState: `${TOPIC_PREFIX}/controller/online_devices/state`
} as const;

export type DeviceTopics = {
  availability: string;
  channelState: string;
  channelSet: string;
  nameState: string;
  nameSet: string;
  ipState: string;
};

export function deviceTopics(mac: string): DeviceTopics {
  const base = `${TOPIC_PREFIX}/device/${compactMac(mac)}`;
  return {
    availability: `${base}/availability`,
    channelState: `${base}/channel/state`,
    channelSet: `${base}/channel/set`,
    nameState: `${base}/name/state`,
    nameSet: `${base}/name/set`,
    ipState: `${base}/ip/state`
  };
}

export const DEVICE_CHANNEL_SET_WILDCARD = `${TOPIC_PREFIX}/device/+/channel/set`;
export const DEVICE_NAME_SET_WILDCARD = `${TOPIC_PREFIX}/device/+/name/set`;

const DEVICE_COMMAND_PATTERN = new RegExp(`^${TOPIC_PREFIX}/device/([0-9a-f]{12})/(channel|name)/set$`);

export type DeviceCommandTopic = { mac: string; command: "channel" | "name" };

export function parseDeviceCommandTopic(topic: string): DeviceCommandTopic | null {
  const match = DEVICE_COMMAND_PATTERN.exec(topic);
  if (!match) return null;
  const compact = match[1]!;
  return { mac: compact.match(/.{2}/g)!.join(":"), command: match[2] as "channel" | "name" };
}

export function haDiscoveryTopic(component: string, nodeId: string, objectId: string): string {
  return `${HA_DISCOVERY_PREFIX}/${component}/${nodeId}/${objectId}/config`;
}

export function deviceNodeId(mac: string): string {
  return `dt241m_${compactMac(mac)}`;
}

export function deviceIdentifier(mac: string): string {
  return `dt241m:${compactMac(mac)}`;
}
