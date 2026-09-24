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

There is no web UI, no REST API and no custom Home Assistant integration. The add-on is a single static Go binary.

## Supported hardware

This add-on supports the DT241M family that was reverse-engineered, running firmware `1.13471.133`:

- Transmitter `ProAVTx ET01` (`am_8270_proavtx-eth_et01-pway-dt241`)
- Receiver `ProAVRx ER01` (`am_8270_proavrx-eth_er01-pway-dt241`)

It does **not** claim support for every PWAY HDMI-over-IP product. Devices whose `product_name`/`model` cannot be classified are shown as `unknown` and are never written to.

## Hardware validation

Validated on 24 September 2026 (both the 1.x Node.js build and the 2.0 Go build) against a live installation of 16 units (3 transmitters, 13 receivers), all on firmware `1.13471.133`, reached over a VPN:

- A `/24` scan discovered and correctly classified all 16 devices in ~61 s (2 s probe timeout, 8 concurrent)
- Polling every 15 s ran without availability flapping and without sending any writes
- A receiver channel change issued through the MQTT command topic completed the verify-MAC → `set_channel_id` → readback path in ~400 ms; the operator confirmed the video source switched, and the change was reverted the same way
- The receiver `set_channel_id` acknowledgement was captured and matches the transmitter shape: `{"jsonrpc":"2.0","id":1,"result":{"result":true}}`
- All responses arrive as `HTTP 200` with `Content-type: text/html` (lighttpd 1.4.35) despite JSON bodies

Not yet verified: the Supervisor services-API MQTT lookup inside a live Home Assistant install, DHCP address changes on real hardware, channel 0 semantics, and whether receiver front-panel digits update after an HTTP change.

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

The image is a statically linked Go binary (`CGO_ENABLED=0`) on plain `alpine:3.24`, about 20 MB in total. SQLite is provided by the pure-Go `modernc.org/sqlite`, so there are no native modules and no per-architecture prebuilt binaries to manage; cross-compiling for either architecture is a single `GOARCH` flag.

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

Requirements: Go ≥ 1.25, Docker (for image builds).

```bash
cd dt241m-controller
go vet ./...
go test -race ./...
CGO_ENABLED=0 go build -o dt241m-controller ./cmd/dt241m-controller
```

Run locally against any MQTT broker:

```bash
echo '{"scan_ranges":["192.168.1.0/24"]}' > /tmp/options.json
MQTT_HOST=127.0.0.1 MQTT_PORT=1883 \
DT241M_OPTIONS_PATH=/tmp/options.json \
DT241M_DATABASE_PATH=/tmp/dt241m.sqlite \
./dt241m-controller
```

Under the Supervisor, `MQTT_HOST` is unset and the binary fetches the broker details from `http://supervisor/services/mqtt` using `SUPERVISOR_TOKEN`; setting `MQTT_HOST` overrides that for local development.

Build the add-on image:

```bash
cd dt241m-controller
docker buildx build --platform linux/amd64 --build-arg BUILD_VERSION=2.0.0 -t dt241m-controller:amd64 --load .
docker buildx build --platform linux/arm64 --build-arg BUILD_VERSION=2.0.0 -t dt241m-controller:aarch64 --load .
```

Database schema changes: append a statement to the `migrations` slice in `internal/store/store.go` and bump `schemaVersion`. Migrations are tracked with `PRAGMA user_version` and run automatically at startup; the schema is unchanged from 1.x so existing databases are reused as-is.

### Layout

```text
dt241m-controller/
├── config.yaml, Dockerfile                  Home Assistant add-on packaging
├── fixtures/                                Captured DT241M responses (from the handoff)
├── cmd/dt241m-controller/main.go            Bootstrap, signals
└── internal/
    ├── dt241m/                              Protocol client, DeviceInfo parsing, TX/RX classification
    ├── controller/                          Orchestration: discovery, polling, per-MAC write queue, MQTT publishing
    ├── registry/                            In-memory adapter registry keyed by MAC
    ├── store/                               SQLite (modernc.org/sqlite) inventory with embedded migrations
    ├── mqtt/                                Connection interface, paho.golang impl, topics, HA Discovery payloads, router
    ├── config/, cidr/, mac/, discovery/     Options + Supervisor MQTT lookup, range expansion, identity, probing
    └── testutil/                            Offline device simulator (http.RoundTripper), fake MQTT, fixtures
```

## Tests

`go test -race ./...` runs 79 tests entirely offline against a simulated device network (an `http.RoundTripper` built from the captured fixtures). No test contacts real hardware or any historical device address. The suites cover the protocol contract, fixture parsing, classification, CIDR/config validation, the Supervisor MQTT lookup, registry behaviour, discovery, polling, DHCP identity safety, command ordering, hardware quirks, MQTT Discovery/behaviour and SQLite persistence/restart.

## Limitations

- Transmitter channels are read-only; no transmitter configuration
- No source-name selector, presets, groups, scenes or operation history in the add-on
- No EDID, firmware, network, emergency-mode or naming (`set_assigned_name`) hardware features
- IPv4 only; scan ranges must be private and `/16` or smaller
- No periodic full subnet scan; new devices require startup, a disappearance or the Rescan button
- API readback confirms the device's reported channel, not that video is visible on a display
- The receiver's physical channel digits may not update after an HTTP channel change (observed hardware quirk)
- Only firmware `1.13471.133` has been observed; other versions need regression testing
- Validated on real hardware from a development machine over VPN; the Supervisor services-API lookup has not yet been exercised inside a live Home Assistant install
