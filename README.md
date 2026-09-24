# DT241M Controller

A Home Assistant add-on repository containing **DT241M Controller**: a headless MQTT bridge for PWAY DT241M HDMI-over-IP transmitters and receivers.

The add-on's only job is to be a reliable hardware driver. It discovers devices, tracks them by MAC address, exposes each receiver's channel as a writable Home Assistant entity, and reads state back from the hardware. Scenes, groups, automations, dashboards and scripts are all built in Home Assistant itself.

## Architecture

```text
Home Assistant
      |
      | MQTT (Discovery, state, commands)
      v
Mosquitto broker
      |
      v
DT241M Controller add-on
      |
      ├── CIDR discovery (HTTP probes)
      ├── Adapter registry (in memory) + SQLite inventory (/data)
      ├── Polling
      ├── MQTT Discovery publisher
      ├── Per-receiver write queue + readback
      └── DT241M HTTP client   (the only code that speaks to the hardware)
              |
              | POST /cgi-bin/proav.cgi  multipart field "data" = JSON-RPC
              v
       DT241M transmitters / receivers
```

There is no web UI, no REST API and no custom Home Assistant integration.

## Supported hardware

This add-on supports the DT241M family that was reverse-engineered, running firmware `1.13471.133`:

- Transmitter `ProAVTx ET01` (`am_8270_proavtx-eth_et01-pway-dt241`)
- Receiver `ProAVRx ER01` (`am_8270_proavrx-eth_er01-pway-dt241`)

It does **not** claim support for every PWAY HDMI-over-IP product. Devices whose `product_name`/`model` cannot be classified are shown as `unknown` and are never written to.

## Hardware validation

Validated on 24 September 2026 against a live installation of 16 units (3 transmitters, 13 receivers), all on firmware `1.13471.133`, reached over a VPN:

- A `/24` scan discovered and correctly classified all 16 devices in ~61 s (2 s probe timeout, 8 concurrent)
- Polling every 15 s ran without availability flapping and without sending any writes
- A receiver channel change issued through the MQTT command topic completed the verify-MAC → `set_channel_id` → readback path in ~400 ms; the operator confirmed the video source switched, and the change was reverted the same way
- The receiver `set_channel_id` acknowledgement was captured and matches the transmitter shape: `{"jsonrpc":"2.0","id":1,"result":{"result":true}}`
- All responses arrive as `HTTP 200` with `Content-type: text/html` (lighttpd 1.4.35) despite JSON bodies

Not yet verified: behaviour inside a live Supervisor install (bashio MQTT lookup, s6 `init: false`), DHCP address changes on real hardware, channel 0 semantics, and whether receiver front-panel digits update after an HTTP change.

## Protocol disclaimer

The device protocol was reverse-engineered from observed HTTP traffic and captured JSON responses; it is not vendor documentation. Only two methods are used, exactly as observed:

- `get_device_info_proav` — read device identity, metadata and reported channel
- `set_channel_id` — request a receiver channel change (`{"pswd":"","channel_id":N}`)

No other RPC methods are guessed or implemented. Behaviour on other firmware versions is untested. The captured responses the implementation is based on are in `dt241m-controller/fixtures/`, with provenance in `fixtures/README.md`.

## Home Assistant MQTT model

| Topic | Purpose |
| --- | --- |
| `dt241m/controller/availability` | Controller online/offline (retained, also the MQTT Last Will) |
| `dt241m/controller/rescan/press` | Rescan button command |
| `dt241m/controller/known_devices/state`, `.../online_devices/state` | Inventory counters |
| `dt241m/device/<mac>/availability` | Per-adapter availability |
| `dt241m/device/<mac>/channel/state` | Reported channel (retained) |
| `dt241m/device/<mac>/channel/set` | Receiver channel command (receivers only) |
| `dt241m/device/<mac>/name/state`, `.../name/set` | User-editable name |
| `dt241m/device/<mac>/ip/state` | Current IP (diagnostic) |
| `dt241m/device/<mac>/role/state` | `receiver` / `transmitter` / `unknown` (diagnostic) |

`<mac>` is the separator-free lowercase MAC, e.g. `fc19286cd6d8`. The Home Assistant device identifier is `dt241m:<mac>` and entity unique IDs are `dt241m_<mac>_channel`, `dt241m_<mac>_name`, `dt241m_<mac>_ip_address`, `dt241m_<mac>_role`. IP addresses and names never appear in topics or identifiers.

Discovery payloads are published under `homeassistant/<component>/dt241m_<mac>/<object>/config`.

## Supported architectures

- `aarch64`
- `amd64`

The image is built from `ghcr.io/home-assistant/base` (Alpine) with Node.js from the Alpine repositories. SQLite is provided by `better-sqlite3`, pinned to a release that ships prebuilt `linuxmusl-x64` / `linuxmusl-arm64` binaries for the Node 24 ABI, so no compiler toolchain is needed in the image. The Dockerfile verifies at build time that the native module loads in the runtime image.

## Installation

1. **Settings → Add-ons → Add-on Store → ⋮ → Repositories**, add this repository's URL.
2. Install **DT241M Controller**.
3. Configure `scan_ranges`, e.g.

   ```yaml
   scan_ranges:
     - 192.168.1.0/24
   ```

4. Start the add-on. Devices appear under **Settings → Devices & services → MQTT**.

The Mosquitto broker add-on and the MQTT integration must be installed; broker credentials are obtained from the Supervisor automatically.

Full user documentation is in [`dt241m-controller/DOCS.md`](dt241m-controller/DOCS.md).

## Development

Requirements: Node.js ≥ 22, pnpm 11, Docker (for image builds).

```bash
cd dt241m-controller
pnpm install
pnpm typecheck
pnpm test
pnpm build
```

Run locally against any MQTT broker:

```bash
echo '{"scan_ranges":["192.168.1.0/24"]}' > /tmp/options.json
MQTT_HOST=127.0.0.1 MQTT_PORT=1883 \
DT241M_OPTIONS_PATH=/tmp/options.json \
DT241M_DATABASE_PATH=/tmp/dt241m.sqlite \
pnpm dev
```

Build the add-on image:

```bash
cd dt241m-controller
docker buildx build --platform linux/amd64 --build-arg BUILD_VERSION=1.0.0 -t dt241m-controller:amd64 --load .
docker buildx build --platform linux/arm64 --build-arg BUILD_VERSION=1.0.0 -t dt241m-controller:aarch64 --load .
```

Database schema changes: edit `src/db/schema.ts`, run `pnpm drizzle-kit generate`, and commit the new file under `drizzle/`. Migrations run automatically at startup.

`better-sqlite3` is pinned to an exact version on purpose: its prebuilt binaries are tied to the Node ABI, and newer releases have not always published them. When bumping it, confirm a `linuxmusl` prebuild exists for the Node major shipped by the Alpine base image (the Docker build fails loudly if the module cannot load).

### Layout

```text
dt241m-controller/
├── config.yaml, Dockerfile, run.sh          Home Assistant add-on packaging
├── drizzle/                                 SQL migrations (generated)
├── fixtures/                                Captured DT241M responses (from the handoff)
└── src/
    ├── index.ts                             Bootstrap, signals
    ├── config.ts                            Add-on options + Supervisor MQTT env (Zod)
    ├── controller.ts                        Orchestration: discovery, polling, writes, MQTT publishing
    ├── registry.ts                          In-memory adapter registry keyed by MAC
    ├── discovery.ts, poller.ts, cidr.ts, concurrency.ts
    ├── db/                                  Drizzle schema, better-sqlite3 database, adapter store
    ├── dt241m/                              Protocol client, Zod schemas, TX/RX classification
    ├── mqtt/                                Connection, topics, HA Discovery payloads, inbound routing
    └── tests/                               Vitest suites + offline simulator
```

## Tests

`pnpm test` runs 128 Vitest tests entirely offline against a simulated device network built from the captured fixtures. No test contacts real hardware or any historical device address. The suites cover the protocol contract, fixture parsing, classification, CIDR/config validation, registry behaviour, discovery, polling, DHCP identity safety, command ordering, hardware quirks, MQTT Discovery/behaviour and SQLite persistence/restart.

## Limitations

- Transmitter channels are read-only; no transmitter configuration
- No source-name selector, presets, groups, scenes or operation history in the add-on
- No EDID, firmware, network, emergency-mode or naming (`set_assigned_name`) hardware features
- IPv4 only; scan ranges must be private and `/16` or smaller
- No periodic full subnet scan; new devices require startup, a disappearance or the Rescan button
- API readback confirms the device's reported channel, not that video is visible on a display
- The receiver's physical channel digits may not update after an HTTP channel change (observed hardware quirk)
- Only firmware `1.13471.133` has been observed; other versions need regression testing
- Validated on real hardware from a development machine over VPN, not yet from inside a Home Assistant Supervisor install
