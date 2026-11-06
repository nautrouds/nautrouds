[English](README.md) | 繁體中文

<div align="center">
	<img src="./docs/icon.webp" width="240" height="240" alt="Nautrouds logo" />
	<h1>Nautrouds</h1>

[![CI](https://github.com/nautrouds/nautrouds/actions/workflows/ci.yml/badge.svg)](https://github.com/nautrouds/nautrouds/actions/workflows/ci.yml)
[![Go Version](https://img.shields.io/badge/Go-1.25%2B-00ADD8?logo=go)](go.mod)
[![License: MIT](https://img.shields.io/badge/License-MIT-yellow.svg)](LICENSE)
[![GHCR](https://img.shields.io/badge/GHCR-nautrouds%2Fnautrouds-blue?logo=docker)](https://github.com/nautrouds/nautrouds/pkgs/container/nautrouds)
</div>

---

Nautrouds 是基於 Unix Domain Sockets (UDS) 的 reverse proxy，內建服務發現。

- **熱重載**：監看 `.ntu` / `Ntufile` 設定檔，變更自動生效。
- **服務發現**：backend 只要在 services 目錄放入 socket 即完成註冊。
- **UDS 代理**：透過 UDS 將請求轉發給 backend。
- **生命週期管理**：自動建立與清理 socket listener 及服務狀態。

## 開始使用

需要 Go 1.25.6 以上。

```bash
git clone https://github.com/nautrouds/nautrouds.git
cd nautrouds
go build -o bin/nautrouds ./cmd/core
go build -o bin/ntuc ./cmd/ntuc
```

用 `ntuc` 編譯 `Ntufile`，再啟動核心服務：

```bash
./bin/ntuc -i Ntufile -o nautrouds.ntu
./bin/nautrouds --config=nautrouds.ntu
```

## 設定

路由定義在 `Ntufile` 中：

```text
GET /api/v1/users user-service
    $SetHeader("X-Source", "Nautrouds")
    $BasicAuth("admin", "secret")

POST /upload/* storage-service
    $IPAllow("192.168.1.0/24")
```

- 語法、middleware 與虛擬服務：[語法指南](./docs/syntax.md)
- CLI flag 與環境變數：[工具指南](./docs/ntuc.md)

## Docker

```bash
docker compose -f examples/docker-compose.yml up --build
```

範例包含一個 `gateway` (socat)，將 TCP `8080` 橋接到 Nautrouds 的 UDS 入口：

```bash
curl http://localhost:8080/
curl http://localhost:8080/health
curl http://localhost:8080/debug/services
```

| 路徑 | 用途 |
| :--- | :--- |
| `/etc/nautrouds` | 設定檔（`.ntu`、`Ntufile`） |
| `/var/run/nautrouds/services` | backend socket（`<service>/<node>.sock`） |
| `/var/run/nautrouds/entrypoints` | Nautrouds 入口 socket |

## 負載平衡

同一個 service 的節點預設採 round-robin。

在 service 目錄放一個空的標記檔即可切換策略；同時存在多個時，優先序高者生效：

| 優先序 | 標記檔 |
| :--- | :--- |
| 1 | `p2c.strategy` |
| 2 | `least_in_flight.strategy` |
| 3（預設） | `round_robin.strategy` |

service 目錄內的任何異動都會觸發該 service 重新掃描；週期性全量掃描則補上漏接的事件。

### 節點權重

socket 命名為 `<name>@<weight>.sock`，權重為 `[1, 100]` 的整數。

- 沒有權重、格式錯誤或超出範圍都視為 1（格式錯誤會記錄警告）。
- round-robin 依權重比例平滑交錯排程。
- least-in-flight / p2c 以在途請求數除以權重比較。

## 安全性注意事項

Nautrouds 是純 UDS 的內部 proxy，不提供 TLS/mTLS。

它假設能連上其 socket 的行程都是可信的，且部分預設值刻意寬鬆。部署前請確認以下項目。

### 1. `ServicesDir` 權限

`ServicesDir` 以 `01777` 建立，讓任何 backend 都能建立 socket，不需與 core 共用 UID/GID。

可透過 `--services-dir-mode` / `NAUTROUDS_SERVICES_DIR_MODE` 調整；在 Docker image 中，這也會驅動 `docker/entrypoint.sh` 的 `chown`/`chmod`/ACL 設定。

- **風險**：sticky bit 只能防止刪除。任何使用者仍可新增 service 目錄，或在既有 service 下放入節點來攔截流量。
- **建議**：共用主機上讓 backend 以專屬群組執行，並收斂權限（例如 `0770`）。

### 2. `$IPAllow` 的 header 模式

`$IPAllow(headerKey, cidr)` 會信任 `headerKey` 收到的任何值。

- **風險**：若 Nautrouds 是第一個 hop，客戶端可自行設定該 header 繞過檢查。
- **建議**：只在上游會覆寫該 header 時使用 header 模式；否則改用單參數形式，讀取 `X-Forwarded-For` 最右側的 entry。

### 3. Metrics collector socket

metrics socket 預設 `chmod` 成 `0666`，可透過 `--metrics-socket-mode` / `NAUTROUDS_METRICS_SOCKET_MODE` 調整。

- **風險**：任何本地行程都能送出偽造的 metrics。
- **建議**：共用主機上限制為可信的行程，告警時把 metrics 視為不可信輸入。

### 4. 請求資料 interpolation

`{header.X}`、`{query.X}` 等樣板會套用在整條指令上，而不只是字串參數。

- **風險**：若 service 或指令名稱取自請求資料，客戶端就能決定執行什麼。
- **建議**：只對參數值做 interpolation，除非所有可能的值都已驗證。

### 5. Entrypoint socket

`--instance-id` 為 entrypoint socket 檔名加上命名空間，讓共用 `EntrypointDir` 的實例不會衝突。

存取控制完全取決於 `EntrypointDir` 的檔案系統權限。

### 6. 權限降級（Docker）

image 以 `root` 啟動以初始化環境，之後降權為非 root 的 `nautrouds` 使用者。

設定 `NAUTROUDS_UID` / `NAUTROUDS_GID` 可在啟動時重新對應該使用者的 ID（例如對齊 bind-mount 目錄）；未設定則沿用 Alpine 建置時分配的 ID。

## Backend 建議

- socket 權限設為 `0666`。Nautrouds 雖會嘗試設定 ACL，但部分環境下對 socket 不可靠。
- 以專屬的非 root 使用者執行 backend。
