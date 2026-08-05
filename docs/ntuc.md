# CLI Tooling Guide

This guide covers the command-line interface for the Nautrouds core service and its configuration compiler.

## Core Service (nautrouds-core)

The `nautrouds-core` is the runtime engine. It manages the proxy, service registry, and configuration watching.

### Configuration
`nautrouds-core` can be configured via command-line flags or environment variables.

| Flag | Env Var | Default | Description |
| :--- | :--- | :--- | :--- |
| `--config` | `NAUTROUDS_CONFIG` | `nautrouds.ntu` | Path to compiled `.ntu` or source `Ntufile`. |
| `--ntuc` | `NAUTROUDS_NTUC` | `ntuc` | Path to the `ntuc` compiler executable. |
| `--services` | `NAUTROUDS_SERVICES_DIR` | `/var/run/nautrouds/services` | Directory to watch for backend UDS sockets. |
| `--services-dir-mode` | `NAUTROUDS_SERVICES_DIR_MODE` | `1777` | Permission mode (octal) applied to the services directory. |
| `--entrypoint-dir` | `NAUTROUDS_ENTRYPOINT_DIR` | `/var/run/nautrouds/entrypoints` | Directory to create entrypoint UDS sockets. |
| `--entrypoint-dir-mode` | `NAUTROUDS_ENTRYPOINT_DIR_MODE` | `0755` | Permission mode (octal) applied to the entrypoint directory. |
| `--entrypoint-count` | `NAUTROUDS_ENTRYPOINT_COUNT` | `1` | Number of entrypoint sockets to create. |
| `--metrics-socket` | `NAUTROUDS_METRICS_SOCKET` | *(empty, resolves to `metrics.sock`)* | Metrics collector socket path, relative to `--services`. Leaving it empty uses `metrics.sock`; set to `-` to disable. |
| `--metrics-socket-mode` | `NAUTROUDS_METRICS_SOCKET_MODE` | `0666` | Permission mode (octal) applied to the metrics collector socket. |
| `--log-level` | `NAUTROUDS_LOG_LEVEL` | `info` | Log level (`debug`, `info`, `warn`, `error`, `dpanic`, `panic`, `fatal`). |
| `--token` | `NAUTROUDS_TOKEN` | *(empty)* | Namespaces entrypoint socket filenames so multiple instances sharing an `--entrypoint-dir` don't collide. Not an auth mechanism — see the [Security Considerations](../README.md#security-considerations) section. |

### Hot-Reloading
Nautrouds automatically tracks changes to your configuration. 
- If `--config` points to an `Ntufile` (source), it uses the specified `ntuc` binary to re-compile on every save.
- If it points to a `.ntu` file (binary), it simply reloads the state.

---

## Compiler (ntuc)

`ntuc` is the tool used to compile human-readable `Ntufile` configurations into the binary format required by the core engine.

### Usage
```bash
ntuc [flags]
```

### Flags
| Flag | Default | Description |
| :--- | :--- | :--- |
| `-i` | `Ntufile` | Input `Ntufile` path (use `-` for stdin). |
| `-o` | `nautrouds.ntu` | Output binary file path (use `-` to write the GOB-encoded output to stdout instead of a file — this is how `nautrouds-core` invokes `ntuc` internally when hot-reloading a source `Ntufile`). |
| `-check`| `false` | Verify syntax only, without generating output. |
| `-print`| `false` | Print the parsed route tree to stdout for inspection. |

### Exit Codes
- `0`: Success.
- `1`: Compilation or Syntax Error.
