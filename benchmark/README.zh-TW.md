[English](README.md) | 繁體中文

# UDS Proxy Benchmark：Nautrouds vs Caddy vs Nginx

比較 Nautrouds、Caddy、Nginx 作為 UDS reverse proxy 的表現。
- client、backend、proxy 各自使用獨立的實體核心
- 被 client 或 backend 卡住的結果會被標記
- proxy 成本以 **每個 request 的 CPU µs** 表示，數據來自 proxy container 的 cgroup
- 搭配 `client → backend` baseline，可以直接看出每多一個 hop 增加多少 latency

## 測試路徑

| category | 路徑 |
|---|---|
| `direct` | client → backend（baseline） |
| `uds_builtin` | client → proxy，由 proxy 直接回應 `/builtin/health` |
| `uds_proxy` | client → proxy → backend |
| `tls_direct` | client → Caddy/Nginx（TLS `:8444`）→ backend |
| `tls_via_nautrouds` | client → Caddy/Nginx（TLS `:8443`）→ Nautrouds → backend |

Nautrouds 不處理 TLS，實際部署時前面會放 Caddy 或 Nginx。

比較 `tls_via_nautrouds` 和 `tls_direct`，就能看出在 TLS edge 後面加上 Nautrouds 的額外成本。

## Workload

| workload | 形式 | 觀察重點 |
|---|---|---|
| `latency-c1` | 1 條連線，closed-loop | 無負載下每個 hop 的 latency |
| `throughput-c64` | 64 條連線，closed-loop | 最大 throughput |
| `throughput-c512` | 512 條連線，closed-loop | 高並發 |
| `rate-<n>` | 64 條連線，固定 req/s 的 open-loop | 相同負載下的 latency |
| `conn-per-request` | 64 條連線，`Connection: close` | accept/dial 與 TLS handshake 成本 |
| `body-64k` / `body-1m` | 大型 response body | 複製與 streaming 成本 |
| `upload-64k` | 帶 64 KiB body 的 POST | request body streaming |
| `slow-backend-10ms` | 256 條連線，backend 延遲 10 ms | 大量 in-flight request |

`body-*`、`upload-64k`、`slow-backend-10ms` 只會跑在有 backend 的路徑上。

比較 latency 時看 `latency-c1` 和 `rate-<n>`。closed-loop 飽和時的 latency 主要是排隊時間。

比較容量時，看 `throughput-*` 搭配 `proxy CPU µs/req`。

## 公平設定

- 三者都關閉 access log。
- 三者的 upstream keep-alive 都設為 1024 條 idle connection：
  - Nginx：`keepalive`
  - Caddy：`keepalive_idle_conns_per_host`
  - Nautrouds：`NAUTROUDS_MAX_IDLE_CONNS_PER_NODE`
- Nginx 設定 `keepalive_requests 1000000`，避免每 1000 個 request 就關閉連線。
- Nginx 保留預設的 response buffering，但設定 `proxy_max_temp_file_size 0`，大型 response 不會寫入 temp file。
- Nginx 關閉 `proxy_request_buffering`，upload 跟另外兩者一樣用 streaming 傳送。
- Nginx 的 `worker_processes` 設為 proxy 可用的 CPU 數。
- Caddy 和 Nautrouds 的 `GOMAXPROCS` 會自動依 cpuset 設定。

## CPU 分配

Orchestrator 讀取 CPU topology，把實體核心切成三個互不重疊的 pool：

| pool | 預設 | flag |
|---|---|---|
| client | 2 顆 | `-client-cores` |
| backend | 1 顆 | `-backend-cores` |
| proxy | 其餘全部 | `-proxy-cores` |

- 預設每顆實體核心只用第一個 hardware thread，不同 pool 不會透過 SMT 共用同一顆核心。
- proxy pool 會依序測試 `1`、`一半`、`全部` 的核心數，對應 tier `1c`、`2c`……
- 所有 container 都沒有 CPU quota，只用 `cpuset` 限制，避免 CFS throttling 干擾數據。

## 量測流程

每種 proxy 組合是一個 Compose profile，例如 `baseline`、`nautrouds-proxy`、`caddy-tls`。

同一時間只會啟動一個 profile，每個 profile 跑完都會先 `down -v`，才啟動下一個。

所有 container 都 healthy 之後，還會實際對每個 target 發送 request，確認回應 2xx 且 body 大小正確，才開始量測。

每個 run 的步驟：

1. `loadgen` 先 warmup，保持連線不中斷並等待開始訊號。
2. Orchestrator 讀取各 container 的 cgroup CPU 用量，然後開始量測。
3. 量測到一半時取樣 memory；結束時再讀一次 CPU。

各 rep 交錯執行，讓緩慢的效能飄移平均分散到每一列。報告呈現各 rep 的 median。

## 報告解讀

- `Δp50 vs direct`：相對 `Direct` baseline 增加的 latency。
- `proxy CPU µs/req`：最主要的效率指標。
- `proxy-bound` / `backend-bound` / `client-bound`：該方用掉 85% 以上的核心。
  - throughput 類的列只有標示 `proxy-bound` 時，數字才代表 proxy 的上限。
- `rate-missed`：open-loop 實際速率低於目標的 95%。
- `client-late`：open-loop dispatcher 送出延遲的 p99 同時超過 100 µs 與量到的 p99 latency 的 10%，latency 不可信。
- `body-mismatch`：response 大小與要求的不符。
- `errors(n)`：I/O 失敗，或收到非 2xx response。

只要有任何一列出現 `errors`、`client-late` 或 `body-mismatch`，整次執行就判定為 FAIL。

## 執行方式

```sh
./run-benchmark.sh
```

用預設值跑完整個矩陣大約要 2 小時多。

縮小範圍：

```sh
cd benchmark
go run ./orchestrator -stages baseline,nautrouds-proxy -workloads latency-c1,throughput-c64 -reps 1
```

| flag | 預設 | 說明 |
|---|---|---|
| `-proxy-cores` | `0`（其餘全部） | proxy pool 的實體核心數 |
| `-backend-cores` | `1` | backend 的實體核心數 |
| `-client-cores` | `2` | load generator 的實體核心數 |
| `-smt` | `false` | 是否一併使用 SMT sibling |
| `-duration` | `5s` | 每個 run 的量測時間 |
| `-warmup` | `2s` | 每個 run 的 warmup 時間 |
| `-reps` | `3` | 每一列的重複次數 |
| `-rates` | `5000,20000` | open-loop 的目標 req/s |
| `-stages` | 全部 | 以逗號分隔的 profile |
| `-workloads` | 全部 | 以逗號分隔的 workload |
| `-healthy-timeout` | `90s` | 等待 container 變成 healthy 的上限 |
| `-compose-file` | `docker-compose.yml` | compose 檔 |
| `-results-dir` | `results` | 輸出目錄 |

輸出檔案：

- `results/<timestamp>.md`：報告
- `results/<timestamp>.json`：原始量測資料
- `results/latest.md`：最新一次報告的副本

