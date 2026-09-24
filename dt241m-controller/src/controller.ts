import { expandScanRanges, type Cidr } from "./cidr.js";
import { KeyedQueue, mapWithConcurrency } from "./concurrency.js";
import type { AddonOptions } from "./config.js";
import type { AdapterStore } from "./db/adapter-store.js";
import { probeAddresses } from "./discovery.js";
import { isDt241mError, type Dt241mClient } from "./dt241m/client.js";
import { channelSchema, type DeviceInfo } from "./dt241m/schemas.js";
import type { Logger } from "./logger.js";
import { normalizeMac } from "./mac.js";
import type { MqttConnection } from "./mqtt/client.js";
import {
  adapterDiscoveryMessages,
  controllerDiscoveryMessages,
  staleAdapterDiscoveryTopics,
  type DiscoveryContext
} from "./mqtt/discovery.js";
import { SUBSCRIPTIONS, createMessageRouter } from "./mqtt/handlers.js";
import { PAYLOAD_OFFLINE, PAYLOAD_ONLINE, controllerTopics, deviceTopics } from "./mqtt/topics.js";
import { createPoller, type Poller } from "./poller.js";
import { Registry, displayName, validateName, type Adapter, type ObservationResult } from "./registry.js";

export type ControllerDeps = {
  client: Dt241mClient;
  mqtt: MqttConnection;
  store: AdapterStore;
  options: AddonOptions;
  log: Logger;
  discoveryContext: DiscoveryContext;
  now?: () => number;
  sleep?: (ms: number) => Promise<void>;
  readback?: { attempts: number; delayMs: number };
};

export type ChannelChangeOutcome =
  | { status: "matched"; mac: string; ip: string; requested: number; reported: number }
  | { status: "mismatch"; mac: string; ip: string; requested: number; reported: number | null; writeAccepted: boolean | "unknown" }
  | { status: "failed"; mac: string; ip: string | null; requested: number; reason: string; reported: number | null };

export type ChannelChangeRejection =
  | "invalid_channel"
  | "unknown_device"
  | "not_a_receiver"
  | "shutting_down";

export type RenameOutcome = { status: "renamed"; mac: string; name: string | null } | { status: "rejected"; mac: string; reason: string };

export class ChannelChangeRejected extends Error {
  readonly reason: ChannelChangeRejection;

  constructor(reason: ChannelChangeRejection, message: string) {
    super(message);
    this.name = "ChannelChangeRejected";
    this.reason = reason;
  }
}

const RETAINED = { retain: true, qos: 1 } as const;

export class Controller {
  readonly registry = new Registry();

  private readonly client: Dt241mClient;
  private readonly mqtt: MqttConnection;
  private readonly store: AdapterStore;
  private readonly options: AddonOptions;
  private readonly log: Logger;
  private readonly discoveryContext: DiscoveryContext;
  private readonly now: () => number;
  private readonly sleep: (ms: number) => Promise<void>;
  private readonly readbackPolicy: { attempts: number; delayMs: number };
  private readonly scanRanges: Cidr[];
  private readonly writeQueue = new KeyedQueue();
  private readonly poller: Poller;

  private discoveryInFlight: Promise<void> | null = null;
  private discoveryRuns = 0;
  private stopping = false;
  private started = false;
  private lastCounts: { known: number; online: number } | null = null;

  constructor(deps: ControllerDeps) {
    this.client = deps.client;
    this.mqtt = deps.mqtt;
    this.store = deps.store;
    this.options = deps.options;
    this.log = deps.log;
    this.discoveryContext = deps.discoveryContext;
    this.now = deps.now ?? (() => Date.now());
    this.sleep = deps.sleep ?? ((ms) => new Promise((resolve) => setTimeout(resolve, ms)));
    this.readbackPolicy = deps.readback ?? { attempts: 3, delayMs: 400 };
    this.scanRanges = deps.options.scan_ranges;
    this.poller = createPoller(deps.options.poll_interval_seconds * 1000, () => this.pollKnownDevices());

    this.mqtt.onMessage(
      createMessageRouter(
        {
          onChannelCommand: (mac, channel) => this.requestChannelChange(mac, channel),
          onNameCommand: (mac, rawName) => this.rename(mac, rawName),
          onRescanRequested: () => this.runDiscovery("manual_rescan"),
          onHomeAssistantOnline: () => this.publishEverything()
        },
        this.log
      )
    );
    this.mqtt.onConnect(() => {
      void this.onMqttConnected().catch((error: unknown) => this.log.error("operation_failed", { operation: "mqtt_connect", error }));
    });
  }

  async start(): Promise<void> {
    if (this.started) return;
    this.started = true;
    this.log.info("controller_started", {
      scanRanges: this.scanRanges.map((c) => c.text).join(","),
      pollIntervalSeconds: this.options.poll_interval_seconds,
      probeTimeoutMs: this.options.probe_timeout_ms,
      discoveryConcurrency: this.options.discovery_concurrency
    });
    const persisted = this.store.loadAll();
    this.registry.hydrate(persisted);
    this.log.info("adapters_loaded", { count: persisted.length });
    if (this.mqtt.isConnected()) await this.onMqttConnected();
    try {
      await this.probeKnownAddresses();
      await this.runDiscovery("startup");
    } catch (error) {
      this.log.error("operation_failed", { operation: "startup_discovery", error });
    }
    this.poller.start();
  }

  async probeKnownAddresses(): Promise<void> {
    const candidates = this.registry.all().filter((adapter) => adapter.ip !== "");
    if (candidates.length === 0) return;
    this.log.info("known_addresses_probe_started", { count: candidates.length });
    await mapWithConcurrency(candidates, this.options.discovery_concurrency, async (adapter) => {
      await this.observeAt(adapter.ip, adapter.mac);
    });
  }

  async rename(mac: string, rawName: string): Promise<RenameOutcome> {
    const normalizedMac = normalizeMac(mac);
    const adapter = normalizedMac ? this.registry.get(normalizedMac) : undefined;
    if (!adapter) {
      this.log.warn("rename_unknown_device", { mac });
      return { status: "rejected", mac, reason: "unknown_device" };
    }
    const validation = validateName(rawName);
    if (!validation.ok) {
      this.log.warn("rename_rejected", { mac: adapter.mac, reason: validation.reason });
      await this.publishAdapterName(adapter);
      return { status: "rejected", mac: adapter.mac, reason: validation.reason };
    }
    const previous = adapter.name;
    this.registry.setName(adapter.mac, validation.name);
    this.store.setName(adapter.mac, validation.name);
    this.log.info("adapter_renamed", { mac: adapter.mac, previousName: previous, name: validation.name, reportedName: adapter.reportedName });
    await this.publishAdapterName(adapter);
    await this.publishAdapterDiscovery(adapter);
    return { status: "renamed", mac: adapter.mac, name: validation.name };
  }

  async stop(): Promise<void> {
    this.stopping = true;
    this.poller.stop();
    if (this.discoveryInFlight) await this.discoveryInFlight.catch(() => {});
    try {
      if (this.mqtt.isConnected()) {
        await this.mqtt.publish(controllerTopics.availability, PAYLOAD_OFFLINE, RETAINED);
      }
    } catch (error) {
      this.log.warn("operation_failed", { operation: "publish_offline", error });
    }
    await this.mqtt.end();
    this.log.info("controller_stopped");
  }

  discoveryRunCount(): number {
    return this.discoveryRuns;
  }

  isDiscoveryRunning(): boolean {
    return this.discoveryInFlight !== null;
  }

  async publishEverything(): Promise<void> {
    await this.mqtt.publish(controllerTopics.availability, PAYLOAD_ONLINE, RETAINED);
    for (const message of controllerDiscoveryMessages(this.discoveryContext)) {
      await this.mqtt.publish(message.topic, JSON.stringify(message.payload), RETAINED);
    }
    for (const adapter of this.registry.all()) {
      await this.publishAdapterDiscovery(adapter);
      await this.publishAdapterAvailability(adapter);
      await this.publishAdapterName(adapter);
      await this.publishAdapterState(adapter);
    }
    await this.publishCounts(true);
  }

  runDiscovery(reason: string): Promise<void> {
    if (this.discoveryInFlight) {
      this.log.debug("discovery_already_running", { reason });
      return this.discoveryInFlight;
    }
    if (this.stopping) return Promise.resolve();
    const run = this.executeDiscovery(reason).finally(() => {
      this.discoveryInFlight = null;
    });
    this.discoveryInFlight = run;
    return run;
  }

  async pollKnownDevices(): Promise<void> {
    if (this.stopping) return;
    const adapters = this.registry.all();
    if (adapters.length === 0) return;
    let needsRediscovery = false;

    await mapWithConcurrency(adapters, this.options.discovery_concurrency, async (adapter) => {
      const outcome = await this.observeAt(adapter.ip, adapter.mac);
      if (outcome === "verified") return;
      if (outcome === "other_device") {
        needsRediscovery = true;
        return;
      }
      const wasOnline = adapter.online;
      if (this.registry.markOffline(adapter.mac)) {
        this.log.warn("adapter_offline", { mac: adapter.mac, ip: adapter.ip, role: adapter.role });
        await this.publishAdapterAvailability(adapter);
        await this.publishCounts();
        this.persist(adapter);
      }
      if (wasOnline) needsRediscovery = true;
    });

    if (needsRediscovery && !this.stopping) {
      await this.runDiscovery("device_disappeared");
    }
  }

  async requestChannelChange(mac: string, channel: number): Promise<ChannelChangeOutcome> {
    if (this.stopping) throw new ChannelChangeRejected("shutting_down", "Controller is shutting down");
    const validChannel = channelSchema.safeParse(channel);
    if (!validChannel.success) throw new ChannelChangeRejected("invalid_channel", "Channel must be an integer 0-255");
    const normalizedMac = normalizeMac(mac);
    const adapter = normalizedMac ? this.registry.get(normalizedMac) : undefined;
    if (!adapter) {
      this.log.warn("channel_command_unknown_device", { mac });
      throw new ChannelChangeRejected("unknown_device", `No known device with MAC ${mac}`);
    }
    if (adapter.role !== "receiver") {
      this.log.warn("channel_command_refused_role", { mac: adapter.mac, role: adapter.role, requestedChannel: channel });
      throw new ChannelChangeRejected("not_a_receiver", `Device ${adapter.mac} is a ${adapter.role}, not a receiver`);
    }
    this.log.info("channel_change_requested", { mac: adapter.mac, ip: adapter.ip, requestedChannel: validChannel.data });
    return this.writeQueue.enqueue(adapter.mac, () => this.executeChannelChange(adapter.mac, validChannel.data));
  }

  private async executeChannelChange(mac: string, requested: number): Promise<ChannelChangeOutcome> {
    const ip = await this.resolveVerifiedIp(mac);
    if (ip === null) {
      const outcome: ChannelChangeOutcome = { status: "failed", mac, ip: null, requested, reason: "device_not_located", reported: null };
      this.log.error("operation_failed", { mac, requestedChannel: requested, reason: outcome.reason });
      return outcome;
    }

    const adapter = this.registry.get(mac)!;
    if (adapter.role !== "receiver") {
      this.log.warn("channel_command_refused_role", { mac, role: adapter.role, requestedChannel: requested });
      return { status: "failed", mac, ip, requested, reason: "not_a_receiver", reported: adapter.channel };
    }

    let writeAccepted: boolean | "unknown";
    try {
      await this.client.setChannel(ip, requested, { timeoutMs: this.options.probe_timeout_ms });
      writeAccepted = true;
      this.log.info("channel_change_acknowledged", { mac, ip, requestedChannel: requested });
    } catch (error) {
      if (isDt241mError(error, "TIMEOUT")) {
        writeAccepted = "unknown";
        this.log.warn("channel_change_ambiguous", { mac, ip, requestedChannel: requested });
      } else {
        writeAccepted = false;
        this.log.error("operation_failed", { mac, ip, requestedChannel: requested, error });
      }
    }

    const reported = await this.readbackChannel(mac, ip, requested);

    if (writeAccepted === false) {
      return { status: "failed", mac, ip, requested, reason: "write_rejected", reported };
    }
    if (reported === requested) {
      this.log.info("channel_readback_matched", { mac, ip, requestedChannel: requested, reportedChannel: reported });
      return { status: "matched", mac, ip, requested, reported };
    }
    this.log.warn("channel_readback_mismatch", { mac, ip, requestedChannel: requested, reportedChannel: reported, writeAccepted });
    return { status: "mismatch", mac, ip, requested, reported, writeAccepted };
  }

  private async readbackChannel(mac: string, ip: string, requested: number): Promise<number | null> {
    let reported: number | null = null;
    for (let attempt = 1; attempt <= this.readbackPolicy.attempts; attempt += 1) {
      if (attempt > 1) await this.sleep(this.readbackPolicy.delayMs);
      try {
        const info = await this.client.getDeviceInfo(ip, { timeoutMs: this.options.probe_timeout_ms });
        if (normalizeMac(info.lan_mac_addr) !== mac) {
          this.log.error("identity_mismatch", { mac, ip, observedMac: normalizeMac(info.lan_mac_addr), phase: "readback" });
          await this.applyObservation(ip, info);
          return null;
        }
        await this.applyObservation(ip, info);
        reported = info.channel_id;
        if (reported === requested) return reported;
      } catch (error) {
        this.log.warn("channel_readback_unavailable", { mac, ip, attempt, error });
      }
    }
    return reported;
  }

  private async resolveVerifiedIp(mac: string): Promise<string | null> {
    const adapter = this.registry.get(mac);
    if (!adapter) return null;

    const outcome = await this.observeAt(adapter.ip, mac);
    if (outcome === "verified") return adapter.ip;

    if (outcome === "unreachable" && this.registry.markOffline(mac)) {
      this.log.warn("adapter_offline", { mac, ip: adapter.ip, role: adapter.role });
      await this.publishAdapterAvailability(adapter);
      await this.publishCounts();
      this.persist(adapter);
    }

    this.log.info("rediscovery_for_write", { mac, staleIp: adapter.ip, reason: outcome });
    await this.runDiscovery(`locate_${mac}`);

    const located = this.registry.get(mac);
    if (located && located.online) return located.ip;
    this.log.error("device_not_located", { mac, lastKnownIp: adapter.ip });
    return null;
  }

  private async observeAt(ip: string, expectedMac: string): Promise<"verified" | "other_device" | "unreachable"> {
    let info: DeviceInfo;
    try {
      info = await this.client.getDeviceInfo(ip, { timeoutMs: this.options.probe_timeout_ms });
    } catch {
      return "unreachable";
    }
    const observedMac = normalizeMac(info.lan_mac_addr);
    if (observedMac !== expectedMac) {
      this.log.warn("identity_mismatch", { mac: expectedMac, ip, observedMac });
      await this.applyObservation(ip, info);
      return "other_device";
    }
    await this.applyObservation(ip, info);
    return "verified";
  }

  private async executeDiscovery(reason: string): Promise<void> {
    const targets = expandScanRanges(this.scanRanges);
    this.discoveryRuns += 1;
    this.log.info("discovery_started", { reason, addresses: targets.length, ranges: this.scanRanges.map((c) => c.text).join(",") });
    const startedAt = this.now();
    let found = 0;

    await probeAddresses(this.client, targets, {
      concurrency: this.options.discovery_concurrency,
      timeoutMs: this.options.probe_timeout_ms,
      shouldStop: () => this.stopping,
      onHit: async ({ ip, info }) => {
        found += 1;
        await this.applyObservation(ip, info).catch((error: unknown) => this.log.error("operation_failed", { ip, error }));
      }
    });

    await this.publishCounts();
    this.log.info("discovery_completed", { reason, found, known: this.registry.counts().known, durationMs: this.now() - startedAt });
  }

  private async applyObservation(ip: string, info: DeviceInfo): Promise<ObservationResult | null> {
    const result = this.registry.recordObservation(ip, info, this.now());
    if (!result) {
      this.log.warn("adapter_invalid_mac", { ip, reportedMac: info.lan_mac_addr });
      return null;
    }
    const { adapter } = result;

    if (result.displaced) {
      this.log.warn("adapter_ip_taken_over", { mac: result.displaced.mac, ip, byMac: adapter.mac });
      await this.publishAdapterAvailability(result.displaced);
    }

    if (result.created) {
      this.log.info("adapter_discovered", { mac: adapter.mac, ip, role: adapter.role, reportedName: adapter.reportedName, channel: adapter.channel });
      this.persist(adapter);
      await this.publishAdapterDiscovery(adapter);
      await this.publishAdapterAvailability(adapter);
      await this.publishAdapterName(adapter);
      await this.publishAdapterState(adapter);
      await this.publishCounts();
      return result;
    }

    if (result.ipChanged) {
      this.log.info("adapter_ip_changed", { mac: adapter.mac, previousIp: result.previousIp, ip, role: adapter.role });
    }
    if (result.metadataChanged) {
      if (result.roleChanged) {
        for (const topic of staleAdapterDiscoveryTopics(adapter)) await this.mqtt.publish(topic, "", RETAINED);
      }
      await this.publishAdapterDiscovery(adapter);
      await this.publishAdapterName(adapter);
    }
    if (result.cameOnline) {
      this.log.info("adapter_online", { mac: adapter.mac, ip, role: adapter.role });
      await this.publishAdapterAvailability(adapter);
    }
    if (result.channelChanged) {
      this.log.info("adapter_channel_observed", { mac: adapter.mac, ip, role: adapter.role, reportedChannel: adapter.channel });
    }
    if (result.channelChanged || result.ipChanged || result.cameOnline) {
      await this.publishAdapterState(adapter);
    }
    if (result.cameOnline || result.displaced) await this.publishCounts();
    if (result.ipChanged || result.channelChanged || result.metadataChanged || result.cameOnline) {
      this.persist(adapter);
    }
    return result;
  }

  private persist(adapter: Adapter): void {
    try {
      this.store.save(adapter);
    } catch (error) {
      this.log.error("persist_failed", { mac: adapter.mac, error });
    }
  }

  private async publishAdapterName(adapter: Adapter): Promise<void> {
    await this.mqtt.publish(deviceTopics(adapter.mac).nameState, displayName(adapter), RETAINED);
  }

  private async publishAdapterDiscovery(adapter: Adapter): Promise<void> {
    for (const message of adapterDiscoveryMessages(adapter, this.discoveryContext)) {
      await this.mqtt.publish(message.topic, JSON.stringify(message.payload), RETAINED);
    }
  }

  private async publishAdapterAvailability(adapter: Adapter): Promise<void> {
    await this.mqtt.publish(deviceTopics(adapter.mac).availability, adapter.online ? PAYLOAD_ONLINE : PAYLOAD_OFFLINE, RETAINED);
  }

  private async publishAdapterState(adapter: Adapter): Promise<void> {
    const topics = deviceTopics(adapter.mac);
    if (adapter.channel !== null) await this.mqtt.publish(topics.channelState, String(adapter.channel), RETAINED);
    if (adapter.ip !== "") await this.mqtt.publish(topics.ipState, adapter.ip, RETAINED);
  }

  private async publishCounts(force = false): Promise<void> {
    const counts = this.registry.counts();
    if (!force && this.lastCounts && this.lastCounts.known === counts.known && this.lastCounts.online === counts.online) return;
    this.lastCounts = counts;
    await this.mqtt.publish(controllerTopics.knownDevicesState, String(counts.known), RETAINED);
    await this.mqtt.publish(controllerTopics.onlineDevicesState, String(counts.online), RETAINED);
  }

  private async onMqttConnected(): Promise<void> {
    await this.mqtt.subscribe(SUBSCRIPTIONS);
    await this.publishEverything();
  }
}
