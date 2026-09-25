# DT241M Controller

Makes DT241M HDMI-over-IP transmitters and receivers appear as MQTT devices in Home Assistant. You pick sources in Home Assistant; the add-on talks to the hardware.

## Installation

1. **Settings → Add-ons → Add-on Store → ⋮ → Repositories**, add this repository's URL.
2. Install **DT241M Controller**.
3. On the **Configuration** tab set `scan_ranges`, then **Start**.
4. Watch the **Log** for `discovery_completed`. Devices appear under **Settings → Devices & services → MQTT**.

Requires the **Mosquitto broker** add-on and the **MQTT integration**. Broker credentials are read from the Supervisor; there is nothing to enter.

## Configuration

```yaml
scan_ranges:
  - 192.168.1.0/24
poll_interval_seconds: 15
probe_timeout_ms: 2000
discovery_concurrency: 8
log_level: info
```

| Option | Default | Meaning |
| --- | --- | --- |
| `scan_ranges` | required | IPv4 CIDRs to probe. Private (RFC 1918) ranges, `/16` or smaller. |
| `poll_interval_seconds` | `15` | How often known devices are re-read. |
| `probe_timeout_ms` | `2000` | HTTP timeout per device request. |
| `discovery_concurrency` | `8` | Parallel probes during a scan. |
| `log_level` | `info` | `debug` / `info` / `warn` / `error` |

A scan probes every address in the range over plain HTTP (port 80): a `/24` takes about a minute. Scans run at startup, on the **Rescan network** button, and when a known device stops answering. There is no periodic full scan.

## Devices and entities

Each unit is identified by its MAC address — that is what the database, MQTT topics and Home Assistant device all key on. IP addresses change freely; names are yours to edit.

| Role | Entities |
| --- | --- |
| Receiver | **Source** (select) · **Channel** (number) · **Name** (text) · IP address, Role (diagnostic) |
| Transmitter | **Channel** (number, under *Configuration*) · **Name** (text) · IP address, Role |
| Unknown | Channel (read-only) · Name · IP address, Role — never written to |

All units sit under a **DT241M Controller** device with a **Rescan network** button and **Known** / **Online devices** counters.

## Routing a receiver

- **Source** lists every transmitter by name. Choose one and the receiver is tuned to that transmitter's current channel. Use this in dashboards and scenes.
- **Channel** is the raw number, for tuning to a channel no transmitter is on.

Both drive the same path: confirm the receiver's IP still belongs to its MAC → send the command → read the channel back → update Home Assistant. Entities only change once the device confirms; a timeout is resolved by reading back, never by resending. Commands to one receiver run in order; different receivers run in parallel.

If no transmitter uses the receiver's channel, Source shows **Unknown** (Home Assistant's "no selection"); there is intentionally no selectable *none*. Duplicate transmitter names appear as `Name (ch 1)` and `Name (ch 2)`. Offline transmitters stay listed, since receivers may still be tuned to them.

## Transmitter channels

A transmitter's **Channel** is configuration, not operation: changing it moves the transmitter, so every receiver watching it loses its picture until retuned. If the new channel is already used by another transmitter the add-on logs `transmitter_channel_collision` and proceeds (so two transmitters can be swapped); resolve it by moving one.

## Names

- **Reported name** — the hardware's own (`ER02_286CD6D8`). Never changed by the add-on.
- **Name** — set via the **Name** entity, stored in the add-on's database, shown as the device name. Clear it (empty string) to fall back to the reported name. Max 64 characters. Works while the device is offline. Never written to the hardware.

Renaming devices or entities directly in Home Assistant also works and is independent of this.

## Scenes and automations

Build them in Home Assistant from the receivers' **Source** (or **Channel**) entities:

```text
Scene: Presentation
  Projector    Source → Lectern PC
  Lobby        Source → Lectern PC
  Stage monitor  Source → Stage Camera
```

Source keeps scenes readable and survives a transmitter being re-channelled; Channel pins the number. Each receiver is commanded independently; there is no atomic multi-device switch on this hardware.

## Persistence and restarts

The inventory (MAC, names, role, last IP and channel, model, firmware, timestamps) lives in `/data/dt241m.sqlite`, which Home Assistant keeps across updates and restarts. On start the add-on publishes every known device as unavailable, re-probes each last-known IP, then runs a full scan. Devices that are off stay visible as unavailable; nothing is deleted for missing a scan. Routes are never stored or reapplied.

The "Create backup before updating" option in Home Assistant's update dialog includes this database.

## DHCP

If a device moves IP, the next poll fails at the old address (it briefly shows unavailable), a scan finds the MAC at the new one, and it comes back with the IP diagnostic updated. Its identity, entities and name are unchanged. Before any write the target IP is re-checked; if a different MAC answers, nothing is sent until the intended device is found.

## Front-panel quirk

Switching a receiver over HTTP changes the video but may leave the unit's physical channel digits unchanged. The add-on ignores the panel entirely; the reported channel is the truth. Conversely, a matching readback does not prove a picture is on screen.

## Troubleshooting

| Symptom | What to check |
| --- | --- |
| Device unavailable | It didn't answer at its last IP. Check power and port-80 reachability from the HA host, then **Rescan network**. |
| Nothing discovered | `scan_ranges` covers the DT241M subnet and the HA host can route to it. From that network: `curl -F 'data={"jsonrpc":"2.0","method":"get_device_info_proav","params":{},"id":1}' http://<ip>/cgi-bin/proav.cgi` |
| `config_invalid` in log | A scan range is malformed, public, or larger than `/16`. |
| `supervisor did not return a usable MQTT service` | Install/start the Mosquitto broker add-on. |
| `mqtt_disconnected` / `mqtt_connect_error` | Broker is down; the add-on reconnects and republishes on its own. |
| Source shows Unknown | No transmitter is on that receiver's channel. |
| `identity_mismatch` in log | A different device answered at an adapter's last IP (seen on polls as well as before writes). The adapter is rediscovered; a pending write is withheld, and `device_not_located` means it wasn't found — nothing was sent. |
| `transmitter_channel_collision` | Two transmitters share a channel; move one. |
| Panel digits differ from Channel | Expected; see *Front-panel quirk*. |
