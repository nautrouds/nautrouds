# Nautrouds

[English](README.md) | 繁體中文

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

### 安全模型

1.  **權限降級 (Privilege Dropping)**：Nautrouds Docker 鏡像以 `root` 身分啟動以初始化環境權限，然後立即降權至非 root 的 `nautrouds` 使用者執行。
2.  **自動化環境管理**：Nautrouds 會自動配置目錄的隔離與存取控制，以確保服務間的通訊安全性。

### 後端實作建議

為了確保穩定的通訊，後端服務應該：
-   **建議權限**：將您的 Socket 權限設定為 `0666`。雖然系統會嘗試透過 ACL 自動管理權限，但由於 ACL 在部分環境下對 Socket 檔案可能失效，設定為 `0666` 是最保險的做法。
-   **以非 Root 執行**：確保後端服務在自己的容器中以專用使用者（非 `root`）身分執行。

## 授權

本專案依照 LICENSE 檔案中的條款授權。
