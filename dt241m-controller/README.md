# DT241M Controller add-on

Home Assistant add-on that bridges **DT241M** HDMI-over-IP transmitters and receivers to MQTT.

- Discovers units on configured private IP ranges and tracks them by MAC address
- Receivers: **Source** select (transmitters by name) and **Channel** number; transmitters: configuration **Channel**; all units: **Name**, IP and Role
- Every write is MAC-verified and read back before Home Assistant is updated
- Inventory and names persist in `/data`; nothing is ever routed automatically
- Single static Go binary, ~20 MB image; `aarch64` and `amd64`

Supports the reverse-engineered `ProAVTx ET01` / `ProAVRx ER01` family on firmware `1.13471.133`. See [DOCS.md](DOCS.md) for setup and behaviour and the [repository README](../README.md) for architecture and development.
