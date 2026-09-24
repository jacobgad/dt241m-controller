import { describe, expect, it } from "vitest";
import { Dt241mError, createDt241mClient, isDt241mError } from "../dt241m/client.js";
import { channelSchema, deviceInfoSchema, rpcEnvelopeSchema } from "../dt241m/schemas.js";
import { RX_FIXTURE_MAC, TX_FIXTURE_MAC, fixtureResult, loadFixture, loadFixtureText } from "./helpers/fixtures.js";
import { SimulatedDevice, SimulatedNetwork } from "./helpers/simulated-network.js";

const TX_IP = "10.0.0.11";
const RX_IP = "10.0.0.12";

function network() {
  const net = new SimulatedNetwork();
  net.place(TX_IP, new SimulatedDevice({ mac: TX_FIXTURE_MAC, fixture: "tx-info-initial-channel-3" }));
  net.place(RX_IP, new SimulatedDevice({ mac: RX_FIXTURE_MAC, fixture: "rx-info-initial-channel-2" }));
  return net;
}

describe("DT241M request construction", () => {
  it("posts multipart/form-data with a generated boundary to /cgi-bin/proav.cgi", async () => {
    const net = network();
    const client = createDt241mClient({ fetchImpl: net.fetch });
    await client.getDeviceInfo(RX_IP);

    const [request] = net.requests;
    expect(request).toBeDefined();
    expect(request!.url).toBe(`http://${RX_IP}/cgi-bin/proav.cgi`);
    expect(request!.method).toBe("POST");
    expect(request!.contentType).toMatch(/^multipart\/form-data; boundary=.+/);
    expect(request!.rawBody).toContain('Content-Disposition: form-data; name="data"');
    expect(request!.rawBody).not.toMatch(/filename=/);
  });

  it("uses a single text form field named data", async () => {
    const net = network();
    const client = createDt241mClient({ fetchImpl: net.fetch });
    await client.getDeviceInfo(RX_IP);
    const request = net.requests[0]!;
    expect(request.dataField).not.toBeNull();
    expect((request.rawBody.match(/name="/g) ?? []).length).toBe(1);
  });

  it("sends the documented get_device_info_proav JSON-RPC request", async () => {
    const net = network();
    const client = createDt241mClient({ fetchImpl: net.fetch });
    await client.getDeviceInfo(TX_IP);
    expect(net.requests[0]!.rpc).toEqual({ jsonrpc: "2.0", method: "get_device_info_proav", params: {}, id: 1 });
  });

  it("sends the documented set_channel_id JSON-RPC request with blank pswd", async () => {
    const net = network();
    const client = createDt241mClient({ fetchImpl: net.fetch });
    await client.setChannel(RX_IP, 2);
    expect(net.requests[0]!.rpc).toEqual({
      jsonrpc: "2.0",
      method: "set_channel_id",
      params: { pswd: "", channel_id: 2 },
      id: 1
    });
  });

  it("keeps numeric RPC id 1 across successive requests", async () => {
    const net = network();
    const client = createDt241mClient({ fetchImpl: net.fetch });
    await client.getDeviceInfo(RX_IP);
    await client.setChannel(RX_IP, 5);
    await client.getDeviceInfo(RX_IP);
    for (const request of net.requests) {
      expect(request.rpc?.id).toBe(1);
      expect(typeof request.rpc?.id).toBe("number");
    }
  });

  it("does not set a JSON content type", async () => {
    const net = network();
    const client = createDt241mClient({ fetchImpl: net.fetch });
    await client.getDeviceInfo(RX_IP);
    expect(net.requests[0]!.contentType).not.toContain("application/json");
  });

  it("rejects non-IPv4 hosts without sending anything", async () => {
    const net = network();
    const client = createDt241mClient({ fetchImpl: net.fetch });
    await expect(client.getDeviceInfo("example.com")).rejects.toMatchObject({ code: "INPUT" });
    await expect(client.getDeviceInfo("http://10.0.0.1")).rejects.toMatchObject({ code: "INPUT" });
    expect(net.requests).toHaveLength(0);
  });
});

describe("DT241M captured fixture parsing", () => {
  it("parses the real transmitter fixture", () => {
    const envelope = rpcEnvelopeSchema.parse(loadFixture("tx-info-initial-channel-3"));
    const info = deviceInfoSchema.parse(envelope.result);
    expect(info.lan_mac_addr).toBe("FC:19:28:6C:D2:91");
    expect(info.channel_id).toBe(3);
    expect(info.product_name).toBe("ProAVTx ET01");
    expect(info.model).toBe("am_8270_proavtx-eth_et01-pway-dt241");
    expect(info.version).toBe("1.13471.133");
    expect(info["wifi_mac_addr"]).toBeNull();
    expect(info["resolution"]).toBe("1920x1080 @60Hz RGB");
    expect(info.capability?.["set_channel_id"]).toEqual({ enable: true, range: "[0,255]" });
  });

  it("parses the post-write transmitter fixture and sees channel 4", () => {
    const info = deviceInfoSchema.parse(fixtureResult("tx-info-after-set-channel-4"));
    expect(info.channel_id).toBe(4);
  });

  it("parses the real receiver fixture", () => {
    const info = deviceInfoSchema.parse(fixtureResult("rx-info-initial-channel-2"));
    expect(info.lan_mac_addr).toBe("FC:19:28:6C:D6:D8");
    expect(info.channel_id).toBe(2);
    expect(info.dev_name).toBe("ER02_286CD6D8");
    expect(info.product_name).toBe("ProAVRx ER01");
    expect(info["resolution"]).toBe("1920x1080_60P");
    expect(info.capability?.["emergency"]).toEqual({ enable: true });
  });

  it("preserves unknown fields", () => {
    const info = deviceInfoSchema.parse({ ...fixtureResult("rx-info-initial-channel-2"), future_field: "keep me" });
    expect(info["future_field"]).toBe("keep me");
    expect(info["swsp_mode"]).toBe("SW");
  });

  it("returns the fixture payload through the client unchanged", async () => {
    const net = new SimulatedNetwork();
    net.place(
      RX_IP,
      new SimulatedDevice({
        mac: RX_FIXTURE_MAC,
        fixture: "rx-info-initial-channel-2",
        responder: () => loadFixtureText("rx-info-initial-channel-2")
      })
    );
    const client = createDt241mClient({ fetchImpl: net.fetch });
    const info = await client.getDeviceInfo(RX_IP);
    expect(info).toEqual(fixtureResult("rx-info-initial-channel-2"));
  });

  it("accepts the captured RX write acknowledgement", async () => {
    const net = new SimulatedNetwork();
    net.place(
      RX_IP,
      new SimulatedDevice({
        mac: RX_FIXTURE_MAC,
        fixture: "rx-info-initial-channel-2",
        responder: (rpc) => (rpc?.method === "set_channel_id" ? loadFixtureText("rx-set-channel-1-success") : null)
      })
    );
    const client = createDt241mClient({ fetchImpl: net.fetch });
    await expect(client.setChannel(RX_IP, 1)).resolves.toEqual({ accepted: true });
    expect(loadFixture("rx-set-channel-1-success")).toEqual(loadFixture("tx-set-channel-4-success"));
  });

  it("parses JSON even though the firmware labels responses text/html", async () => {
    const net = new SimulatedNetwork();
    net.place(
      RX_IP,
      new SimulatedDevice({
        mac: RX_FIXTURE_MAC,
        fixture: "rx-info-initial-channel-2",
        responder: () => new Response(loadFixtureText("rx-info-initial-channel-2"), { status: 200, headers: { "content-type": "text/html" } })
      })
    );
    const client = createDt241mClient({ fetchImpl: net.fetch });
    await expect(client.getDeviceInfo(RX_IP)).resolves.toMatchObject({ channel_id: 2 });
  });

  it("accepts the captured TX write acknowledgement", async () => {
    const net = new SimulatedNetwork();
    net.place(
      TX_IP,
      new SimulatedDevice({
        mac: TX_FIXTURE_MAC,
        fixture: "tx-info-initial-channel-3",
        responder: (rpc) => (rpc?.method === "set_channel_id" ? loadFixtureText("tx-set-channel-4-success") : null)
      })
    );
    const client = createDt241mClient({ fetchImpl: net.fetch });
    await expect(client.setChannel(TX_IP, 4)).resolves.toEqual({ accepted: true });
  });
});

describe("DT241M synthetic failure handling", () => {
  function respondingWith(body: string | Response) {
    const net = new SimulatedNetwork();
    net.place(RX_IP, new SimulatedDevice({ mac: RX_FIXTURE_MAC, fixture: "rx-info-initial-channel-2", responder: () => body }));
    return createDt241mClient({ fetchImpl: net.fetch });
  }

  it("rejects malformed JSON", async () => {
    await expect(respondingWith("<html>oops</html>").getDeviceInfo(RX_IP)).rejects.toMatchObject({ code: "INVALID_JSON" });
  });

  it("rejects HTTP error statuses", async () => {
    await expect(respondingWith(new Response("busy", { status: 503 })).getDeviceInfo(RX_IP)).rejects.toMatchObject({
      code: "HTTP_STATUS"
    });
  });

  it("rejects JSON-RPC error objects", async () => {
    const body = JSON.stringify({ jsonrpc: "2.0", id: 1, error: { code: -32000, message: "denied" } });
    await expect(respondingWith(body).setChannel(RX_IP, 2)).rejects.toMatchObject({ code: "RPC_ERROR" });
  });

  it("rejects an unexpected envelope id", async () => {
    const body = JSON.stringify({ jsonrpc: "2.0", id: 7, result: { result: true } });
    await expect(respondingWith(body).setChannel(RX_IP, 2)).rejects.toMatchObject({ code: "ENVELOPE" });
  });

  it("rejects nested result false", async () => {
    const body = JSON.stringify({ jsonrpc: "2.0", id: 1, result: { result: false } });
    await expect(respondingWith(body).setChannel(RX_IP, 2)).rejects.toMatchObject({ code: "NOT_ACCEPTED" });
  });

  it("does not treat a truthy outer result as acceptance", async () => {
    const body = JSON.stringify({ jsonrpc: "2.0", id: 1, result: {} });
    await expect(respondingWith(body).setChannel(RX_IP, 2)).rejects.toMatchObject({ code: "NOT_ACCEPTED" });
  });

  it("rejects device info missing channel_id", async () => {
    const body = JSON.stringify({ jsonrpc: "2.0", id: 1, result: { lan_mac_addr: "FC:19:28:6C:D6:D8" } });
    await expect(respondingWith(body).getDeviceInfo(RX_IP)).rejects.toMatchObject({ code: "RESULT_SHAPE" });
  });

  it("reports a timeout distinctly", async () => {
    const net = new SimulatedNetwork();
    net.place(RX_IP, new SimulatedDevice({ mac: RX_FIXTURE_MAC, fixture: "rx-info-initial-channel-2", readHangs: true }));
    const client = createDt241mClient({ fetchImpl: net.fetch, timeoutMs: 20 });
    const error = await client.getDeviceInfo(RX_IP).catch((e: unknown) => e);
    expect(isDt241mError(error, "TIMEOUT")).toBe(true);
    expect(error).toBeInstanceOf(Dt241mError);
  });

  it("reports a refused connection as a transport error", async () => {
    const net = new SimulatedNetwork();
    const client = createDt241mClient({ fetchImpl: net.fetch });
    await expect(client.getDeviceInfo("10.0.0.99")).rejects.toMatchObject({ code: "TRANSPORT" });
  });
});

describe("channel validation", () => {
  it.each([0, 1, 2, 255])("accepts integer channel %d", (channel) => {
    expect(channelSchema.safeParse(channel).success).toBe(true);
  });

  it.each([-1, 256, 1.5, Number.NaN, Number.POSITIVE_INFINITY])("rejects %s", (channel) => {
    expect(channelSchema.safeParse(channel).success).toBe(false);
  });

  it("rejects string channels", () => {
    expect(channelSchema.safeParse("2").success).toBe(false);
    expect(channelSchema.safeParse("02").success).toBe(false);
  });

  it("client rejects invalid channels before sending", async () => {
    const net = network();
    const client = createDt241mClient({ fetchImpl: net.fetch });
    for (const bad of [-1, 256, 2.5, "2" as unknown as number]) {
      await expect(client.setChannel(RX_IP, bad)).rejects.toMatchObject({ code: "INPUT" });
    }
    expect(net.requests).toHaveLength(0);
  });

  it("client sends channel 0 and 255 through the transport", async () => {
    const net = network();
    const client = createDt241mClient({ fetchImpl: net.fetch });
    await client.setChannel(RX_IP, 0);
    await client.setChannel(RX_IP, 255);
    const channels = net.writesTo(RX_IP).map((r) => (r.rpc?.params as { channel_id: number }).channel_id);
    expect(channels).toEqual([0, 255]);
  });
});
