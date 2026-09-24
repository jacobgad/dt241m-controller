# DT241M Controller

A headless bridge that makes PWAY DT241M HDMI-over-IP transmitters and receivers appear as native MQTT devices in Home Assistant. Home Assistant handles scenes, groups, automations, dashboards and scripts; this add-on only talks to the hardware.

## Installation

1. In Home Assistant open **Settings → Add-ons → Add-on Store**.
2. Open the menu (⋮) in the top-right corner and choose **Repositories**.
3. Add this repository URL and click **Add**, then close the dialog.
4. Find **DT241M Controller** in the store and click **Install**.
5. Open the **Configuration** tab, set `scan_ranges` (see below), and **Save**.
6. Start the add-on and watch the **Log** tab for `discovery_completed`.

To develop locally instead, copy the `dt241m-controller` folder into the `/addons` share of your Home Assistant installation and use **Check for updates** in the store; it will appear under **Local add-ons**.

## MQTT

The add-on requires the Home Assistant **Mosquitto broker** add-on (or another broker registered as the Supervisor MQTT service) and the **MQTT integration** in Home Assistant.

Broker address and credentials are read automatically from the Supervisor's services API. You do not enter them anywhere. If no MQTT service is available the add-on refuses to start and logs `supervisor did not return a usable MQTT service`.

If the broker is restarted, the add-on reconnects with backoff and republishes discovery, availability and the last known state. It never sends channel commands to the hardware as a result of an MQTT reconnect.

## Configuration

```yaml
scan_ranges:
  - 192.168.1.0/24
poll_interval_seconds: 15
probe_timeout_ms: 2000
discovery_concurrency: 8
log_level: info
```

| Option | Default | Description |
| --- | --- | --- |
| `scan_ranges` | *(required)* | One or more IPv4 CIDR ranges to probe for DT241M devices. Must be private (RFC 1918) ranges with a prefix of `/16` or longer. |
| `poll_interval_seconds` | `15` | How often known devices are polled for channel and availability changes. |
| `probe_timeout_ms` | `2000` | HTTP timeout for each device request. |
| `discovery_concurrency` | `8` | Maximum simultaneous probes during a scan. |
| `log_level` | `info` | `debug`, `info`, `warn` or `error`. |

### Scan ranges

Discovery sends a `get_device_info_proav` request to every host address in each configured range. A `/24` is 254 requests and completes in a few seconds; a `/16` is 65,534 requests and can take many minutes. Use the smallest range that covers the DT241M network.

The add-on container must be able to reach those addresses over plain TCP port 80. It uses ordinary HTTP only: no ping, ARP, raw sockets or privileged mode.

Full scans run at startup, when you press the **Rescan network** button, and when a known device stops responding at its last IP. There is no periodic full scan.

## Devices

Every DT241M unit is identified by its MAC address, which is reported by the device itself. The MAC is the only identity used for the database, the MQTT topics, the Home Assistant device identifier and entity unique IDs. IP addresses and names are never used as identity.

Each adapter appears in Home Assistant as a device under a **DT241M Controller** parent device:

| Role | Icon | Entities |
| --- | --- | --- |
| Receiver (`ProAVRx`) | monitor | **Source** (select), **Channel** (number), **Name** (text), **IP address** and **Role** (diagnostic) |
| Transmitter (`ProAVTx`) | broadcast | **Channel** (number, configuration), **Name** (text), **IP address** and **Role** (diagnostic) |
| Unknown | question mark | **Channel** (sensor, read-only), **Name**, **IP address**, **Role** |

The **Role** sensor reports `receiver`, `transmitter` or `unknown` and can be used in templates and dashboard filters (for example an `auto-entities` card listing every receiver).

Role classification uses the device-reported `product_name` and `model`. Anything that cannot be confidently classified is treated as `unknown` and never receives channel writes.

## Receiver routing

A receiver shows the transmitter channel it is tuned to. There are two ways to change it:

- **Source** — a select listing every known transmitter by name. Pick one and the receiver is tuned to that transmitter's current channel. This is the entity most people should use in dashboards and scenes.
- **Channel** — the raw channel number, for when you know the number or want to tune to a channel no transmitter is on yet.

Both drive the same command path and always agree, because both are derived from the channel the receiver reports.

### Source names

Options are the transmitters' names — the **Name** you set, or the hardware name if you have not set one. Rename a transmitter and every receiver's Source list updates. If two transmitters share a name the options are shown as `Name (ch 1)` and `Name (ch 2)`. Transmitters that are currently offline stay in the list, since a receiver may still be tuned to them.

If a receiver is on a channel that no transmitter is broadcasting on, the Source select has *no selection* — Home Assistant displays it as **Unknown** — and the Channel entity still shows the number. There is deliberately no selectable "none" option; to park a receiver, set its Channel to an unused number.

### What happens on a change

Setting either entity sends `set_channel_id` to that receiver. The workflow is:

1. Validate the channel (integer 0–255).
2. Query the receiver's last-known IP and confirm it still reports the expected MAC.
3. Send the channel command.
4. Read the channel back from the device and publish what it reports.

The entity is not optimistic: it only changes after the device confirms the new value. If the device acknowledges the write but reports a different channel, the add-on logs `channel_readback_mismatch`, publishes the reported value and does not retry. If the HTTP request times out, the add-on reads the device state instead of resending, so a delayed write can never be applied twice.

Commands for the same receiver are executed in order, one at a time. Commands for different receivers run concurrently.

## Transmitter channels

A transmitter's **Channel** is a configuration entity (it appears under the device's *Configuration* section rather than its controls). Changing it re-routes **every receiver currently watching that transmitter**: receivers keep their channel number, so they lose the picture until they are retuned or the transmitter is moved back. Treat it as infrastructure setup, not day-to-day operation.

The write uses the same verify → write → read back path as receivers. If the new channel is already used by another transmitter the add-on logs `transmitter_channel_collision` and proceeds anyway (refusing would make it impossible to swap two transmitters); receivers on that channel will show whichever transmitter sorts first by name until the collision is resolved.

## Names

Every adapter has two names:

- **Reported name** — the `dev_name` the hardware reports (for example `ER02_286CD6D8`). The add-on never changes it.
- **Name** — an optional name you set through the **Name** text entity. It is stored in the add-on's database and survives restarts and rediscovery.

The Home Assistant device is displayed as `name ?? reportedName`. Set **Name** to an empty string to clear it and fall back to the reported name. Names are trimmed and limited to 64 characters. The **Name** entity remains usable while the device is offline.

Renaming never writes to the DT241M hardware. You can also rename the device or its entities directly in Home Assistant if you prefer; the add-on does not interfere with that.

## Scenes

Scenes live entirely in Home Assistant. Create a scene that sets the **Source** (or **Channel**) entity of each receiver, for example:

```text
Scene: Presentation
  Projector Receiver  Source → Lectern PC
  Lobby Receiver      Source → Lectern PC
  Stage Monitor       Source → Stage Camera
```

Using Source keeps scenes readable and survives a transmitter being moved to a different channel; using Channel pins the number regardless of which transmitter is on it.

Activating the scene publishes one MQTT command per receiver. The add-on processes each independently and reports the per-receiver result; there is no atomic multi-device transaction on this hardware.

Groups, automations, scripts and dashboards work the same way: use the receiver **Channel** entities.

## Rescan

Press the **Rescan network** button on the controller device when:

- you have added a new DT241M unit,
- a device is shown as unavailable but you know it is powered and on the network,
- a device was moved to a different IP while it was offline.

Rescan runs a full scan of the configured ranges. Only one scan runs at a time; pressing the button during a scan has no additional effect.

## DHCP and IP changes

Devices are tracked by MAC. If a device moves to a new IP the add-on notices when polling fails at the old address, runs a scan, finds the same MAC at the new address and updates the stored IP. Nothing changes in Home Assistant except the **IP address** diagnostic sensor.

Before every channel write the add-on re-reads the target IP and compares the reported MAC. If a different device now answers at that IP the command is **not** sent; the add-on rediscovers the intended device first and only writes once it has been found and verified.

## Persistence

Known adapters are stored in an SQLite database under `/data`. After a restart the add-on republishes every known device to Home Assistant as *unavailable*, probes each last-known IP, then scans the configured ranges. Devices that are switched off remain visible in Home Assistant as unavailable and are never deleted just because a scan did not find them.

Only inventory is persisted: MAC, name, reported name, role, last IP, last reported channel, model/firmware and first/last seen timestamps. Routing intent is never stored and previous routes are never reapplied after a restart.

## Front-panel quirk

During the original reverse engineering, changing a receiver's channel over HTTP switched the video to the correct source but the physical channel digits on the unit did **not** update. A later live test through this add-on confirmed the video switch again; the panel digits were not re-checked on that occasion. The add-on treats the API-reported channel as the software state and ignores the front panel entirely. A stale panel number is a documented hardware quirk, not a routing failure.

Equally, an API readback that matches the requested channel is not proof that video is visible on the display; only a person looking at the screen can confirm that.

## Troubleshooting

**Device shows as unavailable**
The device did not answer `get_device_info_proav` at its last IP during the most recent poll. Check that it is powered and reachable on port 80 from the Home Assistant host, then press **Rescan network**.

**No devices are discovered**
Confirm `scan_ranges` covers the DT241M network and that Home Assistant can route to it. From a machine on the same network you can verify a unit responds with:

```bash
curl -F 'data={"jsonrpc":"2.0","method":"get_device_info_proav","params":{},"id":1}' http://<device-ip>/cgi-bin/proav.cgi
```

**`config_invalid` in the log**
A scan range is malformed, public or larger than `/16`. Only private ranges are accepted.

**Subnet unreachable**
The add-on runs on Home Assistant's internal Docker network and can only reach networks the host can route to. If the DT241M units are on a separate VLAN, ensure the host has a route and no firewall blocks TCP/80.

**MQTT unavailable**
The add-on exits at start with `supervisor did not return a usable MQTT service` if the Mosquitto add-on is not installed or not running. If the broker drops later, the log shows `mqtt_disconnected` followed by `mqtt_connect_error` entries until it reconnects; discovery and state are republished automatically once it does.

**Device moved to a new DHCP address**
This is handled automatically when the device disappears at its old IP. If the device was offline while its address changed, press **Rescan network**.

**Physical display shows a different channel**
See *Front-panel quirk* above. Check the **Channel** entity and the actual video output rather than the digits on the unit.

**A write was refused with `identity_mismatch`**
Another device now answers at the target's last IP. The add-on rediscovers the intended device before writing; if it cannot be found the operation fails with `device_not_located` and no command is sent anywhere.

**Source shows Unknown**
No transmitter is broadcasting on the receiver's current channel. Check the Channel entity and the transmitters' channels; the add-on cannot select a source that does not exist.

**Two receivers show the same transmitter for different reasons / collision warnings in the log**
Two transmitters are on the same channel. Move one of them via its Channel entity.
