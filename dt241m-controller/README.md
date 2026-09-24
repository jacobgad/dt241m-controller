# DT241M Controller add-on

Home Assistant add-on that bridges PWAY **DT241M** HDMI-over-IP transmitters and receivers to Home Assistant over MQTT.

- Discovers devices by probing configured private IPv4 ranges over HTTP
- Identifies every device by MAC address; IP changes are followed automatically
- Exposes each receiver's channel as a writable Home Assistant `number` entity
- Exposes each transmitter's channel as a read-only `sensor`
- Persists the device inventory and user-set names in SQLite under `/data`
- Publishes everything through Home Assistant MQTT Discovery; no custom integration or UI

See [DOCS.md](DOCS.md) for installation, configuration and troubleshooting, and the repository [README](../README.md) for architecture, development and limitations.

## Supported hardware

Only the DT241M family observed during reverse engineering:

| Role | `product_name` | `model` |
| --- | --- | --- |
| Transmitter | `ProAVTx ET01` | `am_8270_proavtx-eth_et01-pway-dt241` |
| Receiver | `ProAVRx ER01` | `am_8270_proavrx-eth_er01-pway-dt241` |

Firmware `1.13471.133`. Other PWAY products are not supported.

## Supported architectures

`aarch64`, `amd64`
