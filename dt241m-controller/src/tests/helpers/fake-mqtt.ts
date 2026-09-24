import type { MessageHandler, MqttConnection, PublishOptions } from "../../mqtt/client.js";

export type PublishedMessage = { topic: string; payload: string; retain: boolean; qos: 0 | 1 };

export class FakeMqtt implements MqttConnection {
  readonly published: PublishedMessage[] = [];
  readonly subscriptions: string[] = [];
  private readonly messageHandlers: MessageHandler[] = [];
  private readonly connectHandlers: Array<() => void> = [];
  private readonly disconnectHandlers: Array<() => void> = [];
  private connected: boolean;
  ended = false;

  constructor(initiallyConnected = true) {
    this.connected = initiallyConnected;
  }

  async publish(topic: string, payload: string, options: PublishOptions): Promise<void> {
    this.published.push({ topic, payload, retain: options.retain, qos: options.qos ?? 1 });
  }

  async subscribe(topics: readonly string[]): Promise<void> {
    this.subscriptions.push(...topics);
  }

  onMessage(handler: MessageHandler): void {
    this.messageHandlers.push(handler);
  }

  onConnect(handler: () => void): void {
    this.connectHandlers.push(handler);
  }

  onDisconnect(handler: () => void): void {
    this.disconnectHandlers.push(handler);
  }

  isConnected(): boolean {
    return this.connected;
  }

  async end(): Promise<void> {
    this.ended = true;
    this.connected = false;
  }

  deliver(topic: string, payload: string): void {
    for (const handler of this.messageHandlers) handler(topic, payload);
  }

  simulateDisconnect(): void {
    this.connected = false;
    for (const handler of this.disconnectHandlers) handler();
  }

  simulateConnect(): void {
    this.connected = true;
    for (const handler of this.connectHandlers) handler();
  }

  clear(): void {
    this.published.length = 0;
  }

  messagesOn(topic: string): PublishedMessage[] {
    return this.published.filter((m) => m.topic === topic);
  }

  lastOn(topic: string): PublishedMessage | undefined {
    const matches = this.messagesOn(topic);
    return matches[matches.length - 1];
  }

  retainedSnapshot(): Map<string, string> {
    const snapshot = new Map<string, string>();
    for (const message of this.published) {
      if (!message.retain) continue;
      if (message.payload === "") snapshot.delete(message.topic);
      else snapshot.set(message.topic, message.payload);
    }
    return snapshot;
  }

  discoveryConfigs(): Map<string, Record<string, unknown>> {
    const configs = new Map<string, Record<string, unknown>>();
    for (const [topic, payload] of this.retainedSnapshot()) {
      if (topic.startsWith("homeassistant/") && topic.endsWith("/config")) {
        configs.set(topic, JSON.parse(payload) as Record<string, unknown>);
      }
    }
    return configs;
  }
}
