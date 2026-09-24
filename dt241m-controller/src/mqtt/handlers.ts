import type { Logger } from "../logger.js";
import {
  DEVICE_CHANNEL_SET_WILDCARD,
  DEVICE_NAME_SET_WILDCARD,
  HA_STATUS_TOPIC,
  PAYLOAD_ONLINE,
  controllerTopics,
  parseDeviceCommandTopic
} from "./topics.js";

export type InboundActions = {
  onChannelCommand(mac: string, channel: number): Promise<unknown>;
  onNameCommand(mac: string, rawName: string): Promise<unknown>;
  onRescanRequested(): Promise<unknown>;
  onHomeAssistantOnline(): Promise<unknown>;
};

export const SUBSCRIPTIONS: readonly string[] = [
  DEVICE_CHANNEL_SET_WILDCARD,
  DEVICE_NAME_SET_WILDCARD,
  controllerTopics.rescanPress,
  HA_STATUS_TOPIC
];

export function parseChannelPayload(payload: string): number | null {
  const trimmed = payload.trim();
  if (!/^[+-]?\d+(\.0+)?$/.test(trimmed)) return null;
  const value = Number(trimmed);
  if (!Number.isInteger(value) || value < 0 || value > 255) return null;
  return value;
}

export function createMessageRouter(actions: InboundActions, log: Logger): (topic: string, payload: string) => void {
  return (topic, payload) => {
    const deviceCommand = parseDeviceCommandTopic(topic);
    if (deviceCommand?.command === "channel") {
      const channel = parseChannelPayload(payload);
      if (channel === null) {
        log.warn("channel_command_invalid", { mac: deviceCommand.mac, payload });
        return;
      }
      void actions.onChannelCommand(deviceCommand.mac, channel).catch((error: unknown) => {
        log.error("operation_failed", { mac: deviceCommand.mac, requestedChannel: channel, error });
      });
      return;
    }
    if (deviceCommand?.command === "name") {
      void actions.onNameCommand(deviceCommand.mac, payload).catch((error: unknown) => {
        log.error("operation_failed", { mac: deviceCommand.mac, operation: "rename", error });
      });
      return;
    }

    if (topic === controllerTopics.rescanPress) {
      void actions.onRescanRequested().catch((error: unknown) => log.error("operation_failed", { operation: "rescan", error }));
      return;
    }

    if (topic === HA_STATUS_TOPIC) {
      if (payload.trim() === PAYLOAD_ONLINE) {
        log.info("home_assistant_online");
        void actions.onHomeAssistantOnline().catch((error: unknown) => log.error("operation_failed", { operation: "republish", error }));
      }
      return;
    }

    log.debug("mqtt_message_ignored", { topic });
  };
}
