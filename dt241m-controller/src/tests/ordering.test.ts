import { describe, expect, it } from "vitest";
import { KeyedQueue } from "../concurrency.js";
import { deviceTopics } from "../mqtt/topics.js";
import { createHarness } from "./helpers/harness.js";
import { SimulatedDevice } from "./helpers/simulated-network.js";

const RX_A = "aa:aa:aa:aa:aa:aa";
const RX_B = "bb:bb:bb:bb:bb:bb";

describe("KeyedQueue", () => {
  it("serializes tasks with the same key and runs different keys concurrently", async () => {
    const queue = new KeyedQueue();
    const events: string[] = [];
    let releaseA: () => void = () => {};
    const gateA = new Promise<void>((resolve) => {
      releaseA = resolve;
    });

    const a1 = queue.enqueue("a", async () => {
      events.push("a1:start");
      await gateA;
      events.push("a1:end");
    });
    const a2 = queue.enqueue("a", async () => {
      events.push("a2:start");
      events.push("a2:end");
    });
    const b1 = queue.enqueue("b", async () => {
      events.push("b1:start");
      events.push("b1:end");
    });

    await b1;
    expect(events).toEqual(["a1:start", "b1:start", "b1:end"]);
    expect(queue.pendingCount("a")).toBe(2);
    releaseA();
    await Promise.all([a1, a2]);
    expect(events).toEqual(["a1:start", "b1:start", "b1:end", "a1:end", "a2:start", "a2:end"]);
    expect(queue.pendingCount("a")).toBe(0);
  });

  it("continues after a failed task", async () => {
    const queue = new KeyedQueue();
    await expect(queue.enqueue("k", async () => Promise.reject(new Error("boom")))).rejects.toThrow("boom");
    await expect(queue.enqueue("k", async () => "ok")).resolves.toBe("ok");
  });
});

describe("receiver command ordering", () => {
  it("applies same-receiver commands in order and the final state is the newest request", async () => {
    const h = createHarness();
    const device = h.network.place("192.168.1.5", new SimulatedDevice({ mac: RX_A, fixture: "rx-info-initial-channel-2" }));
    await h.controller.runDiscovery("start");

    let releaseFirstWrite: () => void = () => {};
    const firstWriteGate = new Promise<void>((resolve) => {
      releaseFirstWrite = resolve;
    });
    let writes = 0;
    device.responder = (rpc, dev) => {
      if (rpc?.method !== "set_channel_id") return null;
      writes += 1;
      const channel = (rpc.params as { channel_id: number }).channel_id;
      if (writes === 1) {
        return new Response(
          new ReadableStream({
            async start(controller) {
              await firstWriteGate;
              dev.applyChannel(channel);
              controller.enqueue(new TextEncoder().encode(JSON.stringify({ jsonrpc: "2.0", id: 1, result: { result: true } })));
              controller.close();
            }
          }),
          { status: 200 }
        );
      }
      dev.applyChannel(channel);
      return JSON.stringify({ jsonrpc: "2.0", id: 1, result: { result: true } });
    };

    const first = h.controller.requestChannelChange(RX_A, 2);
    const second = h.controller.requestChannelChange(RX_A, 5);
    await new Promise((resolve) => setTimeout(resolve, 10));
    expect(h.network.writesTo("192.168.1.5")).toHaveLength(1);

    releaseFirstWrite();
    const [firstOutcome, secondOutcome] = await Promise.all([first, second]);

    const writeOrder = h.network.writesTo("192.168.1.5").map((r) => (r.rpc?.params as { channel_id: number }).channel_id);
    expect(writeOrder).toEqual([2, 5]);
    expect(firstOutcome.status).toBe("matched");
    expect(secondOutcome.status).toBe("matched");
    expect(device.reportedChannel).toBe(5);
    expect(h.mqtt.lastOn(deviceTopics(RX_A).channelState)?.payload).toBe("5");

    const states = h.mqtt.messagesOn(deviceTopics(RX_A).channelState).map((m) => m.payload);
    expect(states.lastIndexOf("2")).toBeLessThan(states.lastIndexOf("5"));
  });

  it("runs commands for different receivers concurrently", async () => {
    const h = createHarness();
    const a = h.network.place("192.168.1.5", new SimulatedDevice({ mac: RX_A, fixture: "rx-info-initial-channel-2" }));
    const b = h.network.place("192.168.1.6", new SimulatedDevice({ mac: RX_B, fixture: "rx-info-initial-channel-2" }));
    await h.controller.runDiscovery("start");

    let releaseA: () => void = () => {};
    const gateA = new Promise<void>((resolve) => {
      releaseA = resolve;
    });
    a.responder = (rpc, dev) => {
      if (rpc?.method !== "set_channel_id") return null;
      return new Response(
        new ReadableStream({
          async start(controller) {
            await gateA;
            dev.applyChannel((rpc.params as { channel_id: number }).channel_id);
            controller.enqueue(new TextEncoder().encode(JSON.stringify({ jsonrpc: "2.0", id: 1, result: { result: true } })));
            controller.close();
          }
        }),
        { status: 200 }
      );
    };

    const first = h.controller.requestChannelChange(RX_A, 3);
    const second = h.controller.requestChannelChange(RX_B, 4);
    const secondOutcome = await second;
    expect(secondOutcome.status).toBe("matched");
    expect(b.reportedChannel).toBe(4);
    expect(a.reportedChannel).toBe(2);
    expect(a.writeCount).toBe(0);

    releaseA();
    expect((await first).status).toBe("matched");
    expect(a.reportedChannel).toBe(3);
    expect(a.writeCount).toBe(1);
  });
});
