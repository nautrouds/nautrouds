[English](README.md) | 繁體中文

<div align="center">
	<img src="./docs/icon.webp" width="240" height="240" alt="Nautrouds logo" />
	<h1>Nautrouds</h1>
</div>

---

Nautrouds 是一個動態服務管理與代理系統，專為高可用性的請求路由與服務發現而設計。透過 Unix Domain Sockets (UDS) 與設定熱重載，實現流暢的流量管理。

## 核心功能

- **設定熱重載**：自動追蹤 `.ntu` 或 `Ntufile` 設定檔的變更。
- **動態服務發現**：即時的服務註冊表管理。
- **UDS 代理**：透過 Unix Domain Sockets 高效轉發請求。
- **優雅的生命週期管理**：自動化的 Socket 監聽器與服務狀態清理。

## 開始使用

### 先決條件

- Go 1.25.6 或更高版本。

### 安裝

```bash
# Clone 儲存庫
git clone https://github.com/your-repo/nautrouds.git
cd nautrouds

# 編譯核心執行檔
go build -o bin/nautrouds-core ./cmd/nautrouds-core
```

### 使用方式

執行 Nautrouds 核心服務：

```bash
./bin/nautrouds-core --config=my-app.ntu
```

## 設定

Nautrouds 使用 `Ntufile` 作為設定檔，經由 `ntuc` 編譯器轉換為 binary 格式 (`.ntu`) 以供核心引擎進行熱重載。

### 設定檔編譯 (ntuc)

使用 `ntuc` 工具將 `Ntufile` 編譯為 Nautrouds 核心可讀取的 binary 格式 (`.ntu`)：

```bash
# 基本編譯指令
./bin/ntuc -i Ntufile -o nautrouds.ntu
```

### 設定範例 (Ntufile)

```text
# 基礎路由規則
GET /api/v1/users $user-service
    $SetHeader(X-Source, Nautrouds)
    $BasicAuth(admin, secret)

POST /upload/* storage-service
    $IPAllow(192.168.1.0/24)
```

詳細的語法規格、內建中間件與虛擬服務清單，請參閱 [語法指南](./docs/syntax.md)。關於 CLI 使用與核心設定，請參閱 [工具指南](./docs/ntuc.md)。

## Docker 支援

Nautrouds 可以使用 Docker 與 Docker Compose 進行部署。這是推薦的體驗動態 UDS 代理與服務發現的環境。

### 使用 Docker Compose 快速啟動

1. **構建並啟動服務棧**:
   ```bash
   docker compose -f examples/docker-compose.yml up --build
   ```


2. **測試代理功能**:
   範例配置包含一個 `gateway` (socat)，它將 TCP 端口 `8080` 橋接到 Nautrouds 的 UDS 入口。
   ```bash
   # 測試後端服務
   curl http://localhost:8080/

   # 測試虛擬服務
   curl http://localhost:8080/health
   curl http://localhost:8080/debug/services
   ```

3. **Docker 內的目錄結構**:
   - `/etc/nautrouds`: 配置文件 (`.ntu`, `Ntufile`)。
   - `/var/run/nautrouds/services`: 後端 UDS socket 檔案。
   - `/var/run/nautrouds/entrypoints`: Nautrouds 入口 UDS socket 檔案。

## 服務權限

Nautrouds 對 Unix Domain Sockets (UDS) 採用嚴格的權限模型，在確保安全與服務隔離的同時，維持高性能通訊。

### 目錄結構

| 目錄 | 用途 |
| :--- | :--- |
| `/var/run/nautrouds/services` | 後端服務放置 `.sock` 檔案的地方。 |
| `/var/run/nautrouds/entrypoints` | Nautrouds 建立入口 Socket 的地方。 |

### 安全性注意事項

Nautrouds 是純 UDS（Unix Domain Socket）內部 proxy：它假設任何能連上其 socket 的行程，都已經在你的信任邊界之內。它不提供 TLS/mTLS，且部分 built-in 功能刻意設計得較為寬鬆，讓維運人員可以依照自己的環境調整信任模型。以下項目是會直接影響安全性的部署決策，請有意識地做出選擇，而不是沿用預設值。

#### 1. `ServicesDir` 權限

`ServicesDir`（預設 `/var/run/nautrouds/services`）預設以 `01777`（world-writable + sticky bit）建立，目的是讓任何 backend 行程都能建立自己的 `<service>/<node>.sock`，不需要與 core 行程共用 UID/GID。可透過 `--services-dir-mode` / `NAUTROUDS_SERVICES_DIR_MODE` 調整（在 Docker image 中，同一個 env var 也會同步驅動 `docker/entrypoint.sh` 的 `chown`/`chmod`/ACL 設定）。

- **風險**：sticky bit 只能防止其他使用者**刪除**不屬於自己的檔案，**無法防止**他們建立新的服務目錄，或在既有服務底下偷偷放入節點，藉此攔截該服務的真實流量。
- **建議**：單租戶主機/容器用預設值沒問題；共用主機/多租戶環境應讓 backend 服務以專屬群組執行，並將此權限收斂（例如 `0770`），而不是依賴 `01777`。

#### 2. `$IPAllow` 的 header 模式沒有內建信任邊界

`$IPAllow(headerKey, cidr)` 會信任 `headerKey` 收到的任何值。

- **風險**：如果 Nautrouds 是第一個終結客戶端連線的節點，客戶端可以直接控制該 header，藉此繞過 CIDR 檢查。
- **建議**：只有在確定有可信上游會覆寫（而非單純轉發）該 header 時，才使用 header 模式；否則請使用單參數、以 `RemoteAddr` 為基礎的形式。

#### 3. Metrics collector socket

啟用時，metrics socket（預設 `metrics.sock`）預設會被 `chmod` 成 `0666`，讓任何本地 backend 都能無阻礙地推送 metrics。可透過 `--metrics-socket-mode` / `NAUTROUDS_METRICS_SOCKET_MODE` 調整。

- **風險**：結合第 1 點，任何能連上該 socket 的本地行程都能送出偽造的 metrics frame，污染 counter/gauge。
- **建議**：在共用主機上應將此權限收斂到你真正信任會回報 metrics 的行程群組，並在告警時把 metrics 視為不可信輸入。

#### 4. 動態 middleware / service name interpolation

路由與 middleware 指令可以用請求資料（`{header.X}`、`{query.X}` 等）做樣板替換，且替換作用在整條指令上，而不只是字串參數內部。

- **風險**：若指令名稱/目標是從未經驗證的請求資料樣板化出來的，客戶端等於能自行決定要路由到哪個服務、或執行哪個 built-in。
- **建議**：除非已驗證過該 tag 可能取到的所有值，否則應只在參數**值**（例如比對目標）中使用 interpolation，而不是指令名稱或 service name。

#### 5. `X-Forwarded-For`

後端請求透過 Go 的 `httputil.ReverseProxy` 轉發，其預設行為是對既有的 `X-Forwarded-For` 採取附加而非取代。

- **風險**：若 backend 直接信任整個 header，客戶端可以偽造來源 IP。
- **建議**：Backend 應該只信任該 header 最後一段（Nautrouds 自己附加上去的部分）；若需要精確的來源 IP 控管，優先使用邊界層、以 `RemoteAddr` 為基礎的 `$IPAllow`。

#### 6. `-token` 不是驗證機制

它只是替 entrypoint socket 檔名加上命名空間，讓多個共用同一個 `EntrypointDir` 的實例不會互相衝突。entrypoint socket 的存取控制完全取決於 `EntrypointDir` 的檔案系統權限。

#### 7. 權限降級 (Privilege Dropping，Docker)

Nautrouds Docker 鏡像以 `root` 身分啟動以初始化環境權限，然後立即降權至非 root 的 `nautrouds` 使用者執行。

- **UID/GID**：`nautrouds` 使用者/群組的 UID/GID 預設由 Alpine 在建置時自動分配；可設定 `NAUTROUDS_UID` / `NAUTROUDS_GID` 在容器啟動時重新對應（例如對齊 bind-mount 主機目錄的擁有權）——`docker/entrypoint.sh` 會在降權前用指定的 ID 重建該使用者/群組。

### 後端實作建議

為了確保穩定的通訊，後端服務應該：
-   **建議權限**：將您的 Socket 權限設定為 `0666`。雖然系統會嘗試透過 ACL 自動管理權限，但由於 ACL 在部分環境下對 Socket 檔案可能失效，設定為 `0666` 是最保險的做法。
-   **以非 Root 執行**：確保後端服務在自己的容器中以專用使用者（非 `root`）身分執行。

## 授權

本專案依照 LICENSE 檔案中的條款授權。
