# CLI Tooling Guide

## Core Service (nautrouds-core)

The runtime engine: runs the proxy and service registry, and watches the config.

### Configuration

Every option can be set by flag or environment variable.

| Flag | Env Var | Default | Description |
| :--- | :--- | :--- | :--- |
| `--config` | `NAUTROUDS_CONFIG` | `nautrouds.ntu` | [Config file](#config) |
| `--ntuc` | `NAUTROUDS_NTUC` | `ntuc` | [Compiler path](#config) |
| `--default-welcome` | `NAUTROUDS_DEFAULT_WELCOME` | `true` | [Fallback when config is missing](#config) |
| `--services` | `NAUTROUDS_SERVICES_DIR` | `/var/run/nautrouds/services` | [Services directory](#directories) |
| `--services-dir-mode` | `NAUTROUDS_SERVICES_DIR_MODE` | `1777` | [Services directory mode](#directories) |
| `--entrypoint-dir` | `NAUTROUDS_ENTRYPOINT_DIR` | `/var/run/nautrouds/entrypoints` | [Entrypoint directory](#directories) |
| `--entrypoint-dir-mode` | `NAUTROUDS_ENTRYPOINT_DIR_MODE` | `0755` | [Entrypoint directory mode](#directories) |
| `--entrypoint-count` | `NAUTROUDS_ENTRYPOINT_COUNT` | `1` | [Entrypoint socket count](#directories) |
| `--instance-id` | `NAUTROUDS_INSTANCE_ID` | *(empty)* | [Entrypoint socket suffix](#directories) |
| `--metrics-socket` | `NAUTROUDS_METRICS_SOCKET` | `metrics.sock` | [Metrics socket](#metrics) |
| `--metrics-socket-mode` | `NAUTROUDS_METRICS_SOCKET_MODE` | `0666` | [Metrics socket mode](#metrics) |
| `--max-idle-conns-per-node` | `NAUTROUDS_MAX_IDLE_CONNS_PER_NODE` | `48` | [Idle connections per node](#backend-connections) |
| `--idle-conn-timeout` | `NAUTROUDS_IDLE_CONN_TIMEOUT` | `60s` | [Idle connection timeout](#backend-connections) |
| `--log-level` | `NAUTROUDS_LOG_LEVEL` | `info` | [Log level](#logging) |

#### Config

- `--config`: compiled `.ntu` or source `Ntufile`.
- `--ntuc`: `ntuc` executable used to compile a source `Ntufile`.
- `--default-welcome`: if `--config` doesn't exist at startup, start with a fallback route instead of exiting. Other load errors still fail fast. See [Startup Fallback](#startup-fallback---default-welcome).

#### Directories

- `--services`: directory watched for backend sockets.
- `--services-dir-mode`: mode (octal) of the services directory.
- `--entrypoint-dir`: directory for entrypoint sockets.
- `--entrypoint-dir-mode`: mode (octal) of the entrypoint directory.
- `--entrypoint-count`: number of entrypoint sockets.
- `--instance-id`: appended to entrypoint socket filenames so instances can share `--entrypoint-dir`. See [Security Considerations](../README.md#security-considerations).

#### Metrics

- `--metrics-socket`: metrics collector socket, relative to `--services`. `-` disables it.
- `--metrics-socket-mode`: mode (octal) of the metrics socket.

#### Backend Connections

- `--max-idle-conns-per-node`: max idle keep-alive connections per backend node; must be positive.
  - Raise it if sustained concurrency to one node exceeds it, otherwise extra connections are closed and redialed.
- `--idle-conn-timeout`: idle keep-alive timeout, as a positive Go duration (`90s`, `5m`).

#### Logging

- `--log-level`: `debug`, `info`, `warn`, `error`, `dpanic`, `panic`, `fatal`.

### Hot-Reloading

- An `Ntufile` is recompiled with `--ntuc` on every save.
- A `.ntu` file is reloaded directly.
- If `--config` was missing at startup, creating it later triggers a normal reload.

### Startup Fallback (`--default-welcome`)

When `--config` doesn't exist, Nautrouds tries, in order:

1. **Piped stdin**: reads a route table already compiled by `ntuc -o -`. This lets a config skip the disk entirely:

   ```bash
   curl -fsSL https://example.com/Ntufile | ntuc -i - -o - | ./bin/nautrouds
   ```

   If decoding fails, startup fails instead of silently falling back to step 2.

2. **Welcome page**: if stdin isn't piped, serves the built-in `$welcome` catch-all.

Neither is hot-reloaded.

---

## Compiler (ntuc)

Compiles an `Ntufile` into the binary format the core loads.

```bash
ntuc [flags]
```

| Flag | Default | Description |
| :--- | :--- | :--- |
| `-i` | `Ntufile` | Input path; `-` for stdin. |
| `-o` | `nautrouds.ntu` | Output path; `-` writes GOB to stdout (how the core invokes `ntuc` on hot-reload). |
| `-check` | `false` | Check syntax only. |
| `-print` | `false` | Print the parsed route tree. |

Exit code `0` on success, `1` on syntax or compilation error.
