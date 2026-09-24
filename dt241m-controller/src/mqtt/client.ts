import mqtt, { type IClientOptions, type MqttClient } from "mqtt";
import type { MqttSettings } from "../config.js";
import type { Logger } from "../logger.js";

export type PublishOptions = { retain: boolean; qos?: 0 | 1 };

export type MessageHandler = (topic: string, payload: string) => void;

export interface MqttConnection {
  publish(topic: string, payload: string, options: PublishOptions): Promise<void>;
  subscribe(topics: readonly string[]): Promise<void>;
  onMessage(handler: MessageHandler): void;
  onConnect(handler: () => void): void;
  onDisconnect(handler: () => void): void;
  isConnected(): boolean;
  end(): Promise<void>;
}

export type MqttConnectionOptions = {
  settings: MqttSettings;
  will: { topic: string; payload: string };
  clientId: string;
  log: Logger;
};

export function createMqttConnection(options: MqttConnectionOptions): MqttConnection {
  const { settings, will, clientId, log } = options;
  const url = `${settings.tls ? "mqtts" : "mqtt"}://${settings.host}:${settings.port}`;

  const clientOptions: IClientOptions = {
    clientId,
    clean: true,
    reconnectPeriod: 5000,
    connectTimeout: 10_000,
    will: { topic: will.topic, payload: Buffer.from(will.payload), qos: 1, retain: true },
    ...(settings.username !== undefined ? { username: settings.username } : {}),
    ...(settings.password !== undefined ? { password: settings.password } : {})
  };

  log.info("mqtt_connecting", { host: settings.host, port: settings.port, tls: settings.tls });
  const client: MqttClient = mqtt.connect(url, clientOptions);

  const messageHandlers: MessageHandler[] = [];
  const connectHandlers: Array<() => void> = [];
  const disconnectHandlers: Array<() => void> = [];

  client.on("connect", () => {
    log.info("mqtt_connected", { host: settings.host, port: settings.port });
    for (const handler of connectHandlers) handler();
  });
  client.on("close", () => {
    log.warn("mqtt_disconnected");
    for (const handler of disconnectHandlers) handler();
  });
  client.on("reconnect", () => log.info("mqtt_reconnecting"));
  client.on("error", (error) => log.error("mqtt_error", { error }));
  client.on("message", (topic, payload) => {
    const text = payload.toString("utf8");
    for (const handler of messageHandlers) handler(topic, text);
  });

  return {
    publish(topic, payload, publishOptions) {
      return new Promise((resolve, reject) => {
        client.publish(topic, payload, { retain: publishOptions.retain, qos: publishOptions.qos ?? 1 }, (error) =>
          error ? reject(error) : resolve()
        );
      });
    },
    subscribe(topics) {
      return new Promise((resolve, reject) => {
        client.subscribe([...topics], { qos: 1 }, (error) => (error ? reject(error) : resolve()));
      });
    },
    onMessage: (handler) => void messageHandlers.push(handler),
    onConnect: (handler) => void connectHandlers.push(handler),
    onDisconnect: (handler) => void disconnectHandlers.push(handler),
    isConnected: () => client.connected,
    end() {
      return new Promise((resolve) => {
        client.end(false, {}, () => resolve());
      });
    }
  };
}
