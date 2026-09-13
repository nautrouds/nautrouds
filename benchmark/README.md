# UDS Proxy Benchmark: Nautrouds vs Caddy vs Nginx

### Compares Nautrouds, Caddy, and Nginx as UDS-only reverse proxies

#### **Built-in response** (`/builtin/health`)
* the proxy answers directly, no backend involved.
#### **Reverse-proxy response** (`/proxy/*`)
* the proxy forwards to a dedicated backend, itself only reachable over a Unix Domain Socket.


### TCP+TLS edge scenario
Nautrouds doesn't support TLS, so the realistic deployment is an edge proxy (Caddy/Nginx) doing TLS termination on a TCP port in front of it, then forwarding:

#### **`<edge>` TLS → Nautrouds → backend**
- Caddy/Nginx terminates TLS on `:8443`, then hands off to Nautrouds over UDS, which forwards to the backend over UDS.

#### **`<edge>` TLS → backend (no Nautrouds)**
- same edge proxy terminates TLS on `:8444` and reverse-proxies straight to its own backend over UDS, skipping Nautrouds entirely.


This isolates the marginal cost of inserting Nautrouds as an internal UDS hop behind a TLS-terminating edge proxy, vs. letting Caddy/Nginx do everything themselves.

---

## Topology

All communication is over UDS — no TCP ports are published. Each proxy has its own private socket subtree and its own dedicated backend instance. Two named volumes are used:

- `nautrouds-sockets` (mounted at `/var/run/nautrouds`) — used only by the `nautrouds` and `backend-nautrouds` containers.
- `bench-sockets` (mounted at `/var/run/bench`) — shared by `caddy`, `backend-caddy`, `nginx`, and `backend-nginx`.

```
/var/run/nautrouds/
  entrypoints/nautrouds-0.sock   <- Nautrouds entry socket
  services/backend-service/node-0.sock

/var/run/bench/
  caddy/entry.sock                     <- Caddy entry socket
  nginx/entry.sock                     <- Nginx entry socket
  backends/
    caddy/backend-service/node-0.sock
    nginx/backend-service/node-0.sock
```

The `client` container mounts both volumes to reach every socket.

## Stages

`docker-compose.yml` gates every server-side container behind a Compose profile — one per stage — so `docker compose up` with no `--profile` starts nothing. Only one proxy stack is ever up at a time:

| profile | containers | benchmarks run |
|---|---|---|
| `nautrouds-solo` | `nautrouds` | `NautroudsBuiltin` |
| `nautrouds-proxy` | `nautrouds`, `backend-nautrouds` | `NautroudsProxy` |
| `caddy-solo` | `caddy` | `CaddyBuiltin` |
| `caddy-proxy` | `caddy`, `backend-caddy` | `CaddyProxy`, `CaddyTLSDirectProxy` |
| `nginx-solo` | `nginx` | `NginxBuiltin` |
| `nginx-proxy` | `nginx`, `backend-nginx` | `NginxProxy`, `NginxTLSDirectProxy` |
| `caddy-tls` | `caddy`, `nautrouds`, `backend-nautrouds` | `CaddyTLSNautroudsProxy` |
| `nginx-tls` | `nginx`, `nautrouds`, `backend-nautrouds` | `NginxTLSNautroudsProxy` |

The `*-solo` stages run the built-in (no-backend) benchmarks completely alone — no other proxy, backend, or unrelated container is pinned to the same cores — since that's the cleanest read of pure proxy overhead and the most sensitive to any noisy neighbor.

### CPU isolation

Within a stage, the server-side containers (the proxy plus its own backend) share a `cpuset` driven by the `SERVER_CPUSET` env var. The orchestrator (see below) sweeps it across three tiers:

| tier | cpuset (default 6-core pool) |
|---|---|
| `1` | `0` |
| `half` | `0-2` |
| `all` | `0-5` |

The `client` container is pinned to a single dedicated core (`CLIENT_CPUSET`, default: right after the server pool, e.g. core `6`) that never overlaps the server pool — the load generator is never itself the resource under contention, so the only variable swept across a run is how many cores the server side gets. No container carries a `deploy.resources.limits.cpus` quota anymore; `cpuset` alone defines the core budget, so core-count effects and CPU-quota effects don't get conflated in the results.

Because `cpuset` pins the client to one core, its container-reported `GOMAXPROCS` is automatically `1` (Go's runtime reads the affinity mask via `sched_getaffinity`). See [Client](#client) below for how concurrency is still kept meaningful despite that.

Reverse-proxy scenarios use `examples/backend/main.go` as the backend, shared unmodified by all three proxies.

### TLS edge scenario

Caddy and Nginx each additionally expose two internal-network TCP ports:

- `:8443` — TLS terminated, then reverse-proxied over UDS to the Nautrouds entry socket (`nautrouds-sockets` is mounted into both `caddy` and `nginx` for this).
- `:8444` — TLS terminated, then reverse-proxied over UDS straight to that proxy's own backend, bypassing Nautrouds.

Both ports use the same self-signed cert/key at `benchmark/tls/{cert,key}.pem` (ECDSA P-256, benchmark-only, not sensitive) so the TLS handshake cost is identical on both sides. Both are pinned to HTTP/1.1 (Caddy's `protocols h1`, Nginx's default without `http2 on;`) so the comparison isolates the TLS/hop overhead rather than mixing in h1-vs-h2 differences. The client uses `InsecureSkipVerify` since this is a self-signed benchmark cert.

Caddy and Nginx don't create the parent directory for their UDS socket on their own, so their `command:` is wrapped with `mkdir -p <dir> && exec <proxy> ...` to create it first. Nginx additionally fails to bind if a stale socket file from a previous run is still present (`AddrInUse`), so its command also does `rm -f <socket>` before starting; Caddy transparently detects and replaces stale unix sockets itself, so it needs no such workaround.

## Running

The whole matrix (8 stages × 3 core tiers = 24 runs) is driven by a small Go program in `benchmark/orchestrator`. `run-benchmark.sh` is a thin wrapper around it:

```sh
./run-benchmark.sh
```

which is equivalent to:

```sh
cd benchmark && go run ./orchestrator
```

For each (stage, tier) pair the orchestrator sets `SERVER_CPUSET`/`CLIENT_CPUSET`, runs `docker compose --profile <stage> up -d --build`, polls `docker compose ps --format json` until every container reports `healthy`, runs `docker compose --profile <stage> run --rm client go test -bench=<that stage's benchmarks> ...`, then **always** tears the stage down with `docker compose --profile <stage> down -v` — even on failure — before moving on. No container is ever reused across stages or tiers, so results can't drift from state a previous run left behind.

Flags (all optional):

| flag | default | meaning |
|---|---|---|
| `-compose-file` | `docker-compose.yml` | path to the compose file |
| `-results-dir` | `results` | where the final report is written |
| `-server-cores` | `6` | size of the server-side cpuset pool; tiers become `1` / half / all of this |
| `-client-core` | server-cores | which core the client is pinned to |
| `-benchtime` | `5s` | passed straight to `go test -benchtime` |
| `-healthy-timeout` | `90s` | how long to wait for a stage's containers to become healthy |
| `-stages` | (all) | comma-separated subset of stage profiles, e.g. `-stages=nautrouds-solo,caddy-solo` |

Only two files are ever written: `results/<timestamp>.md` and `results/latest.md`. There are no intermediate per-stage log files — everything the orchestrator does is also streamed live to stdout while it runs.

To drive a single stage by hand (e.g. while debugging the Caddyfile):

```sh
SERVER_CPUSET=0-5 CLIENT_CPUSET=6 docker compose --profile caddy-proxy up -d --build
docker compose --profile caddy-proxy run --rm client go test -bench=BenchmarkCaddyProxy -run=^$ ./...
docker compose --profile caddy-proxy down -v
```

## Client

`benchmark/client/bench_test.go` is a standard `go test -bench` suite. It defines ten benchmarks:

- UDS, dialing the target's socket directly via a custom `http.Transport`: `BenchmarkNautroudsBuiltin`, `BenchmarkNautroudsProxy`, `BenchmarkCaddyBuiltin`, `BenchmarkCaddyProxy`, `BenchmarkNginxBuiltin`, `BenchmarkNginxProxy`.
- TCP+TLS edge scenario, dialing the edge proxy's container:port over TLS: `BenchmarkCaddyTLSNautroudsProxy`, `BenchmarkCaddyTLSDirectProxy`, `BenchmarkNginxTLSNautroudsProxy`, `BenchmarkNginxTLSDirectProxy`.

The orchestrator invokes only the subset relevant to the current stage, e.g.:

```sh
go test -bench=^BenchmarkCaddyProxy$ -benchtime=5s -run=^$ ./...
```

Since the `client` container's `GOMAXPROCS` is pinned to `1` by `cpuset` (see [CPU isolation](#cpu-isolation)), `b.RunParallel`'s default concurrency (`GOMAXPROCS × parallelism`) would collapse to a single in-flight request. `bench_test.go` explicitly calls `b.SetParallelism` using the `CLIENT_PARALLELISM` env var (default `64`) so the client still generates meaningful concurrent load — an HTTP client is I/O-bound, so a single core is enough to drive many concurrent goroutines.

Any non-2xx response or dial failure aborts the benchmark with `b.Fatalf`.
