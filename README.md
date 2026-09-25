# DT241M Controller

Home Assistant add-on that exposes **DT241M** HDMI-over-IP transmitters and receivers as native MQTT devices. Pick a source per receiver from Home Assistant; scenes, automations and dashboards are Home Assistant's job. The add-on is only the hardware driver.

User documentation: [`dt241m-controller/DOCS.md`](dt241m-controller/DOCS.md).

## How it works

```text
Home Assistant ──MQTT──▶ Mosquitto ◀──MQTT── DT241M Controller ──HTTP──▶ DT241M units
                                              │
                                              ├─ scans configured ranges, identifies units by MAC
                                              ├─ keeps inventory in SQLite (/data)
                                              ├─ polls, publishes state via MQTT Discovery
                                              └─ writes: verify MAC → set channel → read back
```

- **Identity is the MAC address.** It keys the database, the MQTT topics and the Home Assistant device; IP is only where a unit was last reached and is followed automatically.
- **Writes are verified.** Before any command the target IP is re-read and its MAC checked; the reported channel is read back before Home Assistant is updated. Nothing is published optimistically.
- **Receivers get a Source select** (transmitters by name) and a raw Channel number. Transmitters get a configuration-category Channel number. Every unit gets a Name text entity and IP/Role diagnostics.
- **Nothing routes on its own.** Restarts of the add-on, the broker or Home Assistant republish state but never send channel commands.

Single static Go binary, ~20 MB image, ~6 MB RSS. No web UI, no REST API, no custom integration.

## Hardware

Reverse-engineered from firmware `1.13471.133`; not vendor documentation. Two RPCs are used, exactly as observed:

| Method | Purpose |
| --- | --- |
| `get_device_info_proav` | identity, metadata, reported channel |
| `set_channel_id` | `{"pswd":"","channel_id":N}` |

Supported: `ProAVTx ET01` transmitters and `ProAVRx ER01` receivers. Anything else classifies as `unknown`, is shown read-only and never written to. Captured responses and their provenance are in [`dt241m-controller/fixtures/`](dt241m-controller/fixtures/README.md).

Validated on a live 16-unit installation (3 TX, 13 RX): discovery, restart from the persisted inventory, receiver switching by channel and by source, back-to-back command ordering, graceful shutdown, and a production upgrade under the Supervisor. Not yet exercised on hardware: moving a transmitter's channel, DHCP address changes, channel 0, and whether receiver front-panel digits update after an HTTP change.

## MQTT contract

Prefix `dt241m/`. `<mac>` is the lower-case MAC without separators (`fc19286cd6d8`).

| Topic | Purpose |
| --- | --- |
| `dt241m/controller/availability` | controller online/offline; also the Last Will |
| `dt241m/controller/rescan/press` | Rescan button |
| `dt241m/controller/{known,online}_devices/state` | inventory counters |
| `dt241m/device/<mac>/availability` | per-unit availability |
| `dt241m/device/<mac>/channel/{state,set}` | reported channel / command (receivers and transmitters) |
| `dt241m/device/<mac>/source/{state,set}` | receiver source by transmitter name; `none` when unmatched |
| `dt241m/device/<mac>/name/{state,set}` | user-editable name |
| `dt241m/device/<mac>/{ip,role}/state` | diagnostics |

Home Assistant device identifier `dt241m:<mac>`; entity unique IDs `dt241m_<mac>_{channel,source,name,ip_address,role}`; discovery configs under `homeassistant/<component>/dt241m_<mac>/<object>/config`. State is retained, commands are not.

## Development

Requires Go ≥ 1.25, [golangci-lint](https://golangci-lint.run) v2, Docker for images.

```bash
cd dt241m-controller
go vet ./... && golangci-lint run ./... && go test -race ./...
CGO_ENABLED=0 go build -o dt241m-controller .
```

Run against any broker (`MQTT_HOST` set → env config; unset → Supervisor services API):

```bash
echo '{"scan_ranges":["192.168.1.0/24"]}' > /tmp/options.json
MQTT_HOST=127.0.0.1 DT241M_OPTIONS_PATH=/tmp/options.json DT241M_DATABASE_PATH=/tmp/dt241m.sqlite ./dt241m-controller
```

Build the add-on image: `docker buildx build --platform linux/arm64 --build-arg BUILD_VERSION=$(grep '^version' dt241m-controller/config.yaml | cut -d'"' -f2) -t dt241m-controller --load dt241m-controller`.

Conventions: `gofmt`, zero lint findings, godoc on every package and exported identifier, other comments only for non-obvious *why*. Schema changes append to `migrations` in `internal/store/store.go` and bump `schemaVersion`; 1.x databases are adopted in place.

```text
dt241m-controller/
├── main.go                 wiring and signals
├── config.yaml, Dockerfile add-on packaging
├── fixtures/               captured device responses
└── internal/
    ├── controller/         lifecycle · discovery.go (scan/poll) · write.go (verify→write→readback) · publish.go (→ MQTT)
    ├── dt241m/             protocol client, classification
    ├── mqtt/               connection, topics, entity table → discovery payloads, inbound router
    ├── registry/           adapter inventory keyed by MAC, derived source table
    ├── store/              SQLite persistence, versioned migrations
    ├── config/, mac/       options + Supervisor MQTT lookup; MAC normalisation
    └── testutil/           simulated LAN (http.RoundTripper), fake broker, memory store
```

## Tests

`go test -race ./...` — 100 tests, fully offline against a simulated LAN built from the captured fixtures. No test contacts hardware. A golden file pins every discovery payload (`go test ./internal/controller -run Golden -update` after an intentional change). Coverage includes the protocol contract, DHCP identity safety, command ordering, the front-panel quirk, MQTT reconnect/birth behaviour, persistence and the 1.x upgrade path.

## Limitations

- IPv4 only; scan ranges must be private and `/16` or smaller; no periodic full scan (startup, disappearance, or the Rescan button)
- No presets, groups, scenes or history in the add-on; no EDID, firmware, network or on-device naming features
- A matching readback means the device *reports* the channel, not that video is on the screen
- Receiver front-panel digits may not update after an HTTP change (observed quirk)
