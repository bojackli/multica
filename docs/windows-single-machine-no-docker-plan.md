# Windows 单机无容器化部署方案

## 目标

在一台 **Windows 电脑**（无 Docker、无虚拟机、无 WSL、完全离线）上，单用户使用 Multica，做到**双击即用**：

- 便携 PostgreSQL 17 + Go 后端 + Electron 桌面客户端，全部运行在本机
- 保留 **agent 能力**（Claude Code / Codex 等多后端），通过本机 `multica` daemon 执行
- 数据落在本机目录，关机不丢
- 全程不依赖 Docker、VM、VPS、Node.js、Next.js

## 边界与决策记录

| 决策点 | 结论 |
|--------|------|
| 使用规模 | 纯个人单机 |
| 客户端形态 | 官方 Electron 桌面客户端（`apps/desktop`），**不使用**浏览器 Web 版（`apps/web`） |
| Agent | **保留**，支持多后端（Claude Code、Codex 等） |
| 网络 | 完全离线，所有构件需从联网机器搬运 |
| 数据库 | 便携版 PostgreSQL 17（官方 zip 解压即用） |
| 交付形态 | 双击即用的目录（`multica-win/`） |

## 架构

```
┌─────────────────────────── Windows 本机 ───────────────────────────┐
│                                                                     │
│  ┌──────────────┐   ┌──────────────────┐   ┌────────────────────┐  │
│  │ Multica.exe  │──▶│ server.exe       │──▶│ 便携 PostgreSQL 17  │  │
│  │ Electron 客户端│   │ Go 后端 (8080/ws) │   │ (本机, 端口5433)    │  │
│  └──────────────┘   └───────┬──────────┘   └────────────────────┘  │
│                             │ 领任务/回报进度                        │
│                             ▼                                       │
│                    ┌──────────────────┐                             │
│                    │ multica daemon    │                            │
│                    │ (multica.exe)     │                            │
│                    │ 调用本机 AI CLI    │                            │
│                    │  Claude Code /    │                            │
│                    │  Codex / OpenCode │                            │
│                    └──────────────────┘                             │
└─────────────────────────────────────────────────────────────────────┘
```

**职责分工**
- `server.exe`：API + WebSocket 调度中枢，数据存 PostgreSQL
- `multica daemon`：连接 server，领任务并在本机调用 Claude Code 等执行
- `Multica.exe`：Electron 客户端，人机交互界面，连 `localhost:8080`
- 便携 PG：所有数据的落盘位置

## 目录结构（交付物）

```
multica-win/
├── StartMultica.exe        ← Go 启动器（双击入口）
├── StopMultica.exe         ← Go 停止器
├── data/                   ← 运行时数据（PG 数据目录、上传、日志）
│   ├── pgdata/
│   └── uploads/
├── postgres/               ← 便携版 PostgreSQL 17 (Windows zip)
│   ├── bin/
│   └── share/
├── server.exe              ← Go 后端 (windows/amd64)
├── migrate.exe             ← 迁移工具
├── multica.exe             ← CLI + daemon（agent 调度）
└── client/                 ← Electron 客户端安装产物
    └── Multica.exe 或 NSIS 安装包
```

## 构件来源与离线准备（在联网机器完成）

| 构件 | 来源 | 说明 |
|------|------|------|
| 便携 PG 17 | PostgreSQL 官方 `postgresql-17.x-windows-x64-binaries.zip` | 免安装，解压即用，已确认迁移仅依赖 PG 自带扩展（pgcrypto/pg_trgm） |
| server.exe / migrate.exe / multica.exe | 从源码交叉编译 | `GOOS=windows GOARCH=amd64 CGO_ENABLED=0 go build`，已在本机验证编译通过 |
| Electron 客户端 | 从源码构建 | `apps/desktop` 的 electron-builder Windows 目标（nsis/portable），见下文构建 |

## 关键实现

### 1. 启动器（StartMultica.exe）

纯 Go 程序，启动流程：

1. 若 `data/pgdata` 不存在，先 `initdb`（便携 PG 自带）初始化数据目录
2. `pg_ctl start` 启动 PG，监听 `127.0.0.1:5433`（避开默认 5432 冲突）
3. 若数据库 `multica` 不存在，`createdb` 创建
4. 以 `DATABASE_URL=postgres://multica:multica@127.0.0.1:5433/multica?sslmode=disable` 运行 `migrate.exe up`
5. 以同一 `DATABASE_URL` 启动 `server.exe`（监听 8080）
6. 等待 `/health` 就绪
7. 启动 Electron 客户端 `Multica.exe`
8. 常驻等待；收到 `StopMultica.exe` / 用户关闭时，按序停止 client → server → PG

> **关键细节**：`DATABASE_URL` 使用 `127.0.0.1:5433` 而非 `localhost`。`scripts/ensure-postgres.sh` 对 localhost 强制 Docker 分支，用 127.0.0.1 可天然避开（且本方案不调用该脚本，启动器自管 PG）。

### 2. agent daemon（本机 agent）

1. 安装 AI CLI：Claude Code / Codex / OpenCode 等任选其一，确保在 PATH（离线安装方式见各 CLI 官方离线步骤）
2. `multica config set server_url http://127.0.0.1:8080`
3. `multica config set app_url http://127.0.0.1:3000`（Electron 客户端以 8080 为准，此项仅作一致性）
4. `multica login`（单机可配置固定验证码，见下）
5. `multica daemon start`

### 3. 登录验证码（离线无邮件）

`.env` 或启动器环境注入：
```
APP_ENV=development
MULTICA_DEV_VERIFICATION_CODE=888888
```
> 仅限单机离线安全环境使用；若未来对外暴露必须移除。

### 4. 数据持久化

- PG 数据 → `data/pgdata`（`initdb` 时指定）
- 上传附件 → server 的 `LOCAL_UPLOAD_DIR` 指向 `data/uploads`
- 全部随目录移动，备份 = 复制 `data/` 目录

## 构建步骤（在联网机器，Windows 或任意平台）

### 后端三件套（任意平台交叉编译）

```bash
cd server
GOOS=windows GOARCH=amd64 CGO_ENABLED=0 go build -o server.exe ./cmd/server
GOOS=windows GOARCH=amd64 CGO_ENABLED=0 go build -o migrate.exe ./cmd/migrate
GOOS=windows GOARCH=amd64 CGO_ENABLED=0 go build -o multica.exe ./cmd/multica
```
已在本仓库验证编译成功（server 73MB / migrate 15MB / multica 22MB）。

### Electron 客户端（需在 Windows 上构建）

```bash
cd apps/desktop
pnpm install
pnpm build                 # electron-vite build + bundle-cli
pnpm package -- --win      # electron-builder 出 NSIS / portable 安装包
```
产物：`multica-desktop-<version>-windows-x64.exe`。

### 组装目录

将便携 PG zip、三个 EXE、Electron 安装包、启动器放入 `multica-win/`，拷入离线 Windows。

## 验证清单

- [ ] `StartMultica.exe` 双击后 PG 启动、迁移完成、后端 /health 200
- [ ] Electron 客户端打开并登录（固定验证码）
- [ ] `multica daemon status` 显示 running 且检测到 Claude Code
- [ ] 指派 issue 给 agent，任务在本地执行并回报
- [ ] `StopMultica.exe` 后进程全部退出，重新双击数据仍在

## 风险与后续事项

- **Electron 构建需在 Windows 机器执行**（electron-builder 对 Windows 目标跨平台支持有限），这是离线准备阶段唯一"必须有 Windows"的步骤
- 单机 `APP_ENV=development` 固定码仅限个人离线；若扩展多人需改走 SMTP 验证码
- 若未来要多机访问，可把 server + PG 迁到内网服务器，客户端不变——架构天然支持
- 文档不覆盖 `apps/web`（Next.js）；如未来要浏览器访问，需另行评估 Node 运行时方案
