# Captured DT241M responses

All files are exact device responses, pretty-printed without changing values. Tests run against these offline; no test contacts hardware.

| File | Device | Provenance |
| --- | --- | --- |
| `tx-info-initial-channel-3.json` | TX `ET01_286CD291` | Handoff E1: `get_device_info_proav` |
| `tx-set-channel-4-success.json` | TX `ET01_286CD291` | Handoff E2: `set_channel_id` acknowledgement |
| `tx-info-after-set-channel-4.json` | TX `ET01_286CD291` | Handoff E2: readback after the write |
| `rx-info-initial-channel-2.json` | RX `ER02_286CD6D8` | Handoff E4: `get_device_info_proav` |
| `rx-set-channel-1-success.json` | RX `ER02_2855A8CA` | Live test 2026-09-24: `set_channel_id` acknowledgement captured with `curl -i` |

Observed HTTP envelope for every response on firmware `1.13471.133` (live test 2026-09-24):

```text
HTTP/1.1 200 OK
Cache-Control: no-store, no-cache, must-revalidate, post-check=0, pre-check=0
Access-Control-Allow-Origin: *
Content-type: text/html
Transfer-Encoding: chunked
Server: lighttpd/1.4.35
```

The `Content-type` is `text/html` even though the body is JSON; clients must parse the body as text rather than trusting the header.
