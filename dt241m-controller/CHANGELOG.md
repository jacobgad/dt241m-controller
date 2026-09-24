# Changelog

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
