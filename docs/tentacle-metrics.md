# Metrics Push Specification

`ntl-tentacle` sidecars push their metrics to the `nautrouds` core over UDS.

- **Socket**: `/var/run/nautrouds/services/metrics.sock` by default.
  - The directory is `--services`; the filename is `--metrics-socket` (`-` disables the collector). See the [Tooling Guide](./ntuc.md).
- **Format**: binary frames of header + Protobuf payload + checksum.

## Frame Structure

| Field | Size | Type | Description |
| :--- | :--- | :--- | :--- |
| **Magic** | 1 B | `uint8` | `0xBE` |
| **Version** | 1 B | `uint8` | `0x01` |
| **Length** | 4 B | `uint32` | Big-endian payload length, max 1 MB |
| **Payload** | N B | Protobuf | `MetricPayload` message |
| **Checksum** | 2 B | `uint16` | CRC16 (X-25) of header + payload |

## Payload

The `.proto` schema and generated bindings live in [nautrouds/tentacle-metrics](https://github.com/nautrouds/tentacle-metrics). Each payload carries:

- `tentacle_id` and `service` name
- Generation time, in milliseconds
- Metric type: `COUNTER`, `GAUGE`, or `HISTOGRAM`
- Arbitrary key-value `labels`

## Standard Metrics

| Name | Type | Description |
| :--- | :--- | :--- |
| `tentacle_active_connections` | Gauge | Active TCP connections to the backend. |
| `tentacle_connection_attempts_total` | Counter | Backend connection attempts. |
| `tentacle_connection_failures_total` | Counter | Backend connection failures. |
| `tentacle_bytes_transmitted_total` | Counter | Bytes transferred, both directions. |
| `tentacle_transport_latency_seconds` | Histogram | UDS-to-TCP bridge latency. |

## Reporting Rules

- **Counter**: report the **delta** since the last successful push in `value`.
  - The core simply calls `.Add(value)`, so deltas avoid double-counting after a tentacle restart.
- **Gauge**: report the **absolute** current value in `value`.
- **Histogram**: either a single observation in `value`, or pre-aggregated cumulative `buckets`.

If a push fails, keep the deltas and buckets and retry them in the next cycle. Reset only after a successful push.
