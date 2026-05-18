# Metrics Push Specification (Nautrouds & Tentacle)

To support distributed monitoring and centralized observability, `ntl-tentacle` instances (sidecars) push their internal metrics to the central `nautrouds` core. This document defines the protocol and data semantics for these updates.

## Transport & Encoding

Nautrouds utilizes a high-performance, low-overhead communication channel for metrics collection:

- **Transport**: Unix Domain Socket (UDS)
- **Format**: Raw Binary (Header + Protobuf Payload + Checksum)
- **Default Socket Path**: `/var/run/nautrouds/services/metrics.sock` (Adjustable via `--services-dir`)

---

## 1. Binary Frame Structure

Each metric update is sent as a sequence of frames. A frame consists of a fixed-size header, a variable-length payload, and a trailing checksum.

| Field | Size | Type | Description |
| :--- | :--- | :--- | :--- |
| **Magic Byte** | 1 Byte | `uint8` | Constant `0xBE` |
| **Version** | 1 Byte | `uint8` | Constant `0x01` |
| **Length** | 4 Bytes | `uint32` | Big-endian payload length (Max 1MB) |
| **Payload** | N Bytes | `Protobuf` | Serialized `MetricPayload` message |
| **Checksum** | 2 Bytes | `uint16` | CRC16 (X-25) of **Header + Payload** |

---

## 2. Metric Payload Model (Protobuf)

The metrics payload is strictly governed by a centralized Protobuf schema to ensure cross-language compatibility.

Please refer to the official repository for the latest `.proto` definitions, generated bindings, and schema documentation:

+> **[nautroudsUDS/tentacle-metrics](https://github.com/nautroudsUDS/tentacle-metrics)**

### Key Data Concepts
While the specific message structure is defined in the repository above, the logical model includes:
- **Tenant Identification**: `tentacle_id` and `service` name.
- **Timestamping**: Millisecond-precision generation time.
- **Metric Types**: Support for `COUNTER`, `GAUGE`, and `HISTOGRAM`.
- **Dimensionality**: Support for arbitrary key-value `labels`.
- **Histograms**: Support for both raw observations and pre-aggregated cumulative buckets.

---

## 3. Standard Metric Names

| Metric Name | Type | Description |
| :--- | :--- | :--- |
| `tentacle_active_connections` | Gauge | Current active TCP connections to the backend. |
| `tentacle_connection_attempts_total` | Counter | Total number of backend connection attempts. |
| `tentacle_connection_failures_total` | Counter | Total number of backend connection failures. |
| `tentacle_bytes_transmitted_total` | Counter | Total bytes transferred (bidirectional). |
| `tentacle_transport_latency_seconds` | Histogram | UDS-to-TCP bridge transmission latency. |

---

## 4. Data Reporting Semantics

To ensure accurate aggregation in the central Nautrouds core, tentacles must follow these reporting rules:

### Counter Handling (Delta Reporting)
Tentacles **MUST** report the **Delta** (the increment since the last successful push) in the `value` field.
- **Reasoning**: Nautrouds performs a simple `.Add(value)` operation. Using deltas prevents double-counting upon tentacle restarts.
- **Reset Policy**: Reset the internal delta counter to 0 **only after** a successful push to the UDS.

### Histogram Handling
Histograms can be reported in two ways:
1. **Single Sample**: Provide the observation value in the `value` field.
2. **Pre-aggregated**: Provide a list of cumulative `buckets`.

### Gauge Handling
Gauges are reported as **Absolute** point-in-time values in the `value` field.

### Reliability & Retries
If a push fails:
- **Do not reset** delta counters or histogram buckets.
- Accumulate the data and retry in the next cycle. This ensures zero data loss during transient connectivity issues with the Nautrouds core.
