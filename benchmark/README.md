English | [繁體中文](README.zh-TW.md)

# UDS Proxy Benchmark: Nautrouds vs Caddy vs Nginx

Compares Nautrouds, Caddy, and Nginx as UDS reverse proxies.

- client, backend, and proxy each run on their own physical cores
- rows limited by the client or the backend are flagged
- proxy cost is reported as **CPU µs per request**, read from the proxy container's cgroup
- a `client → backend` baseline shows how much latency each extra hop adds

## Paths

| category | path |
|---|---|
| `direct` | client → backend (baseline) |
| `uds_builtin` | client → proxy, which answers `/builtin/health` itself |
| `uds_proxy` | client → proxy → backend |
| `tls_direct` | client → Caddy/Nginx (TLS `:8444`) → backend |
| `tls_via_nautrouds` | client → Caddy/Nginx (TLS `:8443`) → Nautrouds → backend |

Nautrouds doesn't terminate TLS, so in production it sits behind Caddy or Nginx.

Comparing `tls_via_nautrouds` with `tls_direct` shows what it costs to add Nautrouds behind the TLS edge.

## Workloads

| workload | shape | shows |
|---|---|---|
| `latency-c1` | 1 conn, closed-loop | unloaded per-hop latency |
| `throughput-c64` | 64 conns, closed-loop | max throughput |
| `throughput-c512` | 512 conns, closed-loop | high concurrency |
| `rate-<n>` | 64 conns, open-loop at fixed req/s | latency at equal load |
| `conn-per-request` | 64 conns, `Connection: close` | accept/dial and TLS handshake cost |
| `body-64k` / `body-1m` | large response body | copy and streaming cost |
| `upload-64k` | POST with a 64 KiB body | request body streaming |
| `slow-backend-10ms` | 256 conns, backend sleeps 10 ms | many in-flight requests |

`body-*`, `upload-64k` and `slow-backend-10ms` only run on paths with a backend.

For latency, compare `latency-c1` and `rate-<n>`.

Closed-loop latency at saturation is mostly queueing time.

For capacity, compare `throughput-*` together with `proxy CPU µs/req`.

## Fair configuration

- Access logs are off for all three proxies.
- Upstream keep-alive is set to 1024 idle connections for all three:
  - Nginx: `keepalive`
  - Caddy: `keepalive_idle_conns_per_host`
  - Nautrouds: `NAUTROUDS_MAX_IDLE_CONNS_PER_NODE`
- Nginx sets `keepalive_requests 1000000`, so it doesn't close connections every 1000 requests.
- Nginx keeps its default response buffering, with `proxy_max_temp_file_size 0` so large responses never spill to temp files.
- Nginx turns off `proxy_request_buffering`, so uploads stream like they do in the other two.
- Nginx `worker_processes` equals the proxy CPU count.
- Caddy and Nautrouds set `GOMAXPROCS` from the cpuset automatically.

## CPU allocation

The orchestrator reads the CPU topology and splits physical cores into three disjoint pools:

| pool | default | flag |
|---|---|---|
| client | 2 cores | `-client-cores` |
| backend | 1 core | `-backend-cores` |
| proxy | all remaining cores | `-proxy-cores` |

- Only the first hardware thread of each core is used by default, so no two pools share a core through SMT.
- The proxy pool is swept over `1`, `half` and `all` of its cores, shown as tiers `1c`, `2c`, ...
- No container has a CPU quota; only `cpuset` limits CPU, so CFS throttling doesn't skew the numbers.

## How a run works

Each proxy setup is a Compose profile, e.g. `baseline`, `nautrouds-proxy`, `caddy-tls`.

Only one profile is up at a time, and each is removed with `down -v` before the next one starts.

Once every container is healthy, each target is probed with real requests.

Measurement starts only after it returns 2xx with the expected body size.

For every run:

1. `loadgen` warms up, keeps its connections open and waits.
2. The orchestrator reads each container's cgroup CPU usage, then starts the measurement.
3. Memory is sampled halfway through; CPU is read again at the end.

Reps are interleaved, so slow drift spreads across all rows.

The report shows the median across reps.

## Reading the report

- `Δp50 vs direct`: latency added compared with the `Direct` baseline.
- `proxy CPU µs/req`: the main efficiency metric.
- `proxy-bound` / `backend-bound` / `client-bound`: that side used ≥ 85% of its cores.
  - A throughput row only shows the proxy's ceiling when it is `proxy-bound`.
- `rate-missed`: open-loop achieved < 95% of the target rate.
- `client-late`: the open-loop dispatcher's p99 send lag exceeds both 100 µs and 10% of the measured p99 latency, so latency can't be trusted.
- `body-mismatch`: response size differs from what was requested.
- `errors(n)`: I/O failures or non-2xx responses.

If any row has `errors`, `client-late` or `body-mismatch`, the whole run is marked FAIL.

## Running

```sh
./run-benchmark.sh
```

The full matrix with defaults takes a little over 2 hours.

To narrow it:

```sh
cd benchmark
go run ./orchestrator -stages baseline,nautrouds-proxy -workloads latency-c1,throughput-c64 -reps 1
```

| flag | default | meaning |
|---|---|---|
| `-proxy-cores` | `0` (all remaining) | physical cores in the proxy pool |
| `-backend-cores` | `1` | physical cores for the backend |
| `-client-cores` | `2` | physical cores for the load generator |
| `-smt` | `false` | also use SMT siblings |
| `-duration` | `5s` | measurement time per run |
| `-warmup` | `2s` | warmup per run |
| `-reps` | `3` | repetitions per row |
| `-rates` | `5000,20000` | open-loop req/s targets |
| `-stages` | all | comma-separated profiles |
| `-workloads` | all | comma-separated workloads |
| `-healthy-timeout` | `90s` | wait limit for containers to become healthy |
| `-compose-file` | `docker-compose.yml` | compose file |
| `-results-dir` | `results` | output directory |

Output:

- `results/<timestamp>.md`: report
- `results/<timestamp>.json`: raw measurements
- `results/latest.md`: copy of the latest report

