# Nautrouds

English | [中文](README.zh-TW.md)

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

### Security Model

1.  **Privilege Dropping**: The Nautrouds Docker image starts as `root` to initialize the environment and then immediately drops privileges to a non-root `nautrouds` user for execution.
2.  **Automated Environment Management**: Nautrouds automatically configures directory isolation and access controls to ensure secure communication between services.

### Backend Implementation Advice

To ensure stable communication, backend services should:
-   **Recommended Permissions**: Set your socket permissions to `0666`. While Nautrouds attempts to manage permissions via ACLs, they can be unreliable for socket files in certain environments; `0666` remains the safest default.
-   **Run as Non-Root**: Ensure backend services run as a dedicated user (not `root`) within their own containers.

## License

This project is licensed under the terms of the LICENSE file.
