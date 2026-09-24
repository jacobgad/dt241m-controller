# Changelog

## 2.1.1

- Fix a race where two transmitters discovered at the same moment could leave receivers' **Source** option lists missing a transmitter until the next change
- Internal restructure by responsibility (discovery, write, publish) and table-driven discovery payloads; no user-visible change

## 2.1.0

- New **Source** select on every receiver: choose a transmitter by name instead of remembering its channel. Options are the transmitters' names (user name if set, otherwise the hardware name); duplicates are shown as `Name (ch N)`. When no transmitter uses the receiver's channel the select has no selection (Home Assistant shows *Unknown*); `none` is never offered as an option.
- Transmitter **Channel** is now writable (a configuration-category number entity). Changing it re-routes every receiver watching that transmitter; moving onto a channel another transmitter already uses is logged as `transmitter_channel_collision` but allowed so two transmitters can be swapped.
- Receiver Source options and states refresh automatically when transmitters are discovered, renamed or retuned.

## 2.0.0

Rewrite in Go. Behaviour, MQTT topics, Home Assistant entities, unique IDs and the SQLite schema are unchanged, so existing installations upgrade in place and keep their devices, names and inventory.

- Single static binary on a plain Alpine base: image size 20 MB (was 142 MB), idle memory ~6 MB (was ~70 MB)
- No native modules or Node ABI coupling; SQLite via the pure-Go `modernc.org/sqlite`
- MQTT via `eclipse/paho.golang` (MQTT 5) with automatic reconnection
- Broker credentials read directly from the Supervisor services API; no bashio or s6 in the image
- Databases created by 1.x are adopted in place (their `adapters` table is identical; only the schema-version marker is added)
- Validated against the same live 16-unit installation as 1.x (discovery, restart from SQLite, receiver channel switching)

## 1.0.1

- Receivers, transmitters, unknown devices and the controller now use distinct icons (monitor, broadcast, question mark, video switch)
- New read-only **Role** diagnostic sensor on every adapter (`receiver` / `transmitter` / `unknown`) for dashboard filters and templates

## 1.0.0

Initial release. Discovery, polling and receiver channel switching validated against a live 16-unit DT241M installation on firmware `1.13471.133`.

- Discovery of DT241M transmitters and receivers via `get_device_info_proav` across configured private IPv4 CIDR ranges
- MAC-based device identity with automatic DHCP/IP change handling and identity verification before every write
- Receiver **Channel** number entity (writable, readback-confirmed) and transmitter **Channel** sensor (read-only)
- Per-adapter **Name** text entity stored in SQLite; hardware-reported name preserved separately
- Per-adapter **IP address** diagnostic sensor and availability
- Controller device with **Rescan network** button, **Known devices** and **Online devices** sensors
- SQLite (better-sqlite3) + Drizzle ORM persistence of adapter inventory under `/data`, with automatic migrations
- Per-receiver command serialisation; ambiguous write timeouts resolved by readback rather than retry
- Republishes discovery and state on MQTT reconnect and on the Home Assistant `online` birth message
- Graceful SIGTERM shutdown with retained controller availability and MQTT Last Will
