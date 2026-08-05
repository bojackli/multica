# 内网完全离线部署手册

场景目标：在**完全离线**的内网环境中部署 Multica 服务端（跑在内网 VPS 上），并且内网 `Windows` 机器运行 daemon + `Claude Code` 作为 agent，通过**直接 IP** 访问 Web 界面。

> 适用范围：VPS 无外网、Windows 机无外网的封闭内网。
> 操作方法：一切构件在**联网工作机**上下载/打包，再用 U 盘 / scp / 内网传输进内网。

---

## 架构回顾

Multica 分两层，部署位置不同：

| 组件 | 部署位置 | 说明 |
|------|----------|------|
| 服务端（Web + API + PostgreSQL） | 内网 VPS | Docker Compose，3 个镜像 |
| multica daemon（调度 agent） | 每台 Windows | 执行 Claude Code 任务 |

关键点：**daemon 不是跑在 VPS 上，而是跑在使用者各自的 Windows 机器上**。VPS 只做调度中心 + Web 界面。

---

## 依赖清单（必须全部离线准备）

| 构件 | 来源 | VPS / Windows |
|------|------|---------------|
| Docker / Docker Compose | VPS 预装 或 离线安装 | VPS |
| `pgvector/pgvector:pg17` 镜像 | Docker Hub | VPS |
| `ghcr.io/multica-ai/multica-backend` | GHCR | VPS |
| `ghcr.io/multica-ai/multica-web` | GHCR | VPS |
| Multica 仓库文件 | GitHub | VPS（compose 配置） |
| `multica.exe`（daemon） | GitHub Releases | Windows |
| Claude Code（`@anthropic-ai/claude-code`） | npm registry | Windows |
| Anthropic API key | 联网时生成 | Windows |

> 提前确定并统一一个**已发布的稳定版本号**，记为 `MULTICA_TAG`（如 `v0.2.4`），镜像和 CLI 都用它，避免版本漂移。

---

## 第 1 步：联网工作机准备构件

### 1.1 拉取并导出 Docker 镜像

```bash
MULTICA_TAG=v0.x.x   # 替换为实际发布的稳定版本号

docker pull pgvector/pgvector:pg17
docker pull ghcr.io/multica-ai/multica-backend:$MULTICA_TAG
docker pull ghcr.io/multica-ai/multica-web:$MULTICA_TAG

mkdir -p images
docker save pgvector/pgvector:pg17 -o images/postgres.tar
docker save ghcr.io/multica-ai/multica-backend:$MULTICA_TAG -o images/backend.tar
docker save ghcr.io/multica-ai/multica-web:$MULTICA_TAG -o images/web.tar
```

### 1.2 下载 Windows CLI

从 GitHub Releases 下载对应版本：

```
https://github.com/multica-ai/multica/releases/download/v${MULTICA_TAG#v}/multica-cli-${MULTICA_TAG#v}-windows-amd64.zip
```

### 1.3 打包仓库

离线机无法 `git clone`，直接拷贝整个仓库目录（内含 `docker-compose.selfhost.yml`、`.env.example`）：

```bash
git clone https://github.com/multica-ai/multica.git
tar -czf multica-repo.tar.gz multica
```

### 1.4 准备 Claude Code（离线 Windows 上的关键难点）

Claude Code 是 npm 包，离线安装有两条路：

- **方案 A（推荐）**：在联网 Windows 上完成安装 + 登录，把整个用户配置目录拷给离线机。
  ```powershell
  # 联网机上
  npm install -g @anthropic-ai/claude-code
  claude --login
  # 拷贝 ~/.claude（Linux/mac）或 %USERPROFILE%\.claude（Windows）+ 全局 npm node_modules
  ```
- **方案 B（纯离线装）**：在公司内网 npm 镜像上把 `@anthropic-ai/claude-code` 及其全部依赖拉下来打包；或使用 pnpm 离线 store（`pnpm store` / `.local`）拷贝。

无论哪种，Claude Code 都需要一个 `ANTHROPIC_API_KEY`（联网时生成），离线机上直接配置使用即可。注意保管、勿泄露。

---

## 第 2 步：部署到内网 VPS

### 2.1 导入镜像

```bash
scp -r images/ multica.tar.gz user@192.168.1.100:/opt/
```

到 VPS 上：

```bash
mkdir -p /opt/multica /opt/images
# 解压仓库到 /opt/multica
tar xzf /opt/src/multica.tar.gz -C /opt/multica --strip-components=1
# 导入镜像
cd /opt/images && for f in *.tar; do docker load -i $f; done
```

### 2.2 配置 `.env`

```bash
cd /opt/multica && cp .env.example .env
```

编辑，至少改动（把 `192.168.1.100` 换成 VPS 实际内网 IP）：

```bash
JWT_SECRET=$(openssl rand -hex 32)
POSTGRES_PASSWORD=<强密码>
MULTICA_IMAGE_TAG=v0.2.4
MULTICA_VCS_SECRET_KEY=$(openssl rand -base64 32)

FRONTEND_ORIGIN=http://192.168.1.100:3000
MULTICA_APP_URL=http://192.168.1.100:3000
MULTICA_PUBLIC_URL=http://192.168.1.100:8080
NEXT_PUBLIC_API_URL=http://192.168.1.100:8080

ALLOW_SIGNUP=true     # 首登后可改为 false 锁死注册
```

### 2.3 修改端口绑定（关键）

默认 `docker-compose.selfhost.yml` 把端口绑在 `127.0.0.1`，内网 Windows 连不上。把两处绑定改为 `0.0.0.0`：

- `backend` 服务 `ports:`：`127.0.0.1:8080:8080` → `0.0.0.0:8080:8080`
- `frontend` 服务 `ports:`：`127.0.0.1:3000:3000` → `0.0.0.0:3000:3000`

> **安全警告**：Docker 会绕过主机防火墙（UFW/iptables）。开放 `0.0.0.0` 后，务必在 VPS 系统防火墙层**只放行内网网段**对 3000/8080 的访问；用 `ALLOWED_EMAIL_DOMAINS` / 事后 `ALLOW_SIGNUP=false` 锁死注册。不要把这个内网服务暴露到公网。

### 2.4 启动并取验证码

```bash
docker compose -f docker-compose.selfhost.yml up -d
docker compose -f docker-compose.selfhost.yml logs -f backend
# 日志中找: [DEV] Verification code for ...
```

> 无邮件服务的替代方案：给 `.env` 加 `APP_ENV=development` + `MULTICA_DEV_VERIFICATION_CODE=888888`（固定码），但**仅限内网安全环境**，公网可达时禁用。

### 2.5 验证服务

```bash
curl http://192.168.1.100:8080/health       # liveness
curl http://192.168.1.100:8080/readyz       # readiness
```

---

## 第 3 步：Windows 机器（daemon + Claude Code）

对每台要跑 agent 的 Windows 机器重复：

### 3.1 安装 multica.exe

1. 解压 `multica-cli-*-windows-amd64.zip`，得到 `multica.exe`。
2. 放到 `%USERPROFILE%\.multica\bin\`。
3. 把该目录加入用户 PATH（`设置 → 系统 → 高级 → 环境变量`）。

### 3.2 安装 Claude Code

按第 1.4 步的方案 A（拷贝配置）或方案 B（离线 npm）安装，并确保 `claude` 在 PATH 中。

### 3.3 配置并连接内网 VPS

```powershell
multica config set server_url http://192.168.1.100:8080
multica config set app_url http://192.168.1.100:3000
multica login          # 输邮箱 + 贴第2.4步的后台验证码
multica daemon start
multica daemon status  # 应显示 running 且检测到 claude
```

---

## 第 4 步：验证

1. 浏览器打开 `http://192.168.1.100:3000`。
2. **Settings → Runtimes**：应看到该 Windows 机器在线。
3. **Settings → Agents**：新建 agent，选择该 runtime → 指派一个 issue 测试。

---

## 常见问题

- **登录无邮件**：用后台日志验证码，或内网固定码（见 2.4）。
- **daemon 检测不到 claude**：确认 `claude` 在 PATH，重启 daemon。
- **Windows 连不上**：确认 VPS 端口绑定是 `0.0.0.0`、防火墙放行、IP/端口一致。
- **更新版本**：重新离线下载新镜像/CLI 及最新仓库，改 `MULTICA_IMAGE_TAG` 后重拉镜像重启即可。

## 参考

- [SELF_HOSTING.md](../SELF_HOSTING.md) — 在线自托管完整指南
- [SELF_HOSTING_ADVANCED.md](../SELF_HOSTING_ADVANCED.md) — 环境变量 / 反向代理 / 高级配置
- [CLI_AND_DAEMON.md](../CLI_AND_DAEMON.md) — CLI 与 daemon 命令参考