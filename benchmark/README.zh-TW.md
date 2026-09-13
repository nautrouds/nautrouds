# UDS Proxy Benchmark：Nautrouds vs Caddy vs Nginx

### 比較 Nautrouds、Caddy、Nginx 作為純 UDS reverse proxy 的效能

#### **Built-in response**(`/builtin/health`）
* proxy 直接回應，不經過 backend。
#### **Reverse-proxy response**(`/proxy/*`)
* proxy 轉發至專屬的 backend，該 backend 本身也只能透過 Unix Domain Socket 存取。


### TCP+TLS edge 情境
Nautrouds 不支援 TLS ，因此實際部署情境會是由 edge proxy ( Caddy/Nginx ) 在 TCP port 上終止 TLS 後轉發：

#### **`<edge>` TLS → Nautrouds → backend** 
- Caddy/Nginx 在 `:8443` 終止 TLS，接著透過 UDS 交給 Nautrouds，再由 Nautrouds 透過 UDS 轉發給 backend。

#### **`<edge>` TLS → backend（略過 Nautrouds）**
- 同一個 edge proxy 在 `:8444` 終止 TLS，直接透過 UDS reverse-proxy 到自己的 backend，完全略過 Nautrouds。


這樣可以獨立出「在 TLS-terminating edge proxy 後面插入 Nautrouds 作為額外的 internal UDS hop」所增加的邊際成本，對比讓 Caddy/Nginx 自己處理全部流程的情況。

---

## Topology

所有通訊都走 UDS——不對外開放任何 TCP port。每個 proxy 都有自己獨立的 socket 子目錄與專屬的 backend instance。使用了兩個 named volume：

- `nautrouds-sockets`（掛載於 `/var/run/nautrouds`）— 只給 `nautrouds` 與 `backend-nautrouds` 容器使用。
- `bench-sockets`（掛載於 `/var/run/bench`）— 由 `caddy`、`backend-caddy`、`nginx`、`backend-nginx` 共用。

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

`client` 容器同時掛載這兩個 volume，以便存取所有 socket。

## Stages（分階段）

`docker-compose.yml` 把每個 server 端容器都掛在一個 Compose profile 底下,一個 stage 對應一個 profile，所以不帶 `--profile` 執行 `docker compose up` 什麼都不會啟動。同一時間永遠只有一組 proxy stack 在跑：

| profile | 容器 | 會跑的 benchmark |
|---|---|---|
| `nautrouds-solo` | `nautrouds` | `NautroudsBuiltin` |
| `nautrouds-proxy` | `nautrouds`、`backend-nautrouds` | `NautroudsProxy` |
| `caddy-solo` | `caddy` | `CaddyBuiltin` |
| `caddy-proxy` | `caddy`、`backend-caddy` | `CaddyProxy`、`CaddyTLSDirectProxy` |
| `nginx-solo` | `nginx` | `NginxBuiltin` |
| `nginx-proxy` | `nginx`、`backend-nginx` | `NginxProxy`、`NginxTLSDirectProxy` |
| `caddy-tls` | `caddy`、`nautrouds`、`backend-nautrouds` | `CaddyTLSNautroudsProxy` |
| `nginx-tls` | `nginx`、`nautrouds`、`backend-nautrouds` | `NginxTLSNautroudsProxy` |

`*-solo` 這幾個 stage 會完全獨立跑 built-in（不經 backend）的 benchmark——沒有其他 proxy、backend，也沒有任何不相關的容器被 pin 在同一批核心上——這是最乾淨的純 proxy overhead 訊號，也是對任何 noisy neighbor 最敏感的測項。

### CPU 隔離

同一個 stage 內，server 端容器（proxy 本身加上它自己的 backend）共用一組由 `SERVER_CPUSET` 環境變數控制的 `cpuset`。Orchestrator（見下方）會依序掃過三個分級：

| tier | cpuset（預設 6 核心 pool） |
|---|---|
| `1` | `0` |
| `half` | `0-2` |
| `all` | `0-5` |

`client` 容器被 pin 在單一顆專屬核心（`CLIENT_CPUSET`，預設是緊接在 server pool 後面那顆，例如核心 `6`），跟 server pool 完全不重疊——load generator 本身永遠不會是被搶佔的資源，整次執行唯一被掃描的變因就是 server 端拿到幾顆核心。所有容器都拿掉了 `deploy.resources.limits.cpus` 這層 quota；核心預算完全由 `cpuset` 決定，避免「核心數效應」跟「quota 效應」混在數字裡分不清。

因為 `cpuset` 把 client 鎖在單一核心，容器內回報的 `GOMAXPROCS` 會自動變成 `1`（Go runtime 是透過 `sched_getaffinity` 讀取 affinity mask 判斷的）。並發量如何在這個限制下仍然有意義，見下方 [Client](#client) 小節。

Reverse-proxy 情境使用 `examples/backend/main.go` 作為後端，三個 proxy 共用同一份不修改。

### TLS edge 情境

Caddy 與 Nginx 各自額外開放兩個 internal-network TCP port：

- `:8443` — 終止 TLS 後，透過 UDS reverse-proxy 到 Nautrouds 的 entry socket（為此 `nautrouds-sockets` 同時掛載進 `caddy` 與 `nginx`）。
- `:8444` — 終止 TLS 後，透過 UDS reverse-proxy 直接到該 proxy 自己的 backend，略過 Nautrouds。

兩個 port 都使用同一組 self-signed cert/key（位於 `benchmark/tls/{cert,key}.pem`，ECDSA P-256，僅供 benchmark 使用，不含機敏資訊），確保雙方的 TLS handshake 成本一致。兩者也都固定使用 HTTP/1.1（Caddy 的 `protocols h1`、Nginx 未設定 `http2 on;` 的預設值），讓比較只聚焦在 TLS/hop 的額外開銷，而不混入 h1 vs h2 的差異。Client 端使用 `InsecureSkipVerify`，因為這是 self-signed 的 benchmark 憑證。

Caddy 與 Nginx 都不會自行建立 UDS socket 的父目錄，所以它們的 `command:` 都包了一層 `mkdir -p <dir> && exec <proxy> ...` 先建目錄。Nginx 另外在偵測到上次執行殘留的 stale socket file 時會直接綁定失敗（`AddrInUse`），所以它的 command 在啟動前還會多做一次 `rm -f <socket>`；Caddy 則會自動偵測並取代 stale unix socket，不需要這個 workaround。

## 執行方式

整個矩陣（8 個 stage × 3 個核心分級 = 24 次執行）由 `benchmark/orchestrator` 底下一支小型 Go 程式驅動。`run-benchmark.sh` 只是包在外面的一層薄殼：

```sh
./run-benchmark.sh
```

等同於：

```sh
cd benchmark && go run ./orchestrator
```

對每一組 (stage, tier)，orchestrator 會設定 `SERVER_CPUSET`/`CLIENT_CPUSET`、執行 `docker compose --profile <stage> up -d --build`、輪詢 `docker compose ps --format json` 直到所有容器都回報 `healthy`、執行 `docker compose --profile <stage> run --rm client go test -bench=<該 stage 的 benchmark> ...`，接著**不論成功或失敗**都會用 `docker compose --profile <stage> down -v` 把這個 stage 清乾淨，才會進到下一組。容器不會跨 stage 或跨 tier 被重用，數字不會被前一次執行留下的殘留狀態拖著跑偏。

可用參數（皆為選填）：

| flag | 預設值 | 說明 |
|---|---|---|
| `-compose-file` | `docker-compose.yml` | compose 檔案路徑 |
| `-results-dir` | `results` | 最終報告輸出目錄 |
| `-server-cores` | `6` | server 端 cpuset pool 的核心數；tier 就是這個數字的 1 / 一半 / 全部 |
| `-client-core` | 同 server-cores | client 被 pin 在哪顆核心 |
| `-benchtime` | `5s` | 直接傳給 `go test -benchtime` |
| `-healthy-timeout` | `90s` | 等待一個 stage 的容器變 healthy 的最長時間 |
| `-stages` | （全部） | 逗號分隔的 stage profile 子集，例如 `-stages=nautrouds-solo,caddy-solo` |

執行過程只會產生兩個檔案：`results/<timestamp>.md` 與 `results/latest.md`。沒有中間的 per-stage log 檔——orchestrator 做的每件事都會同時即時印到 stdout。

若想手動只跑單一 stage（例如在除錯 Caddyfile 時）：

```sh
SERVER_CPUSET=0-5 CLIENT_CPUSET=6 docker compose --profile caddy-proxy up -d --build
docker compose --profile caddy-proxy run --rm client go test -bench=BenchmarkCaddyProxy -run=^$ ./...
docker compose --profile caddy-proxy down -v
```

## Client

`benchmark/client/bench_test.go` 是標準的 `go test -bench` 測試套件，裡面定義了十個 benchmark：

- UDS，透過自訂的 `http.Transport` 直接 dial 目標的 socket：`BenchmarkNautroudsBuiltin`、`BenchmarkNautroudsProxy`、`BenchmarkCaddyBuiltin`、`BenchmarkCaddyProxy`、`BenchmarkNginxBuiltin`、`BenchmarkNginxProxy`。
- TCP+TLS edge 情境，透過 TLS dial edge proxy 的 container:port：`BenchmarkCaddyTLSNautroudsProxy`、`BenchmarkCaddyTLSDirectProxy`、`BenchmarkNginxTLSNautroudsProxy`、`BenchmarkNginxTLSDirectProxy`。

Orchestrator 每次只會跑當前 stage 相關的子集，例如：

```sh
go test -bench=^BenchmarkCaddyProxy$ -benchtime=5s -run=^$ ./...
```

由於 `client` 容器的 `GOMAXPROCS` 被 `cpuset` 鎖成 `1`（見 [CPU 隔離](#cpu-隔離)），`b.RunParallel` 預設的並發數（`GOMAXPROCS × parallelism`）會直接崩到只剩一個 in-flight request。`bench_test.go` 因此明確呼叫 `b.SetParallelism`，數值來自 `CLIENT_PARALLELISM` 環境變數（預設 `64`），讓 client 仍能打出有意義的並發量——HTTP client 是 I/O bound，單一核心就足夠驅動大量並發 goroutine。

任何非 2xx 的回應或 dial failure 都會讓 `b.Fatalf` 直接中止該 benchmark。
