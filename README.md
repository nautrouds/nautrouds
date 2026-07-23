English | [繁體中文](README.zh-TW.md)

<div align="center">
	<img src="./docs/icon.webp" width="240" height="240" alt="Nautrouds logo" />
	<h1>Nautrouds</h1>
</div>

---

Nautrouds is a dynamic service management and proxy system designed for high-availability request routing and service discovery. It facilitates seamless traffic management through Unix Domain Sockets (UDS) and hot-reloading configurations.

## Core Features

- **Hot-Reloading Configurations**: Automatically tracks changes to `.ntu` or `Ntufile` configuration files.
- **Dynamic Service Discovery**: Real-time registry management for active services.
- **UDS Proxying**: Efficient request forwarding via Unix Domain Sockets.
- **Graceful Lifecycle Management**: Clean automated setup and teardown of socket listeners and service states.

## Getting Started

### Prerequisites

- Go 1.25.6 or higher.

### Installation

```bash
# Clone the repository
git clone https://github.com/your-repo/nautrouds.git
cd nautrouds

# Build the core binary
go build -o bin/nautrouds-core ./cmd/nautrouds-core
```

### Usage

Run the Nautrouds core service:

```bash
./bin/nautrouds-core --config=my-app.ntu
```

## Configuration

Nautrouds uses an `Ntufile` as a configuration file, which is compiled by the `ntuc` compiler into a binary format (`.ntu`) for hot-reloading by the core engine.

### Configuration Compilation (ntuc)

Use the `ntuc` tool to compile your `Ntufile` into a binary format readable by the Nautrouds core:

```bash
# Basic compilation command
./bin/ntuc -i Ntufile -o nautrouds.ntu
```

### Configuration Example (Ntufile)

```text
# Basic routing rules
GET /api/v1/users $user-service
    $SetHeader(X-Source, Nautrouds)
    $BasicAuth(admin, secret)

POST /upload/* storage-service
    $IPAllow(192.168.1.0/24)
```

For detailed syntax specifications, built-in middleware, and virtual service listings, please refer to the [Syntax Guide](./docs/syntax.md). 
For CLI usage and core configuration, see the [Tooling Guide](./docs/ntuc.md).

## Docker Support

Nautrouds can be deployed using Docker and Docker Compose. This is the recommended way to experience the dynamic UDS proxying in a controlled environment.

### Quick Start with Docker Compose

1. **Build and start the stack**:
   ```bash
   docker compose -f examples/docker-compose.yml up --build
   ```


2. **Test the proxy**:
   The example setup includes a `gateway` (socat) that bridges TCP port `8080` to the Nautrouds UDS entrypoint.
   ```bash
   # Test the backend service
   curl http://localhost:8080/

   # Test virtual services
   curl http://localhost:8080/health
   curl http://localhost:8080/debug/services
   ```

3. **Directory Structure for Docker**:
   - `/etc/nautrouds`: Configuration files (`.ntu`, `Ntufile`).
   - `/var/run/nautrouds/services`: Backend UDS sockets.
   - `/var/run/nautrouds/entrypoints`: Nautrouds entrypoint sockets.

## Service Permission

Nautrouds uses a strict permission model for Unix Domain Sockets (UDS) to ensure security and service isolation while maintaining high-performance communication.

### Directory Structure

| Directory | Purpose |
| :--- | :--- |
| `/var/run/nautrouds/services` | Where backend services place their `.sock` files. |
| `/var/run/nautrouds/entrypoints` | Where Nautrouds creates its entrypoint sockets. |

### Security Considerations

Nautrouds is a UDS-only internal proxy: it assumes every process able to reach its sockets already sits inside your trust boundary. It does not provide TLS/mTLS, and several built-ins are deliberately permissive so operators can shape the trust model to their environment. The points below are deployment decisions that directly affect security — make them consciously rather than leaving them at defaults.

#### 1. `ServicesDir` permissions

`ServicesDir` (default `/var/run/nautrouds/services`) is created with mode `01777` (world-writable, sticky bit) by default so any backend process can create its own `<service>/<node>.sock` without sharing a UID/GID with the core process. Configurable via `--services-dir-mode` / `NAUTROUDS_SERVICES_DIR_MODE` (in the Docker image, the same env var also drives `docker/entrypoint.sh`'s `chown`/`chmod`/ACL setup).

- **Risk**: the sticky bit only stops other users from *deleting* files they don't own — it does **not** stop them from creating a new service directory or planting a node under an existing one, which would let that process intercept live traffic for that service.
- **Guidance**: fine on a single-tenant host/container. On a shared/multi-tenant host, run backend services under a dedicated group and narrow this mode (e.g. `0770`) instead of relying on `01777`.

#### 2. `$IPAllow` header mode has no built-in trust boundary

`$IPAllow(headerKey, cidr)` trusts whatever value arrives in `headerKey`.

- **Risk**: if Nautrouds is the first hop terminating client connections, the client controls that header directly and can bypass the CIDR check.
- **Guidance**: only use the header form behind a trusted upstream guaranteed to overwrite (not merely forward) that header; otherwise use the single-argument, `RemoteAddr`-based form.

#### 3. Metrics collector socket

When enabled, the metrics socket (default `metrics.sock`) is `chmod`'d to `0666` by default so any local backend can push metrics without permission friction. Configurable via `--metrics-socket-mode` / `NAUTROUDS_METRICS_SOCKET_MODE`.

- **Risk**: combined with point 1, any local process able to reach the socket can submit forged metrics frames, polluting counters/gauges.
- **Guidance**: on shared hosts, narrow this mode to the group of processes you trust to report metrics, and treat metrics as untrusted input when alerting.

#### 4. Dynamic middleware / service-name interpolation

Route and middleware directives can be templated from request data (`{header.X}`, `{query.X}`, ...), and the substitution applies to the whole directive, not just string arguments.

- **Risk**: a client can effectively choose which service or built-in runs if a directive name/target is templated from unvalidated request data.
- **Guidance**: prefer interpolating only argument *values* (e.g. a comparison target), not directive names or service names, unless every value the tag can take has been validated.

#### 5. `X-Forwarded-For`

Backend requests are proxied with Go's `httputil.ReverseProxy`, which appends to (rather than replaces) any pre-existing `X-Forwarded-For` header.

- **Risk**: a backend that trusts the header verbatim can be fed a spoofed origin IP by the client.
- **Guidance**: backends should only trust the last hop's contribution (the one Nautrouds itself appended); prefer `RemoteAddr`-based `$IPAllow` at the edge for origin-IP enforcement.

#### 6. `-token` is not an auth mechanism

It only namespaces entrypoint socket filenames so multiple instances sharing an `EntrypointDir` don't collide. Access control for entrypoint sockets is entirely a function of filesystem permissions on `EntrypointDir`.

#### 7. Privilege dropping (Docker)

The Nautrouds Docker image starts as `root` to initialize the environment and then immediately drops privileges to a non-root `nautrouds` user for execution.

- **UID/GID**: the `nautrouds` user/group's UID/GID default to whatever Alpine assigns at build time; set `NAUTROUDS_UID` / `NAUTROUDS_GID` to remap them at container start (e.g. to match a bind-mounted host directory's ownership) — `docker/entrypoint.sh` recreates the user/group with the requested IDs before dropping privileges.

### Backend Implementation Advice

To ensure stable communication, backend services should:
-   **Recommended Permissions**: Set your socket permissions to `0666`. While Nautrouds attempts to manage permissions via ACLs, they can be unreliable for socket files in certain environments; `0666` remains the safest default.
-   **Run as Non-Root**: Ensure backend services run as a dedicated user (not `root`) within their own containers.

## License

This project is licensed under the terms of the LICENSE file.
