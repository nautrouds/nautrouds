English | [繁體中文](README.zh-TW.md)

<div align="center">
	<img src="./docs/icon.webp" width="240" height="240" alt="Nautrouds logo" />
	<h1>Nautrouds</h1>

[![CI](https://github.com/nautrouds/nautrouds/actions/workflows/ci.yml/badge.svg)](https://github.com/nautrouds/nautrouds/actions/workflows/ci.yml)
[![Go Version](https://img.shields.io/badge/Go-1.25%2B-00ADD8?logo=go)](go.mod)
[![License: MIT](https://img.shields.io/badge/License-MIT-yellow.svg)](LICENSE)
[![GHCR](https://img.shields.io/badge/GHCR-nautrouds%2Fnautrouds-blue?logo=docker)](https://github.com/nautrouds/nautrouds/pkgs/container/nautrouds)
</div>

---

Nautrouds is a reverse proxy with service discovery over Unix Domain Sockets (UDS).

- **Hot-reloading**: watches `.ntu` / `Ntufile` configs and applies changes automatically.
- **Service discovery**: backends register by placing a socket in the services directory.
- **UDS proxying**: forwards requests to backends over UDS.
- **Lifecycle management**: sets up and tears down socket listeners and service state automatically.

## Getting Started

Requires Go 1.25.6+.

```bash
git clone https://github.com/nautrouds/nautrouds.git
cd nautrouds
go build -o bin/nautrouds ./cmd/core
go build -o bin/ntuc ./cmd/ntuc
```

Compile an `Ntufile` with `ntuc`, then run the core service:

```bash
./bin/ntuc -i Ntufile -o nautrouds.ntu
./bin/nautrouds --config=nautrouds.ntu
```

## Configuration

Routes are defined in an `Ntufile`:

```text
GET /api/v1/users user-service
    $SetHeader("X-Source", "Nautrouds")
    $BasicAuth("admin", "secret")

POST /upload/* storage-service
    $IPAllow("192.168.1.0/24")
```

- Syntax, middleware, and virtual services: [Syntax Guide](./docs/syntax.md)
- CLI flags and environment variables: [Tooling Guide](./docs/ntuc.md)

## Docker

```bash
docker compose -f examples/docker-compose.yml up --build
```

The example includes a `gateway` (socat) that bridges TCP `8080` to the Nautrouds UDS entrypoint:

```bash
curl http://localhost:8080/
curl http://localhost:8080/health
curl http://localhost:8080/debug/services
```

| Path | Purpose |
| :--- | :--- |
| `/etc/nautrouds` | Configuration files (`.ntu`, `Ntufile`) |
| `/var/run/nautrouds/services` | Backend sockets (`<service>/<node>.sock`) |
| `/var/run/nautrouds/entrypoints` | Nautrouds entrypoint sockets |

## Load Balancing

Nodes in a service are balanced round-robin by default.

To change the strategy, place an empty marker file in the service directory. If several are present, the highest priority wins:

| Priority | Marker file |
| :--- | :--- |
| 1 | `p2c.strategy` |
| 2 | `least_in_flight.strategy` |
| 3 (default) | `round_robin.strategy` |

Changes inside a service directory trigger a rescan of that service; a periodic full scan catches any missed events.

### Node Weight

Name the socket `<name>@<weight>.sock`, where weight is an integer in `[1, 100]`.

- A missing, invalid, or out-of-range weight is treated as 1 (invalid values log a warning).
- Round-robin interleaves nodes smoothly in proportion to weight.
- least-in-flight / p2c compare in-flight count divided by weight.

## Security Considerations

Nautrouds is a UDS-only internal proxy without TLS/mTLS.

It assumes every process that can reach its sockets is trusted, and several defaults are deliberately permissive. Review the following before deploying.

### 1. `ServicesDir` permissions

`ServicesDir` is created with mode `01777` so any backend can create its socket without sharing a UID/GID with the core.

Configure with `--services-dir-mode` / `NAUTROUDS_SERVICES_DIR_MODE`; in the Docker image this also drives the `chown`/`chmod`/ACL setup in `docker/entrypoint.sh`.

- **Risk**: the sticky bit only prevents deletion. Any user can still add a service directory or a node to an existing one and intercept its traffic.
- **Guidance**: on shared hosts, run backends under a dedicated group and narrow the mode (e.g. `0770`).

### 2. `$IPAllow` header mode

`$IPAllow(headerKey, cidr)` trusts whatever value arrives in `headerKey`.

- **Risk**: if Nautrouds is the first hop, clients can set the header and bypass the check.
- **Guidance**: use the header form only behind an upstream that overwrites the header. Otherwise use the single-argument form, which reads the rightmost `X-Forwarded-For` entry.

### 3. Metrics collector socket

The metrics socket is `chmod`'d to `0666` by default. Configure with `--metrics-socket-mode` / `NAUTROUDS_METRICS_SOCKET_MODE`.

- **Risk**: any local process can push forged metrics.
- **Guidance**: on shared hosts, restrict it to trusted processes and treat metrics as untrusted input when alerting.

### 4. Request-data interpolation

Templates such as `{header.X}` and `{query.X}` are substituted across the whole directive, not just string arguments.

- **Risk**: templating a service or directive name from request data lets clients choose what runs.
- **Guidance**: interpolate only argument values, unless every possible value has been validated.

### 5. Entrypoint sockets

`--instance-id` namespaces entrypoint socket filenames so instances sharing an `EntrypointDir` don't collide.

Access control relies entirely on the filesystem permissions of `EntrypointDir`.

### 6. Privilege dropping (Docker)

The image starts as `root` to initialize the environment, then drops to the non-root `nautrouds` user.

Set `NAUTROUDS_UID` / `NAUTROUDS_GID` to remap that user's IDs at startup (e.g. to match a bind-mounted directory). Otherwise the IDs Alpine assigned at build time are used.

## Backend Recommendations

- Set socket permissions to `0666`. Nautrouds also tries ACLs, but they are unreliable on sockets in some environments.
- Run backends as a dedicated non-root user.
